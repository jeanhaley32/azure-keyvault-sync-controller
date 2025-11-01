package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	akvv1alpha1 "github.com/jeanhaley32/azure-keyvault-sync-controller/api/v1alpha1"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/cache"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/circuitbreaker"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/config"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/health"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/token"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/update"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	spcclient "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	retryDelay = 5 * time.Second
	maxRetries = 5 // Maximum retry attempts before dropping

	annotationServiceAccount = "azure-keyvault-sync/service-account"
	annotationRespectTags    = "azure-keyvault-sync/respect-tags"

	// Label keys for tag filtering
	labelService     = "service"
	labelEnvironment = "environment"
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

func getServiceAccount(obj *secretsstorev1.SecretProviderClass) (string, bool) {
	if obj.Annotations == nil {
		return "", false
	}
	sa, exists := obj.Annotations[annotationServiceAccount]
	return sa, exists
}

// isValidForSync checks if a SecretProviderClass should be synced
// The presence of the service-account annotation is an implicit opt-in
func isValidForSync(obj *secretsstorev1.SecretProviderClass) (bool, string) {
	serviceAccount, hasServiceAccount := getServiceAccount(obj)
	if !hasServiceAccount {
		return false, ""
	}
	return true, serviceAccount
}

type Controller struct {
	client              spcclient.Interface
	clientset           kubernetes.Interface
	ctrlClient          client.Client // Controller-runtime client for CRD access
	cache               *cache.SecretProviderClassCache
	tokenCache          *token.TokenCache
	azureTokenCache     *azure.AzureTokenCache
	queue               workqueue.TypedRateLimitingInterface[QueueKey]
	HealthChecker       *health.HealthChecker
	config              *config.Config
	watchNamespace      string          // Empty = cluster-wide, set = namespace-scoped
	azureCircuitBreaker *circuitbreaker.CircuitBreaker // Protects against Azure API failures

	// Injected dependencies for testability
	tokenProvider TokenProvider
	vaultClient   VaultClient
	patchClient   PatchClient
}

func NewController(spcClient spcclient.Interface, clientset kubernetes.Interface, ctrlClient client.Client, config *config.Config, watchNamespace string) *Controller {
	// Initialize circuit breaker for Azure API protection
	azureCB := circuitbreaker.NewCircuitBreaker(
		config.AzureCircuitBreakerThreshold,
		config.AzureCircuitBreakerTimeout,
	)

	slog.Info("Azure circuit breaker initialized",
		"threshold", config.AzureCircuitBreakerThreshold,
		"timeout", config.AzureCircuitBreakerTimeout)

	// Create caches
	tokenCache := token.NewTokenCache()
	azureTokenCache := azure.NewAzureTokenCache()

	// Create real implementations of interfaces
	tokenProvider := NewRealTokenProvider(tokenCache, azureTokenCache)
	vaultClient := NewRealVaultClient()
	patchClient := NewRealPatchClient(spcClient)

	return &Controller{
		client:              spcClient,
		clientset:           clientset,
		ctrlClient:          ctrlClient,
		cache:               cache.NewCache(),
		tokenCache:          tokenCache,
		azureTokenCache:     azureTokenCache,
		queue:               workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[QueueKey]()),
		HealthChecker:       health.NewHealthChecker(),
		config:              config,
		watchNamespace:      watchNamespace,
		azureCircuitBreaker: azureCB,
		tokenProvider:       tokenProvider,
		vaultClient:         vaultClient,
		patchClient:         patchClient,
	}
}

func (ctrl *Controller) printCache() {
	objects := ctrl.cache.List()
	fmt.Printf("\n--- Current SecretProviderClass objects: %d ---\n", len(objects))
	for _, obj := range objects {
		fmt.Printf("  %s/%s\n", obj.Namespace, obj.Name)
	}
	fmt.Println("---")
}

