package main

import (
	"context"
	"fmt"
	"log"
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
	resyncInterval = 5 * time.Minute
	retryDelay     = 5 * time.Second
	numWorkers     = 5     // Number of concurrent worker goroutines
	maxRetries     = 5     // Maximum retry attempts before dropping

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
	client          dynamic.Interface
	clientset       kubernetes.Interface
	cache           *SecretProviderClassCache
	tokenCache      *TokenCache
	azureTokenCache *AzureTokenCache
	queue           workqueue.TypedRateLimitingInterface[QueueKey]
	gvr             schema.GroupVersionResource
	ctx             context.Context
}

func NewController(client dynamic.Interface, clientset kubernetes.Interface) *Controller {
	return &Controller{
		client:          client,
		clientset:       clientset,
		cache:           NewCache(),
		tokenCache:      NewTokenCache(),
		azureTokenCache: NewAzureTokenCache(),
		queue:           workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[QueueKey]()),
		gvr: schema.GroupVersionResource{
			Group:    "secrets-store.csi.x-k8s.io",
			Version:  "v1",
			Resource: "secretproviderclasses",
		},
		ctx: context.Background(),
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
		log.Printf("Event: ADDED %s/%s (sync enabled, service-account: %s) - enqueuing", namespace, name, serviceAccount)

		// Enqueue for reconciliation
		key := keyFor(namespace, name)
		ctrl.queue.Add(key)
	} else if isSyncEnabled(obj) {
		log.Printf("Event: ADDED %s/%s (sync enabled but missing service-account annotation, skipping)", namespace, name)
	} else {
		log.Printf("Event: ADDED %s/%s (sync disabled, skipping)", namespace, name)
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
			log.Printf("Event: MODIFIED %s/%s (enqueuing for reconciliation)", namespace, name)
			ctrl.queue.Add(key)
		} else {
			log.Printf("Event: MODIFIED %s/%s (missing service-account annotation, removing from cache if present)", namespace, name)
			if inCache {
				ctrl.cache.Delete(namespace, name)
				ctrl.printCache()
			}
		}
	} else if !enabled && inCache {
		// Sync disabled, remove from cache
		log.Printf("Event: MODIFIED %s/%s (sync disabled, removing from cache)", namespace, name)
		ctrl.cache.Delete(namespace, name)
		ctrl.printCache()
	} else {
		log.Printf("Event: MODIFIED %s/%s (sync disabled, skipping)", namespace, name)
	}
}

func (ctrl *Controller) handleDeleted(namespace, name string, inCache bool) {
	if inCache {
		log.Printf("Event: DELETED %s/%s", namespace, name)
		ctrl.cache.Delete(namespace, name)
		ctrl.printCache()
	} else {
		log.Printf("Event: DELETED %s/%s (not in cache, skipping)", namespace, name)
	}
}

