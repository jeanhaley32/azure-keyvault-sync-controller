package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
)

const (
	retryDelay = 5 * time.Second
	maxRetries = 5 // Maximum retry attempts before dropping

	annotationEnabled        = "azure-keyvault-sync/enabled"
	annotationServiceAccount = "azure-keyvault-sync/service-account"
	annotationSecretObjects  = "azure-keyvault-sync/secret-objects"
	annotationCertObjects    = "azure-keyvault-sync/cert-objects"
	annotationEnabledValue   = "true"
)

// QueueKey represents a namespaced resource name for the work queue
type QueueKey string

// keyFor creates a queue key from namespace and name
func keyFor(namespace, name string) QueueKey {
	return QueueKey(fmt.Sprintf("%s/%s", namespace, name))
}

// parseKey splits a queue key into namespace and name
func parseKey(key QueueKey) (namespace, name string, err error) {
	parts := strings.SplitN(string(key), "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid key format: %s", key)
	}
	return parts[0], parts[1], nil
}

func isSyncEnabled(obj *unstructured.Unstructured) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	return annotations[annotationEnabled] == annotationEnabledValue
}

func getServiceAccount(obj *unstructured.Unstructured) (string, bool) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return "", false
	}
	sa, exists := annotations[annotationServiceAccount]
	return sa, exists
}

func isValidForSync(obj *unstructured.Unstructured) (bool, string) {
	if !isSyncEnabled(obj) {
		return false, ""
	}
	serviceAccount, hasServiceAccount := getServiceAccount(obj)
	if !hasServiceAccount {
		return false, ""
	}
	return true, serviceAccount
}

type Controller struct {
	client              dynamic.Interface
	clientset           kubernetes.Interface
	cache               *SecretProviderClassCache
	tokenCache          *TokenCache
	azureTokenCache     *AzureTokenCache
	queue               workqueue.TypedRateLimitingInterface[QueueKey]
	gvr                 schema.GroupVersionResource
	ctx                 context.Context
	healthChecker       *HealthChecker
	config              *Config
	watchNamespace      string          // Empty = cluster-wide, set = namespace-scoped
	azureCircuitBreaker *CircuitBreaker // Protects against Azure API failures
}

func NewController(client dynamic.Interface, clientset kubernetes.Interface, config *Config, watchNamespace string) *Controller {
	// Initialize circuit breaker for Azure API protection
	azureCB := NewCircuitBreaker(
		config.AzureCircuitBreakerThreshold,
		config.AzureCircuitBreakerTimeout,
	)

	slog.Info("Azure circuit breaker initialized",
		"threshold", config.AzureCircuitBreakerThreshold,
		"timeout", config.AzureCircuitBreakerTimeout)

	return &Controller{
		client:              client,
		clientset:           clientset,
		cache:               NewCache(),
		tokenCache:          NewTokenCache(),
		azureTokenCache:     NewAzureTokenCache(),
		queue:               workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[QueueKey]()),
		gvr: schema.GroupVersionResource{
			Group:    "secrets-store.csi.x-k8s.io",
			Version:  "v1",
			Resource: "secretproviderclasses",
		},
		ctx:                 context.Background(),
		healthChecker:       NewHealthChecker(),
		config:              config,
		watchNamespace:      watchNamespace,
		azureCircuitBreaker: azureCB,
	}
}

func (ctrl *Controller) printCache() {
	objects := ctrl.cache.List()
	fmt.Printf("\n--- Current SecretProviderClass objects: %d ---\n", len(objects))
	for _, obj := range objects {
		fmt.Printf("  %s/%s\n", obj.GetNamespace(), obj.GetName())
	}
	fmt.Println("---")
}