func (ctrl *Controller) handleAdded(obj *secretsstorev1.SecretProviderClass) {
	namespace := obj.Namespace
	name := obj.Name

	if valid, serviceAccount := isValidForSync(obj); valid {
		slog.Info("Event: ADDED - enqueuing", "namespace", namespace, "name", name, "serviceAccount", serviceAccount)

		// Enqueue for reconciliation
		key := keyFor(namespace, name)
		ctrl.queue.Add(key)
	} else {
		slog.Debug("Event: ADDED - no service-account annotation", "namespace", namespace, "name", name)
	}
}

func (ctrl *Controller) handleModified(obj *secretsstorev1.SecretProviderClass) {
	namespace := obj.Namespace
	name := obj.Name
	key := keyFor(namespace, name)

	valid, serviceAccount := isValidForSync(obj)
	inCache := ctrl.cache.Has(namespace, name)

	if valid {
		// Resource has service-account annotation, enqueue for reconciliation
		slog.Info("Event: MODIFIED - enqueuing", "namespace", namespace, "name", name, "serviceAccount", serviceAccount)
		ctrl.queue.Add(key)
	} else if inCache {
		// Service-account annotation removed, remove from cache
		slog.Info("Event: MODIFIED - removing from cache", "namespace", namespace, "name", name)
		ctrl.cache.Delete(namespace, name)
		ctrl.printCache()
	} else {
		slog.Debug("Event: MODIFIED - no service-account annotation", "namespace", namespace, "name", name)
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
	obj, ok := event.Object.(*secretsstorev1.SecretProviderClass)
	if !ok {
		slog.Warn("Unexpected object type", "type", fmt.Sprintf("%T", event.Object))
		return
	}

	namespace := obj.Namespace
	name := obj.Name
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
func (ctrl *Controller) reconcileResource(ctx context.Context, obj *secretsstorev1.SecretProviderClass) error {
	namespace := obj.Namespace
	name := obj.Name

	// Get service account
	serviceAccount, hasServiceAccount := getServiceAccount(obj)
	if !hasServiceAccount {
		return fmt.Errorf("missing service-account annotation")
	}

	// Extract clientID from spec
	if obj.Spec.Parameters == nil {
		return fmt.Errorf("spec.parameters is nil")
	}
	clientID, ok := obj.Spec.Parameters["clientID"]
	if !ok {
		return fmt.Errorf("missing clientID in spec.parameters")
	}

	// Get Kubernetes token
	token, err := ctrl.tokenProvider.GetK8sToken(
		ctx,
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
	tenantID, ok := obj.Spec.Parameters["tenantId"]
	if !ok {
		return fmt.Errorf("missing tenantId in spec.parameters")
	}

	// Get Azure AD token
	azureToken, azureTokenExpiration, err := ctrl.tokenProvider.GetAzureToken(
		ctx,
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
	keyvaultName, ok := obj.Spec.Parameters["keyvaultName"]
	if !ok {
		return fmt.Errorf("missing keyvaultName in spec.parameters")
	}

	// List secrets from vault with tags (protected by circuit breaker)
	var vaultSecrets []azure.VaultSecret
	err = ctrl.azureCircuitBreaker.Call(func() error {
		var listErr error
		vaultSecrets, listErr = ctrl.vaultClient.ListSecrets(ctx, keyvaultName, azureToken, azureTokenExpiration)
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
		    "count", len(vaultSecrets), "vault", keyvaultName, "namespace", namespace, "name", name)

	// List certificates from vault with tags (protected by circuit breaker)
	var vaultCertificates []azure.VaultCertificate
	err = ctrl.azureCircuitBreaker.Call(func() error {
		var listErr error
		vaultCertificates, listErr = ctrl.vaultClient.ListCertificates(ctx, keyvaultName, azureToken, azureTokenExpiration)
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
		"count", len(vaultCertificates), "vault", keyvaultName, "namespace", namespace, "name", name)

	// Apply tag filtering if enabled
	annotations := obj.Annotations
	respectTags := annotations != nil && annotations[annotationRespectTags] == "true"

	var secrets []string
	var certificates []string

	if respectTags {
		// Extract service and environment labels
		labels := obj.Labels
		serviceLabel := ""
		environmentLabel := ""
		if labels != nil {
			serviceLabel = labels[labelService]
			environmentLabel = labels[labelEnvironment]
		}

		slog.Info("Tag filtering enabled",
			"namespace", namespace, "name", name,
			"serviceLabel", serviceLabel, "environmentLabel", environmentLabel)

		// Create filter config
		filterConfig := azure.TagFilterConfig{
			RespectTags:      true,
			ServiceLabel:     serviceLabel,
			EnvironmentLabel: environmentLabel,
		}

		// Filter secrets
		var rejectedSecrets int
		for _, vaultSecret := range vaultSecrets {
			result := azure.MatchesTags(vaultSecret.Tags, filterConfig)
			if result.Include {
				secrets = append(secrets, vaultSecret.Name)
				slog.Debug("Secret included", "name", vaultSecret.Name, "tags", vaultSecret.Tags)
			} else {
				rejectedSecrets++
				slog.Info("Secret rejected by tag filter",
					"secret", vaultSecret.Name,
					"vault", keyvaultName,
					"namespace", namespace,
					"name", name,
					"reason", result.Reason,
					"vaultTags", vaultSecret.Tags,
					"spcService", serviceLabel,
					"spcEnvironment", environmentLabel)
			}
		}

		// Filter certificates
		var rejectedCerts int
		for _, vaultCert := range vaultCertificates {
			result := azure.MatchesTags(vaultCert.Tags, filterConfig)
			if result.Include {
				certificates = append(certificates, vaultCert.Name)
				slog.Debug("Certificate included", "name", vaultCert.Name, "tags", vaultCert.Tags)
			} else {
				rejectedCerts++
				slog.Info("Certificate rejected by tag filter",
					"certificate", vaultCert.Name,
					"vault", keyvaultName,
					"namespace", namespace,
					"name", name,
					"reason", result.Reason,
					"vaultTags", vaultCert.Tags,
					"spcService", serviceLabel,
					"spcEnvironment", environmentLabel)
			}
		}

		slog.Info("Tag filtering complete",
			"namespace", namespace, "name", name,
			"secretsIncluded", len(secrets), "secretsRejected", rejectedSecrets,
			"certsIncluded", len(certificates), "certsRejected", rejectedCerts)
	} else {
		// Tag filtering disabled - include all secrets and certificates
		for _, vaultSecret := range vaultSecrets {
			secrets = append(secrets, vaultSecret.Name)
		}
		for _, vaultCert := range vaultCertificates {
			certificates = append(certificates, vaultCert.Name)
		}

		slog.Debug("Tag filtering disabled, including all secrets and certificates")
	}

	slog.Info("Secrets to sync", "count", len(secrets), "vault", keyvaultName, "namespace", namespace, "name", name)
	for _, secret := range secrets {
		slog.Debug("Vault secret", "name", secret)
	}
	slog.Info("Certificates to sync", "count", len(certificates), "vault", keyvaultName, "namespace", namespace, "name", name)
	for _, cert := range certificates {
		slog.Debug("Vault certificate", "name", cert)
	}

	// Update SecretProviderClass with discovered objects
	slog.Info("Updating SecretProviderClass", "namespace", namespace, "name", name)

	// Generate objects from vault (vault is source of truth)
	discoveredObjects := update.GenerateObjectsFromVault(secrets, certificates)

	// Format as YAML
	newObjects, err := update.FormatObjectsYAML(discoveredObjects)
	if err != nil {
		return fmt.Errorf("error formatting objects: %w", err)
	}

	// Process secretObjects - vault tags control K8s Secret generation
	// Build VaultSecretWithTags and VaultCertWithTags slices with tags
	var secretsWithTags []update.VaultSecretWithTags
	var certsWithTags []update.VaultCertWithTags

	// Keep the filtered vault objects with their tags for secretObjects generation
	for _, vaultSecret := range vaultSecrets {
		// Only include if it passed tag filtering (or filtering disabled)
		included := false
		for _, name := range secrets {
			if name == vaultSecret.Name {
				included = true
				break
			}
		}
		if included {
			secretsWithTags = append(secretsWithTags, update.VaultSecretWithTags{
				Name: vaultSecret.Name,
				Tags: vaultSecret.Tags,
			})
		}
	}

	for _, vaultCert := range vaultCertificates {
		// Only include if it passed tag filtering (or filtering disabled)
		included := false
		for _, name := range certificates {
			if name == vaultCert.Name {
				included = true
				break
			}
		}
		if included {
			certsWithTags = append(certsWithTags, update.VaultCertWithTags{
				Name: vaultCert.Name,
				Tags: vaultCert.Tags,
			})
		}
	}

	slog.Info("Processing secretObjects from vault tags",
		"namespace", namespace, "name", name,
		"totalSecrets", len(secretsWithTags),
		"totalCerts", len(certsWithTags))

	// Generate secretObjects based on vault tags (secret-object=true, cert-object=true)
	generatedSecretObjects := update.GenerateSecretObjectsFromVault(secretsWithTags, certsWithTags)

	var secretObjectsToSync interface{}
	var secretObjectsChanged bool

	// Check if secretObjects actually changed
	if len(generatedSecretObjects) > 0 {
		if update.CompareSecretObjects(obj, generatedSecretObjects) {
			secretObjectsToSync = generatedSecretObjects
			secretObjectsChanged = true
			slog.Info("SecretObjects changed", "namespace", namespace, "name", name,
				"existingCount", len(obj.Spec.SecretObjects),
				"generatedCount", len(generatedSecretObjects))
		} else {
			slog.Info("SecretObjects unchanged", "namespace", namespace, "name", name,
				"count", len(generatedSecretObjects))
		}
	} else {
		// No secrets opted into K8s Secret generation - clear secretObjects field if present
		if len(obj.Spec.SecretObjects) > 0 {
			secretObjectsToSync = "REMOVE_FIELD"
			secretObjectsChanged = true
			slog.Info("No secrets opted into K8s Secret generation - clearing secretObjects field",
				"namespace", namespace, "name", name,
				"existingCount", len(obj.Spec.SecretObjects))
		} else {
			slog.Debug("SecretObjects already empty", "namespace", namespace, "name", name)
		}
	}

	// Check if update needed
	currentObjects := obj.Spec.Parameters["objects"]
	objectsChanged := update.DetectChanges(currentObjects, newObjects)

	if !objectsChanged && !secretObjectsChanged {
		slog.Info("No changes detected - skipping patch",
			"namespace", namespace, "name", name,
			"objectsChanged", objectsChanged,
			"secretObjectsChanged", secretObjectsChanged)
		return nil
	}

	// Patch the resource
	timestamp := time.Now().Format(time.RFC3339)
	slog.Info("Changes detected - applying patch",
		    "namespace", namespace, "name", name, "objectsChanged", objectsChanged, "secretObjectsChanged", secretObjectsChanged)
	err = ctrl.patchClient.PatchSecretProviderClass(
		ctx,
		namespace,
		name,
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
func (ctrl *Controller) enqueueAll(ctx context.Context) {
	if ctrl.watchNamespace != "" {
		slog.Info("Periodic resync starting", "namespace", ctrl.watchNamespace)
	} else {
		slog.Info("Periodic resync starting (cluster-wide)")
	}

	result, err := ctrl.client.SecretsstoreV1().SecretProviderClasses(ctrl.watchNamespace).List(
		ctx, metav1.ListOptions{},
	)
	if err != nil {
		slog.Error("Error listing resources for resync", "error", err)
		return
	}

	enqueuedCount := 0
	for i := range result.Items {
		item := &result.Items[i]
		if valid, _ := isValidForSync(item); valid {
			key := keyFor(item.Namespace, item.Name)
			ctrl.queue.Add(key)
			enqueuedCount++
		}
	}

	slog.Info("Periodic resync complete", "enqueuedCount", enqueuedCount)
}

func (ctrl *Controller) syncCache(ctx context.Context) {
	slog.Info("Performing initial sync")
	// Use enqueueAll for initial sync as well
	ctrl.enqueueAll(ctx)
}

func (ctrl *Controller) startPeriodicResync(ctx context.Context) {
	ticker := time.NewTicker(ctrl.config.SyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("Periodic resync stopped due to shutdown")
			return
		case <-ticker.C:
			ctrl.enqueueAll(ctx)
		}
	}
}

// worker processes items from the work queue
func (ctrl *Controller) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			slog.Debug("Worker stopping due to shutdown")
			return
		default:
			if !ctrl.processNextItem(ctx) {
				return
			}
		}
	}
}

// drainQueue waits for the work queue to be drained with a timeout
func (ctrl *Controller) drainQueue() {
	drainTimeout := 20 * time.Second
	slog.Info("Draining work queue", "timeout", drainTimeout, "queueLength", ctrl.queue.Len())

	deadline := time.Now().Add(drainTimeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		queueLen := ctrl.queue.Len()
		if queueLen == 0 {
			slog.Info("Work queue successfully drained")
			return
		}

		slog.Debug("Waiting for queue to drain", "remainingItems", queueLen)
		<-ticker.C
	}

	remainingItems := ctrl.queue.Len()
	if remainingItems > 0 {
		slog.Warn("Queue drain timeout exceeded", "remainingItems", remainingItems)
	}
}

// processNextItem processes a single item from the work queue
func (ctrl *Controller) processNextItem(ctx context.Context) bool {
	// Get next item from queue
	key, shutdown := ctrl.queue.Get()
	if shutdown {
		return false
	}
	defer ctrl.queue.Done(key)

	// Reconcile
	err := ctrl.reconcile(ctx, key)

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
func (ctrl *Controller) reconcile(ctx context.Context, key QueueKey) error {
	// Parse key
	namespace, name, err := parseKey(key)
	if err != nil {
		return err
	}

	// Get resource from Kubernetes
	obj, err := ctrl.client.SecretsstoreV1().SecretProviderClasses(namespace).Get(
		ctx, name, metav1.GetOptions{},
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
	err = ctrl.reconcileResource(ctx, obj)
	if err != nil {
		return fmt.Errorf("reconciliation failed: %w", err)
	}

	// Update cache
	ctrl.cache.Set(namespace, name, obj.DeepCopy())

	return nil
}

// watchAzureKeyVaultSync watches AzureKeyVaultSync CRDs and reconciles them
func (ctrl *Controller) watchAzureKeyVaultSync(ctx context.Context) {
	slog.Info("Starting AzureKeyVaultSync CRD watcher")

	// Use a ticker for periodic reconciliation
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("AzureKeyVaultSync watcher shutting down")
			return
		case <-ticker.C:
			// List all AzureKeyVaultSync resources
			var akvList akvv1alpha1.AzureKeyVaultSyncList
			listOpts := []client.ListOption{}

			// Apply namespace filter if configured
			if ctrl.watchNamespace != "" {
				listOpts = append(listOpts, client.InNamespace(ctrl.watchNamespace))
			}

			if err := ctrl.ctrlClient.List(ctx, &akvList, listOpts...); err != nil {
				slog.Error("Failed to list AzureKeyVaultSync resources", "error", err)
				continue
			}

			slog.Debug("Found AzureKeyVaultSync resources", "count", len(akvList.Items))

			// Reconcile each resource
			for _, akv := range akvList.Items {
				if err := ctrl.reconcileAzureKeyVaultSync(ctx, &akv); err != nil {
					slog.Error("Failed to reconcile AzureKeyVaultSync",
						"namespace", akv.Namespace,
						"name", akv.Name,
						"error", err)
				}
			}
		}
	}
}

// reconcileAzureKeyVaultSync reconciles a single AzureKeyVaultSync resource
func (ctrl *Controller) reconcileAzureKeyVaultSync(ctx context.Context, akv *akvv1alpha1.AzureKeyVaultSync) error {
	slog.Info("Reconciling AzureKeyVaultSync",
		"namespace", akv.Namespace,
		"name", akv.Name,
		"vault", akv.Spec.KeyvaultName)

	// TODO: Implement full reconciliation logic
	// This will be implemented in the next steps

	return nil
}

func (ctrl *Controller) Run(ctx context.Context) {
	defer ctrl.queue.ShutDown()

	ctrl.syncCache(ctx)
	ctrl.printCache()

	// Start periodic resync
	go ctrl.startPeriodicResync(ctx)

	// Start AzureKeyVaultSync CRD watcher
	go ctrl.watchAzureKeyVaultSync(ctx)

	// Start worker pool
	slog.Info("Starting workers", "count", ctrl.config.WorkerCount)
	for range ctrl.config.WorkerCount {
		go ctrl.worker(ctx)
	}
	ctrl.HealthChecker.SetWorkersRunning(true)

	if ctrl.watchNamespace != "" {
		slog.Info("Watching for events", "namespace", ctrl.watchNamespace)
	} else {
		slog.Info("Watching for events (cluster-wide)")
	}

	for {
		// Check if context is cancelled before creating watcher
		select {
		case <-ctx.Done():
			slog.Info("Shutdown signal received, stopping watch loop")
			ctrl.HealthChecker.SetWorkersRunning(false)
			ctrl.HealthChecker.SetWatchConnected(false)
			ctrl.drainQueue()
			return
		default:
		}

		watcher, err := ctrl.client.SecretsstoreV1().SecretProviderClasses(ctrl.watchNamespace).Watch(ctx, metav1.ListOptions{})
		if err != nil {
			// Check if error is due to context cancellation
			if ctx.Err() != nil {
				slog.Info("Watch cancelled due to shutdown")
				ctrl.HealthChecker.SetWorkersRunning(false)
				ctrl.HealthChecker.SetWatchConnected(false)
				ctrl.drainQueue()
				return
			}

			slog.Error("Error creating watcher", "error", err)
			ctrl.HealthChecker.SetWatchConnected(false)
			time.Sleep(retryDelay)
			continue
		}

		// Mark watch as connected
		ctrl.HealthChecker.SetWatchConnected(true)
		slog.Info("Watch connected successfully")

		// Watch loop with context cancellation check
	watchLoop:
		for {
			select {
			case <-ctx.Done():
				slog.Info("Shutdown signal received, stopping watch")
				watcher.Stop()
				ctrl.HealthChecker.SetWorkersRunning(false)
				ctrl.HealthChecker.SetWatchConnected(false)
				ctrl.drainQueue()
				return
			case event, ok := <-watcher.ResultChan():
				if !ok {
					// Channel closed, reconnect
					break watchLoop
				}
				ctrl.handleEvent(event)
				ctrl.HealthChecker.UpdateWatchActivity()
			}
		}

		slog.Info("Watch connection closed, reconnecting", "delay", "5s")
		ctrl.HealthChecker.SetWatchConnected(false)
		watcher.Stop()

		// Check context before sleeping
		select {
		case <-ctx.Done():
			slog.Info("Shutdown during reconnect delay")
			ctrl.HealthChecker.SetWorkersRunning(false)
			ctrl.drainQueue()
			return
		case <-time.After(retryDelay):
		}
	}
}