func (ctrl *Controller) handleEvent(event watch.Event) {
	obj, ok := event.Object.(*unstructured.Unstructured)
	if !ok {
		log.Printf("Unexpected object type: %T", event.Object)
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
		log.Printf("Event: ERROR %s/%s", namespace, name)
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

	log.Printf("Obtained Kubernetes token for %s/%s, ready for Azure authentication with clientID: %s",
		namespace, serviceAccount, clientID)

	// Debug: Print token snippet
	tokenSnippet := fmt.Sprintf("%s...%s", token[:5], token[len(token)-5:])
	log.Printf("DEBUG: K8s token for %s/%s: %s", namespace, serviceAccount, tokenSnippet)

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

	log.Printf("Obtained Azure AD token for %s/%s, ready for Key Vault access",
		namespace, serviceAccount)

	// Debug: Print Azure token snippet
	azureTokenSnippet := fmt.Sprintf("%s...%s", azureToken[:10], azureToken[len(azureToken)-10:])
	log.Printf("DEBUG: Azure AD token for %s/%s: %s", namespace, serviceAccount, azureTokenSnippet)

	// Extract vault name
	keyvaultName, err := ExtractKeyvaultName(obj)
	if err != nil {
		return fmt.Errorf("missing keyvaultName: %w", err)
	}

	// List secrets from vault
	secrets, err := ListSecrets(ctrl.ctx, keyvaultName, azureToken, azureTokenExpiration)
	if err != nil {
		// Fail the reconciliation - don't continue with empty secrets
		return fmt.Errorf("failed to list secrets from vault %s: %w", keyvaultName, err)
	}

	log.Printf("Found %d secrets in vault %s for %s/%s",
		len(secrets), keyvaultName, namespace, name)
	for _, secret := range secrets {
		log.Printf("  - Secret: %s", secret)
	}

	// List certificates from vault
	certificates, err := ListCertificates(ctrl.ctx, keyvaultName, azureToken, azureTokenExpiration)
	if err != nil {
		// Fail the reconciliation - don't continue with empty certificates
		return fmt.Errorf("failed to list certificates from vault %s: %w", keyvaultName, err)
	}

	log.Printf("Found %d certificates in vault %s for %s/%s",
		len(certificates), keyvaultName, namespace, name)
	for _, cert := range certificates {
		log.Printf("  - Certificate: %s", cert)
	}

	// Update SecretProviderClass with discovered objects
	log.Printf("Updating SecretProviderClass %s/%s with discovered objects", namespace, name)

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
		log.Printf("Processing secretObjects for %s/%s (secrets: %v, certs: %v)",
			namespace, name, enableSecretObjects, enableCertObjects)

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
			log.Printf("Annotation disabled for %s/%s, will clear secretObjects field", namespace, name)
		}
	}

	// Check if update needed
	currentObjects, _, _ := unstructured.NestedString(obj.Object, "spec", "parameters", "objects")
	objectsChanged := DetectChanges(currentObjects, newObjects)

	if !objectsChanged && !secretObjectsChanged {
		log.Printf("No changes detected for %s/%s, skipping update", namespace, name)
		return nil
	}

	// Patch the resource
	timestamp := time.Now().Format(time.RFC3339)
	log.Printf("Updating %s/%s (objects changed: %v, secretObjects changed: %v)",
		namespace, name, objectsChanged, secretObjectsChanged)
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

	log.Printf("Successfully updated %s/%s with %d objects (%d secrets, %d certs)",
		namespace, name, len(discoveredObjects), len(secrets), len(certificates))

	return nil
}

// enqueueAll enqueues all valid resources for reconciliation
func (ctrl *Controller) enqueueAll() {
	log.Println("Periodic resync: enqueuing all tracked resources")

	result, err := ctrl.client.Resource(ctrl.gvr).Namespace("").List(
		ctrl.ctx, metav1.ListOptions{},
	)
	if err != nil {
		log.Printf("Error listing resources for resync: %v", err)
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

	log.Printf("Enqueued %d resources for periodic resync", enqueuedCount)
}

func (ctrl *Controller) syncCache() {
	log.Println("Performing initial sync")
	// Use enqueueAll for initial sync as well
	ctrl.enqueueAll()
}

func (ctrl *Controller) startPeriodicResync() {
	ticker := time.NewTicker(resyncInterval)
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
		log.Printf("Error reconciling %v (attempt %d/%d), retrying: %v", key, numRequeues+1, maxRetries, err)
		ctrl.queue.AddRateLimited(key)
		return
	}

	// Max retries exceeded - give up
	log.Printf("Dropping %v from queue after %d failed attempts: %v", key, maxRetries, err)
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
			log.Printf("Resource %s/%s not found, removing from cache", namespace, name)
			ctrl.cache.Delete(namespace, name)
			ctrl.printCache()
			return nil
		}
		return fmt.Errorf("failed to get resource: %w", err)
	}

	// Validate if resource should be synced
	if valid, _ := isValidForSync(obj); !valid {
		log.Printf("Resource %s/%s not valid for sync, skipping", namespace, name)
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
	log.Printf("Starting %d workers...", numWorkers)
	for range numWorkers {
		go ctrl.worker()
	}

	log.Println("Watching for events...")

	for {
		watcher, err := ctrl.client.Resource(ctrl.gvr).Namespace("").Watch(ctrl.ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("Error creating watcher: %v", err)
			time.Sleep(retryDelay)
			continue
		}

		for event := range watcher.ResultChan() {
			ctrl.handleEvent(event)
		}

		log.Println("Watch connection closed, reconnecting in 5 seconds...")
		watcher.Stop()
		time.Sleep(retryDelay)
	}
}