func (ctrl *Controller) handleAdded(obj *unstructured.Unstructured) {
	namespace := obj.GetNamespace()
	name := obj.GetName()

	if valid, serviceAccount := isValidForSync(obj); valid {
		slog.Info("Event: ADDED - enqueuing", "namespace", namespace, "name", name, "serviceAccount", serviceAccount)

		// Enqueue for reconciliation
		key := keyFor(namespace, name)
		ctrl.queue.Add(key)
	} else if isSyncEnabled(obj) {
		slog.Warn("Event: ADDED - missing service-account annotation", "namespace", namespace, "name", name)
	} else {
		slog.Debug("Event: ADDED - sync disabled", "namespace", namespace, "name", name)
	}
}

func (ctrl *Controller) handleModified(obj *unstructured.Unstructured) {
	namespace := obj.GetNamespace()
	name := obj.GetName()
	key := keyFor(namespace, name)

	enabled := isSyncEnabled(obj)
	inCache := ctrl.cache.Has(namespace, name)

	if enabled {
		// Resource should be synced, enqueue for reconciliation
		_, hasServiceAccount := getServiceAccount(obj)
		if hasServiceAccount {
			slog.Info("Event: MODIFIED - enqueuing", "namespace", namespace, "name", name)
			ctrl.queue.Add(key)
		} else {
			slog.Warn("Event: MODIFIED - missing service-account annotation", "namespace", namespace, "name", name)
			if inCache {
				ctrl.cache.Delete(namespace, name)
				ctrl.printCache()
			}
		}
	} else if !enabled && inCache {
		// Sync disabled, remove from cache
		slog.Info("Event: MODIFIED - removing from cache", "namespace", namespace, "name", name)
		ctrl.cache.Delete(namespace, name)
		ctrl.printCache()
	} else {
		slog.Debug("Event: MODIFIED - sync disabled", "namespace", namespace, "name", name)
	}
}

func (ctrl *Controller) handleDeleted(namespace, name string, inCache bool) {
	if inCache {
		slog.Info("Event: DELETED", "namespace", namespace, "name", name)
		ctrl.cache.Delete(namespace, name)
		ctrl.printCache()
	} else {
		slog.Debug("Event: DELETED - not in cache", "namespace", namespace, "name", name)
	}
}

func (ctrl *Controller) handleEvent(event watch.Event) {
	obj, ok := event.Object.(*unstructured.Unstructured)
	if !ok {
		slog.Warn("Unexpected object type", "type", fmt.Sprintf("%T", event.Object))
		return
	}

	namespace := obj.GetNamespace()
	name := obj.GetName()
	inCache := ctrl.cache.Has(namespace, name)

	switch event.Type {
	case watch.Added:
		ctrl.handleAdded(obj)

	case watch.Modified:
		ctrl.handleModified(obj)

	case watch.Deleted:
		ctrl.handleDeleted(namespace, name, inCache)

	case watch.Error:
		slog.Error("Event: ERROR", "namespace", namespace, "name", name)
	}
}

// reconcileResource performs vault discovery and SecretProviderClass update for a single resource
func (ctrl *Controller) reconcileResource(obj *unstructured.Unstructured) error {
	namespace := obj.GetNamespace()
	name := obj.GetName()

	// Get service account
	serviceAccount, hasServiceAccount := getServiceAccount(obj)
	if !hasServiceAccount {
		return fmt.Errorf("missing service-account annotation")
	}

	// Extract clientID from spec
	clientID, err := ExtractClientID(obj)
	if err != nil {
		return fmt.Errorf("missing clientID: %w", err)
	}

	// Get Kubernetes token
	token, err := ctrl.tokenCache.GetToken(
		ctrl.ctx,
		ctrl.clientset,
		namespace,
		serviceAccount,
	)
	if err != nil {
		return fmt.Errorf("error getting token: %w", err)
	}

	slog.Info("Obtained Kubernetes token",
		    "namespace", namespace, "serviceAccount", serviceAccount, "clientID", clientID)

	// Debug: Print token snippet
	tokenSnippet := fmt.Sprintf("%s...%s", token[:5], token[len(token)-5:])
	slog.Debug("Kubernetes token acquired", "namespace", namespace, "serviceAccount", serviceAccount, "tokenSnippet", tokenSnippet)

	// Extract tenantID
	tenantID, err := ExtractTenantID(obj)
	if err != nil {
		return fmt.Errorf("missing tenantID: %w", err)
	}

	// Get Azure AD token
	azureToken, azureTokenExpiration, err := ctrl.azureTokenCache.GetToken(
		ctrl.ctx,
		namespace,
		serviceAccount,
		token,
		clientID,
		tenantID,
	)
	if err != nil {
		return fmt.Errorf("error getting Azure AD token: %w", err)
	}

	slog.Info("Obtained Azure AD token",
		    "namespace", namespace, "serviceAccount", serviceAccount)

	// Debug: Print Azure token snippet
	azureTokenSnippet := fmt.Sprintf("%s...%s", azureToken[:10], azureToken[len(azureToken)-10:])
	slog.Debug("Azure AD token acquired", "namespace", namespace, "serviceAccount", serviceAccount, "tokenSnippet", azureTokenSnippet)

	// Extract vault name
	keyvaultName, err := ExtractKeyvaultName(obj)
	if err != nil {
		return fmt.Errorf("missing keyvaultName: %w", err)
	}

	// List secrets from vault (protected by circuit breaker)
	var secrets []string
	err = ctrl.azureCircuitBreaker.Call(func() error {
		var listErr error
		secrets, listErr = ListSecrets(ctrl.ctx, keyvaultName, azureToken, azureTokenExpiration)
		return listErr
	})
	if err != nil {
		if err.Error() == "circuit breaker is open" ||
		   strings.Contains(err.Error(), "circuit breaker is open") {
			slog.Warn("Azure circuit breaker open, skipping vault secrets call",
				"vault", keyvaultName,
				"namespace", namespace,
				"name", name)
			// Return nil to allow requeueing without marking as permanent failure
			return nil
		}
		// Fail the reconciliation for other errors
		return fmt.Errorf("failed to list secrets from vault %s: %w", keyvaultName, err)
	}

	slog.Info("Found secrets in vault",
		    "count", len(secrets), "vault", keyvaultName, "namespace", namespace, "name", name)
	for _, secret := range secrets {
		slog.Debug("Vault secret", "name", secret)
	}

	// List certificates from vault (protected by circuit breaker)
	var certificates []string
	err = ctrl.azureCircuitBreaker.Call(func() error {
		var listErr error
		certificates, listErr = ListCertificates(ctrl.ctx, keyvaultName, azureToken, azureTokenExpiration)
		return listErr
	})
	if err != nil {
		if err.Error() == "circuit breaker is open" ||
		   strings.Contains(err.Error(), "circuit breaker is open") {
			slog.Warn("Azure circuit breaker open, skipping vault certificates call",
				"vault", keyvaultName,
				"namespace", namespace,
				"name", name)
			// Return nil to allow requeueing without marking as permanent failure
			return nil
		}
		// Fail the reconciliation for other errors
		return fmt.Errorf("failed to list certificates from vault %s: %w", keyvaultName, err)
	}

	slog.Info("Found certificates in vault",
		"count", len(certificates), "vault", keyvaultName, "namespace", namespace, "name", name)
	for _, cert := range certificates {
		slog.Debug("Vault certificate", "name", cert)
	}

	// Update SecretProviderClass with discovered objects
	slog.Info("Updating SecretProviderClass", "namespace", namespace, "name", name)

	// Generate objects from vault (vault is source of truth)
	discoveredObjects := GenerateObjectsFromVault(secrets, certificates)

	// Format as YAML
	newObjects, err := FormatObjectsYAML(discoveredObjects)
	if err != nil {
		return fmt.Errorf("error formatting objects: %w", err)
	}

	// Process secretObjects
	var secretObjectsToSync interface{}
	var secretObjectsChanged bool
	annotations := obj.GetAnnotations()
	enableSecretObjects := annotations != nil && annotations[annotationSecretObjects] == annotationEnabledValue
	enableCertObjects := annotations != nil && annotations[annotationCertObjects] == annotationEnabledValue

	if enableSecretObjects || enableCertObjects {
		slog.Info("Processing secretObjects",
			"namespace", namespace, "name", name, "generateSecrets", enableSecretObjects, "generateCerts", enableCertObjects)

		// Generate secretObjects from vault + annotations
		generatedSecretObjects := GenerateSecretObjectsFromVault(
			secrets,
			certificates,
			enableSecretObjects,
			enableCertObjects,
		)

		// Check if secretObjects actually changed
		if CompareSecretObjects(obj, generatedSecretObjects) {
			secretObjectsToSync = generatedSecretObjects
			secretObjectsChanged = true
		}
	} else {
		// Check if field exists and needs removal
		existingSecretObjects, found, _ := unstructured.NestedSlice(obj.Object, "spec", "secretObjects")
		if found && len(existingSecretObjects) > 0 {
			secretObjectsToSync = "REMOVE_FIELD"
			secretObjectsChanged = true
			slog.Info("Clearing secretObjects field", "namespace", namespace, "name", name)
		}
	}

	// Check if update needed
	currentObjects, _, _ := unstructured.NestedString(obj.Object, "spec", "parameters", "objects")
	objectsChanged := DetectChanges(currentObjects, newObjects)

	if !objectsChanged && !secretObjectsChanged {
		slog.Debug("No changes detected", "namespace", namespace, "name", name)
		return nil
	}

	// Patch the resource
	timestamp := time.Now().Format(time.RFC3339)
	slog.Info("Applying updates",
		    "namespace", namespace, "name", name, "objectsChanged", objectsChanged, "secretObjectsChanged", secretObjectsChanged)
	err = PatchSecretProviderClass(
		ctrl.ctx,
		ctrl.client,
		namespace,
		name,
		ctrl.gvr,
		newObjects,
		secretObjectsToSync,
		timestamp,
	)
	if err != nil {
		return fmt.Errorf("error patching: %w", err)
	}

	slog.Info("Successfully updated SecretProviderClass",
		"namespace", namespace, "name", name, "totalObjects", len(discoveredObjects), "secrets", len(secrets), "certificates", len(certificates))

	return nil
}

// enqueueAll enqueues all valid resources for reconciliation
func (ctrl *Controller) enqueueAll() {
	if ctrl.watchNamespace != "" {
		slog.Info("Periodic resync starting", "namespace", ctrl.watchNamespace)
	} else {
		slog.Info("Periodic resync starting (cluster-wide)")
	}

	result, err := ctrl.client.Resource(ctrl.gvr).Namespace(ctrl.watchNamespace).List(
		ctrl.ctx, metav1.ListOptions{},
	)
	if err != nil {
		slog.Error("Error listing resources for resync", "error", err)
		return
	}

	enqueuedCount := 0
	for _, item := range result.Items {
		if valid, _ := isValidForSync(&item); valid {
			key := keyFor(item.GetNamespace(), item.GetName())
			ctrl.queue.Add(key)
			enqueuedCount++
		}
	}

	slog.Info("Periodic resync complete", "enqueuedCount", enqueuedCount)
}

func (ctrl *Controller) syncCache() {
	slog.Info("Performing initial sync")
	// Use enqueueAll for initial sync as well
	ctrl.enqueueAll()
}

func (ctrl *Controller) startPeriodicResync() {
	ticker := time.NewTicker(ctrl.config.SyncInterval)
	defer ticker.Stop()
	for range ticker.C {
		ctrl.enqueueAll()
	}
}

// worker processes items from the work queue
func (ctrl *Controller) worker() {
	for ctrl.processNextItem() {
	}
}

// processNextItem processes a single item from the work queue
func (ctrl *Controller) processNextItem() bool {
	// Get next item from queue
	key, shutdown := ctrl.queue.Get()
	if shutdown {
		return false
	}
	defer ctrl.queue.Done(key)

	// Reconcile
	err := ctrl.reconcile(key)

	// Handle result with retry logic
	ctrl.handleReconcileResult(key, err)

	return true
}

// handleReconcileResult handles the result of a reconciliation with retry logic
func (ctrl *Controller) handleReconcileResult(key QueueKey, err error) {
	if err == nil {
		// Success - remove from rate limiter
		ctrl.queue.Forget(key)
		return
	}

	// Check retry count
	numRequeues := ctrl.queue.NumRequeues(key)
	if numRequeues < maxRetries {
		// Retry with exponential backoff
		slog.Warn("Error reconciling resource, retrying", "key", key, "attempt", numRequeues+1, "maxRetries", maxRetries, "error", err)
		ctrl.queue.AddRateLimited(key)
		return
	}

	// Max retries exceeded - give up
	slog.Error("Dropping from queue after max retries", "key", key, "maxRetries", maxRetries, "error", err)
	ctrl.queue.Forget(key)
}

// reconcile performs the actual reconciliation for a queue item
func (ctrl *Controller) reconcile(key QueueKey) error {
	// Parse key
	namespace, name, err := parseKey(key)
	if err != nil {
		return err
	}

	// Get resource from Kubernetes
	obj, err := ctrl.client.Resource(ctrl.gvr).Namespace(namespace).Get(
		ctrl.ctx, name, metav1.GetOptions{},
	)
	if err != nil {
		if errors.IsNotFound(err) {
			// Resource deleted, remove from cache
			slog.Info("Resource not found, removing from cache", "namespace", namespace, "name", name)
			ctrl.cache.Delete(namespace, name)
			ctrl.printCache()
			return nil
		}
		return fmt.Errorf("failed to get resource: %w", err)
	}

	// Validate if resource should be synced
	if valid, _ := isValidForSync(obj); !valid {
		slog.Debug("Resource not valid for sync, skipping", "namespace", namespace, "name", name)
		return nil
	}

	// Perform reconciliation
	err = ctrl.reconcileResource(obj)
	if err != nil {
		return fmt.Errorf("reconciliation failed: %w", err)
	}

	// Update cache
	ctrl.cache.Set(namespace, name, obj.DeepCopy())

	return nil
}

func (ctrl *Controller) Run() {
	defer ctrl.queue.ShutDown()

	ctrl.syncCache()
	ctrl.printCache()

	// Start periodic resync
	go ctrl.startPeriodicResync()

	// Start worker pool
	slog.Info("Starting workers", "count", ctrl.config.WorkerCount)
	for range ctrl.config.WorkerCount {
		go ctrl.worker()
	}
	ctrl.healthChecker.SetWorkersRunning(true)

	if ctrl.watchNamespace != "" {
		slog.Info("Watching for events", "namespace", ctrl.watchNamespace)
	} else {
		slog.Info("Watching for events (cluster-wide)")
	}

	for {
		watcher, err := ctrl.client.Resource(ctrl.gvr).Namespace(ctrl.watchNamespace).Watch(ctrl.ctx, metav1.ListOptions{})
		if err != nil {
			slog.Error("Error creating watcher", "error", err)
			ctrl.healthChecker.SetWatchConnected(false)
			time.Sleep(retryDelay)
			continue
		}

		// Mark watch as connected
		ctrl.healthChecker.SetWatchConnected(true)
		slog.Info("Watch connected successfully")

		for event := range watcher.ResultChan() {
			ctrl.handleEvent(event)
			ctrl.healthChecker.UpdateWatchActivity()
		}

		slog.Info("Watch connection closed, reconnecting", "delay", "5s")
		ctrl.healthChecker.SetWatchConnected(false)
		watcher.Stop()
		time.Sleep(retryDelay)
	}
}
