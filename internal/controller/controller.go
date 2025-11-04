package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
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
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	spcclient "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	retryDelay = 5 * time.Second
	maxRetries = 5 // Maximum retry attempts before dropping

	annotationServiceAccount = "azure-keyvault-sync/service-account"

	// Label keys for service/environment filtering (multi-tenant vaults)
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

func (ctrl *Controller) handleDeleted(obj *secretsstorev1.SecretProviderClass, inCache bool) {
	// Nil-check guard
	if obj == nil {
		slog.Debug("handleDeleted called with nil obj")
		return
	}

	namespace := obj.Namespace
	name := obj.Name

	// Cache cleanup (conditional on inCache)
	if inCache {
		slog.Info("Event: DELETED", "namespace", namespace, "name", name)
		ctrl.cache.Delete(namespace, name)
		ctrl.printCache()
	} else {
		slog.Debug("Event: DELETED (not in cache)", "namespace", namespace, "name", name)
	}

	// Owner-check and immediate reconciliation (always runs, regardless of cache state)
	ctrl.handleOwnedSPCDeletion(obj)
}

// handleOwnedSPCDeletion checks if a deleted SPC is owned by an AzureKeyVaultSync CRD
// and enqueues the owner for immediate reconciliation to recreate the SPC.
// This provides fast recovery (seconds) instead of waiting for periodic sync (up to 5 minutes).
// Uses the workqueue pattern for bounded concurrency, deduplication, and rate limiting.
func (ctrl *Controller) handleOwnedSPCDeletion(obj *secretsstorev1.SecretProviderClass) {
	namespace := obj.Namespace
	name := obj.Name

	// Check each owner reference for AzureKeyVaultSync CRD ownership
	for _, ownerRef := range obj.OwnerReferences {
		// Only process the first controlling owner reference matching our CRD type
		if ownerRef.APIVersion != "keyvault.azure.com/v1alpha1" ||
			ownerRef.Kind != "AzureKeyVaultSync" ||
			ownerRef.Controller == nil || !*ownerRef.Controller {
			continue
		}

		slog.Info("Deleted SPC is owned by AzureKeyVaultSync CRD, enqueueing owner for immediate reconciliation",
			"namespace", namespace,
			"spc", name,
			"owner", ownerRef.Name,
			"ownerUID", ownerRef.UID)

		// Enqueue the owner CRD for reconciliation using the workqueue pattern.
		// This provides:
		// - Deduplication: multiple SPC deletions for same owner → single reconcile
		// - Rate limiting: protects Azure API from overload
		// - Bounded concurrency: controlled by worker count
		// - Consistency: uses same reconciliation path as other events
		ownerKey := keyFor(namespace, ownerRef.Name)
		ctrl.queue.Add(ownerKey)

		slog.Debug("Enqueued owner CRD for reconciliation",
			"namespace", namespace,
			"owner", ownerRef.Name,
			"queueKey", ownerKey)

		// Successfully processed first controller owner, no need to check others
		break
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
		ctrl.handleDeleted(obj, inCache)

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
		if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
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
		if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
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

	// Mandatory tag-based filtering (opinionated controller philosophy)
	// Extract service and environment labels for multi-tenant filtering
	labels := obj.Labels
	serviceLabel := ""
	environmentLabel := ""
	if labels != nil {
		serviceLabel = labels[labelService]
		environmentLabel = labels[labelEnvironment]
	}

	// Create filter config for service/environment matching
	filterConfig := azure.TagFilterConfig{
		ServiceLabel:     serviceLabel,
		EnvironmentLabel: environmentLabel,
	}

	var secrets []string
	var certificates []string

	slog.Info("Applying mandatory tag-based filtering",
		"namespace", namespace, "name", name,
		"serviceLabel", serviceLabel, "environmentLabel", environmentLabel)

	// Filter secrets: Must have sync opt-in AND match service/environment (if specified)
	var syncedSecrets, noSyncTag, serviceEnvRejected int
	for _, vaultSecret := range vaultSecrets {
		// Step 1: Check sync opt-in (sync=true OR secret-object=true)
		if !update.ShouldSyncSecret(vaultSecret.Tags) {
			noSyncTag++
			slog.Debug("Secret rejected - no sync opt-in tag",
				"secret", vaultSecret.Name,
				"namespace", namespace,
				"name", name)
			continue
		}

		// Step 2: Check service/environment matching (if labels specified)
		result := azure.MatchesTags(vaultSecret.Tags, filterConfig)
		if result.Include {
			secrets = append(secrets, vaultSecret.Name)
			syncedSecrets++
			slog.Debug("Secret included", "name", vaultSecret.Name, "tags", vaultSecret.Tags)
		} else {
			serviceEnvRejected++
			slog.Info("Secret rejected by service/environment filter",
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

	// Filter certificates: Must have sync opt-in AND match service/environment (if specified)
	var syncedCerts, noCertSyncTag, certServiceEnvRejected int
	for _, vaultCert := range vaultCertificates {
		// Step 1: Check sync opt-in (sync=true OR cert-object=true)
		if !update.ShouldSyncCert(vaultCert.Tags) {
			noCertSyncTag++
			slog.Debug("Certificate rejected - no sync opt-in tag",
				"certificate", vaultCert.Name,
				"namespace", namespace,
				"name", name)
			continue
		}

		// Step 2: Check service/environment matching (if labels specified)
		result := azure.MatchesTags(vaultCert.Tags, filterConfig)
		if result.Include {
			certificates = append(certificates, vaultCert.Name)
			syncedCerts++
			slog.Debug("Certificate included", "name", vaultCert.Name, "tags", vaultCert.Tags)
		} else {
			certServiceEnvRejected++
			slog.Info("Certificate rejected by service/environment filter",
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

	slog.Info("Tag-based filtering complete",
		"namespace", namespace, "name", name,
		"secretsSynced", syncedSecrets, "secretsNoSyncTag", noSyncTag, "secretsServiceEnvRejected", serviceEnvRejected,
		"certsSynced", syncedCerts, "certsNoSyncTag", noCertSyncTag, "certsServiceEnvRejected", certServiceEnvRejected)

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

	// Collect annotations from vault tags (k8s-annotation. prefix)
	spcAnnotations := make(map[string]string)
	for _, vaultSecret := range vaultSecrets {
		// Only process secrets that passed tag filtering
		included := false
		for _, name := range secrets {
			if name == vaultSecret.Name {
				included = true
				break
			}
		}
		if included {
			secretAnnotations := azure.TransformTagsToSPCAnnotations(vaultSecret.Name, vaultSecret.Tags)
			for k, v := range secretAnnotations {
				spcAnnotations[k] = v
			}
		}
	}

	slog.Info("Collected annotations from vault tags",
		"namespace", namespace, "name", name,
		"annotationCount", len(spcAnnotations))

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
		spcAnnotations,
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
		if kerrors.IsNotFound(err) {
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

// generateSecretProviderClass creates a SecretProviderClass from an AzureKeyVaultSync resource
func generateSecretProviderClass(akv *akvv1alpha1.AzureKeyVaultSync, secrets []azure.VaultSecret) *secretsstorev1.SecretProviderClass {
	// Build array of secret objects for the SPC
	var objects []map[string]interface{}

	// Build secretsWithTags for secretObjects generation
	var secretsWithTags []update.VaultSecretWithTags

	// Track sync statistics
	syncedCount := 0
	skippedCount := 0

	for _, secret := range secrets {
		// Check if secret should be synced (has sync=true OR secret-object=true tag)
		if !update.ShouldSyncSecret(secret.Tags) {
			skippedCount++
			slog.Debug("Secret skipped - no sync opt-in tag",
				"secret", secret.Name,
				"namespace", akv.Namespace,
				"name", akv.Name)
			continue
		}

		syncedCount++

		// Create the object entry for parameters
		obj := map[string]interface{}{
			"objectName": secret.Name,
			"objectType": "secret",
		}
		objects = append(objects, obj)

		// Keep secret with tags for secretObjects generation
		secretsWithTags = append(secretsWithTags, update.VaultSecretWithTags{
			Name: secret.Name,
			Tags: secret.Tags,
		})
	}

	slog.Info("Filtered secrets by sync tag for CRD-based SPC",
		"namespace", akv.Namespace,
		"name", akv.Name,
		"syncedCount", syncedCount,
		"skippedCount", skippedCount)

	// Build parameters map
	parameters := map[string]string{
		"keyvaultName": akv.Spec.KeyvaultName,
		"tenantId":     akv.Spec.TenantID,
		"clientID":     akv.Spec.ClientID,
	}

	spc := &secretsstorev1.SecretProviderClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      akv.Name, // SPC name matches CRD name
			Namespace: akv.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: akv.APIVersion,
					Kind:       akv.Kind,
					Name:       akv.Name,
					UID:        akv.UID,
					Controller: boolPtr(true),
					// Set based on deletePolicy
					BlockOwnerDeletion: boolPtr(akv.Spec.DeletePolicy == akvv1alpha1.DeletePolicyCascade),
				},
			},
		},
		Spec: secretsstorev1.SecretProviderClassSpec{
			Provider:   "azure",
			Parameters: parameters,
		},
	}

	// Add objects array to parameters as YAML/JSON
	// The CSI driver expects this as a YAML string
	if len(objects) > 0 {
		objectsYAML, err := buildObjectsArrayString(objects)
		if err != nil {
			slog.Error("Failed to marshal objects array to YAML",
				"namespace", akv.Namespace,
				"name", akv.Name,
				"error", err)
			return nil
		}
		spc.Spec.Parameters["objects"] = objectsYAML
	}

	// Generate secretObjects based on vault tags (secret-object=true)
	// No certificates in CRD mode, so pass empty slice for certs
	generatedSecretObjects := update.GenerateSecretObjectsFromVault(secretsWithTags, nil)
	if len(generatedSecretObjects) > 0 {
		spc.Spec.SecretObjects = generatedSecretObjects
		slog.Info("Generated secretObjects for CRD-based SPC",
			"namespace", akv.Namespace,
			"name", akv.Name,
			"secretObjectCount", len(generatedSecretObjects))
	}

	// Collect annotations and labels from vault tags
	// Annotations: k8s-annotation. prefix
	// Labels: k8s-label. prefix
	// These will be stored in the SPC and applied to Secrets by the Secret watcher
	spcAnnotations := make(map[string]string)
	for _, secret := range secrets {
		// Only process secrets that have sync opt-in tag
		if update.ShouldSyncSecret(secret.Tags) {
			// Collect annotations
			secretAnnotations := azure.TransformTagsToSPCAnnotations(secret.Name, secret.Tags)
			for k, v := range secretAnnotations {
				spcAnnotations[k] = v
			}

			// Collect labels (stored as annotations with different prefix)
			secretLabels := azure.TransformTagsToSPCLabels(secret.Name, secret.Tags)
			for k, v := range secretLabels {
				spcAnnotations[k] = v
			}
		}
	}

	if len(spcAnnotations) > 0 {
		spc.Annotations = spcAnnotations
		slog.Info("Collected annotations and labels from vault tags for CRD-based SPC",
			"namespace", akv.Namespace,
			"name", akv.Name,
			"totalCount", len(spcAnnotations))
	}

	return spc
}

// buildObjectsArrayString builds the objects array string for SPC parameters using safe YAML marshaling
func buildObjectsArrayString(objects []map[string]interface{}) (string, error) {
	// Wrap the objects array in a map with "array" key as expected by the CSI driver
	wrapper := map[string]interface{}{
		"array": objects,
	}

	// Marshal to YAML using safe library to prevent injection vulnerabilities
	yamlBytes, err := yaml.Marshal(wrapper)
	if err != nil {
		return "", fmt.Errorf("failed to marshal objects array to YAML: %w", err)
	}

	return string(yamlBytes), nil
}

// compareSecretObjects compares two SecretObject slices for equality
func compareSecretObjects(existing, desired []*secretsstorev1.SecretObject) bool {
	if len(existing) != len(desired) {
		return false
	}

	// Build maps for comparison
	existingMap := make(map[string]*secretsstorev1.SecretObject)
	for _, obj := range existing {
		existingMap[obj.SecretName] = obj
	}

	// Check each desired object exists and matches
	for _, desiredObj := range desired {
		existingObj, exists := existingMap[desiredObj.SecretName]
		if !exists {
			return false
		}

		// Compare type
		if existingObj.Type != desiredObj.Type {
			return false
		}

		// Compare data array length
		if len(existingObj.Data) != len(desiredObj.Data) {
			return false
		}

		// Build map of existing data for comparison
		existingData := make(map[string]string)
		for _, data := range existingObj.Data {
			existingData[data.Key] = data.ObjectName
		}

		// Check each desired data entry matches
		for _, data := range desiredObj.Data {
			if objectName, exists := existingData[data.Key]; !exists || objectName != data.ObjectName {
				return false
			}
		}
	}

	return true
}

// compareAnnotations compares two annotation maps for equality
// Returns true if they are equal, false if different
func compareAnnotations(existing, desired map[string]string) bool {
	// Different lengths means different
	if len(existing) != len(desired) {
		return false
	}

	// Check each desired annotation exists in existing with same value
	for key, desiredValue := range desired {
		existingValue, exists := existing[key]
		if !exists || existingValue != desiredValue {
			return false
		}
	}

	return true
}

// boolPtr returns a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
}

// reconcileAzureKeyVaultSync reconciles a single AzureKeyVaultSync resource
func (ctrl *Controller) reconcileAzureKeyVaultSync(ctx context.Context, akv *akvv1alpha1.AzureKeyVaultSync) error {
	namespace := akv.Namespace
	name := akv.Name

	slog.Info("Reconciling AzureKeyVaultSync",
		"namespace", namespace,
		"name", name,
		"vault", akv.Spec.KeyvaultName)

	// Step 1: Get Kubernetes token for the service account
	k8sToken, err := ctrl.tokenProvider.GetK8sToken(
		ctx,
		ctrl.clientset,
		namespace,
		akv.Spec.ServiceAccount,
	)
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes token: %w", err)
	}

	// Step 2: Exchange for Azure AD token
	azureToken, azureTokenExpiration, err := ctrl.tokenProvider.GetAzureToken(
		ctx,
		namespace,
		akv.Spec.ServiceAccount,
		k8sToken,
		akv.Spec.ClientID,
		akv.Spec.TenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to get Azure token: %w", err)
	}

	// Step 3: List secrets from vault (protected by circuit breaker)
	var vaultSecrets []azure.VaultSecret
	err = ctrl.azureCircuitBreaker.Call(func() error {
		var listErr error
		vaultSecrets, listErr = ctrl.vaultClient.ListSecrets(ctx, akv.Spec.KeyvaultName, azureToken, azureTokenExpiration)
		return listErr
	})
	if err != nil {
		if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
			slog.Warn("Azure circuit breaker open, skipping vault secrets call",
				"vault", akv.Spec.KeyvaultName,
				"namespace", namespace,
				"name", name)
			return nil
		}
		return fmt.Errorf("failed to list vault secrets: %w", err)
	}

	slog.Info("Listed vault secrets",
		"vault", akv.Spec.KeyvaultName,
		"count", len(vaultSecrets))

	// Step 4: Apply tag filtering if filters are specified
	filteredSecrets := azure.FilterSecretsByTags(vaultSecrets, akv.Spec.Filters)

	slog.Info("Filtered secrets",
		"vault", akv.Spec.KeyvaultName,
		"originalCount", len(vaultSecrets),
		"filteredCount", len(filteredSecrets),
		"filters", akv.Spec.Filters)

	// Step 5: Generate SecretProviderClass
	desiredSPC := generateSecretProviderClass(akv, filteredSecrets)

	// Step 6: Create or update the SecretProviderClass
	existingSPC := &secretsstorev1.SecretProviderClass{}
	err = ctrl.ctrlClient.Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, existingSPC)

	if err != nil {
		if kerrors.IsNotFound(err) {
			// SPC doesn't exist, create it
			slog.Info("Creating SecretProviderClass",
				"namespace", namespace,
				"name", name)

			// Use the typed client to create
			_, createErr := ctrl.client.SecretsstoreV1().SecretProviderClasses(namespace).Create(
				ctx,
				desiredSPC,
				metav1.CreateOptions{},
			)
			if createErr != nil {
				return fmt.Errorf("failed to create SecretProviderClass: %w", createErr)
			}

			slog.Info("SecretProviderClass created successfully",
				"namespace", namespace,
				"name", name)
		} else {
			return fmt.Errorf("failed to get SecretProviderClass: %w", err)
		}
	} else {
		// SPC exists, check if update is needed
		objectsChanged := existingSPC.Spec.Parameters["objects"] != desiredSPC.Spec.Parameters["objects"]
		secretObjectsChanged := !compareSecretObjects(existingSPC.Spec.SecretObjects, desiredSPC.Spec.SecretObjects)
		annotationsChanged := !compareAnnotations(existingSPC.Annotations, desiredSPC.Annotations)

		if !objectsChanged && !secretObjectsChanged && !annotationsChanged {
			slog.Info("No changes detected - skipping SPC update",
				"namespace", namespace,
				"name", name)
		} else {
			slog.Info("Changes detected - updating SecretProviderClass",
				"namespace", namespace,
				"name", name,
				"objectsChanged", objectsChanged,
				"secretObjectsChanged", secretObjectsChanged,
				"annotationsChanged", annotationsChanged)

			// Update the spec and annotations
			existingSPC.Spec = desiredSPC.Spec
			existingSPC.Annotations = desiredSPC.Annotations
			existingSPC.OwnerReferences = desiredSPC.OwnerReferences

			_, updateErr := ctrl.client.SecretsstoreV1().SecretProviderClasses(namespace).Update(
				ctx,
				existingSPC,
				metav1.UpdateOptions{},
			)
			if updateErr != nil {
				return fmt.Errorf("failed to update SecretProviderClass: %w", updateErr)
			}

			slog.Info("SecretProviderClass updated successfully",
				"namespace", namespace,
				"name", name)
		}
	}

	// Step 7: Update AzureKeyVaultSync status
	// Count secret objects (secrets with secret-object: "true" tag)
	secretObjectCount := 0
	for _, secret := range filteredSecrets {
		if secret.Tags != nil {
			if tagValue, exists := secret.Tags["secret-object"]; exists && tagValue != nil && *tagValue == "true" {
				secretObjectCount++
			}
		}
	}

	// Update status fields
	akv.Status.SecretCount = len(filteredSecrets)
	akv.Status.SecretObjectCount = secretObjectCount
	akv.Status.GeneratedSPCName = name
	akv.Status.ObservedGeneration = akv.Generation
	now := metav1.Now()
	akv.Status.LastSyncTime = &now

	// Update the status subresource
	if err := ctrl.ctrlClient.Status().Update(ctx, akv); err != nil {
		slog.Warn("Failed to update AzureKeyVaultSync status",
			"namespace", namespace,
			"name", name,
			"error", err)
		// Don't fail the reconciliation on status update failure
	}

	slog.Info("AzureKeyVaultSync reconciliation complete",
		"namespace", namespace,
		"name", name,
		"secretCount", len(filteredSecrets),
		"secretObjectCount", secretObjectCount)

	return nil
}

// watchSecrets watches for Secret creation/updates and applies annotations from SPC metadata
func (ctrl *Controller) watchSecrets(ctx context.Context) {
	slog.Info("Starting Secret watcher for annotation synchronization")

	// Use a ticker for periodic reconciliation of Secrets
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Secret watcher shutting down")
			return
		case <-ticker.C:
			// List all Secrets in the watch namespace
			listOpts := metav1.ListOptions{}

			secrets, err := ctrl.clientset.CoreV1().Secrets(ctrl.watchNamespace).List(ctx, listOpts)
			if err != nil {
				slog.Error("Failed to list Secrets", "error", err)
				continue
			}

			slog.Debug("Found Secrets to check for annotation sync", "count", len(secrets.Items))

			// Process each Secret
			for _, secret := range secrets.Items {
				// Only process Secrets managed by secrets-store.csi.k8s.io
				if secret.Labels == nil || secret.Labels["secrets-store.csi.k8s.io/managed"] != "true" {
					continue
				}

				if err := ctrl.reconcileSecretAnnotations(ctx, &secret); err != nil {
					slog.Error("Failed to reconcile Secret annotations",
						"namespace", secret.Namespace,
						"name", secret.Name,
						"error", err)
				}
			}
		}
	}
}

// reconcileSecretAnnotations applies SPC annotations to a Secret
func (ctrl *Controller) reconcileSecretAnnotations(ctx context.Context, secret *corev1.Secret) error {
	namespace := secret.Namespace
	name := secret.Name

	slog.Debug("Reconciling Secret metadata (annotations and labels)",
		"namespace", namespace,
		"name", name)

	// Find the SPC that manages this Secret
	spcName, err := ctrl.findSPCForSecret(ctx, secret)
	if err != nil {
		return fmt.Errorf("failed to find SPC for Secret: %w", err)
	}
	if spcName == "" {
		slog.Debug("No SPC found for Secret", "namespace", namespace, "name", name)
		return nil
	}

	// Get the SPC
	spc, err := ctrl.client.SecretsstoreV1().SecretProviderClasses(namespace).Get(
		ctx,
		spcName,
		metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get SPC: %w", err)
	}

	// Extract annotations and labels for this Secret from SPC metadata
	desiredAnnotations := azure.ExtractAnnotationsForSecret(spc.Annotations, name)
	desiredLabels := azure.ExtractLabelsForSecret(spc.Annotations, name)

	// Find annotations/labels to add or update
	annotationsToAdd := make(map[string]string)
	for key, desiredValue := range desiredAnnotations {
		currentValue, exists := secret.Annotations[key]
		if !exists || currentValue != desiredValue {
			annotationsToAdd[key] = desiredValue
		}
	}

	labelsToAdd := make(map[string]string)
	for key, desiredValue := range desiredLabels {
		currentValue, exists := secret.Labels[key]
		if !exists || currentValue != desiredValue {
			labelsToAdd[key] = desiredValue
		}
	}

	// Get previously managed metadata from tracking annotations
	previouslyManagedAnnotations := getPreviouslyManagedMetadata(secret.Annotations, azure.ManagedAnnotationsAnnotation)
	previouslyManagedLabels := getPreviouslyManagedMetadata(secret.Annotations, azure.ManagedLabelsAnnotation)

	// Find annotations to remove (were previously managed but no longer desired)
	annotationsToRemove := []string{}
	for _, key := range previouslyManagedAnnotations {
		if _, stillDesired := desiredAnnotations[key]; !stillDesired {
			annotationsToRemove = append(annotationsToRemove, key)
		}
	}

	// Find labels to remove (were previously managed but no longer desired)
	labelsToRemove := []string{}
	for _, key := range previouslyManagedLabels {
		if _, stillDesired := desiredLabels[key]; !stillDesired {
			labelsToRemove = append(labelsToRemove, key)
		}
	}

	// Build new tracking annotation values
	newManagedAnnotations := buildManagedMetadataList(desiredAnnotations)
	newManagedLabels := buildManagedMetadataList(desiredLabels)

	// Add tracking annotations to the annotations we're adding
	if newManagedAnnotations != "" {
		annotationsToAdd[azure.ManagedAnnotationsAnnotation] = newManagedAnnotations
	}
	if newManagedLabels != "" {
		annotationsToAdd[azure.ManagedLabelsAnnotation] = newManagedLabels
	}

	// Check if we need to remove tracking annotations (when no metadata is managed)
	if newManagedAnnotations == "" && secret.Annotations != nil {
		if _, exists := secret.Annotations[azure.ManagedAnnotationsAnnotation]; exists {
			annotationsToRemove = append(annotationsToRemove, azure.ManagedAnnotationsAnnotation)
		}
	}
	if newManagedLabels == "" && secret.Annotations != nil {
		if _, exists := secret.Annotations[azure.ManagedLabelsAnnotation]; exists {
			annotationsToRemove = append(annotationsToRemove, azure.ManagedLabelsAnnotation)
		}
	}

	if len(annotationsToAdd) == 0 && len(labelsToAdd) == 0 && len(annotationsToRemove) == 0 && len(labelsToRemove) == 0 {
		slog.Debug("Secret metadata already up to date", "namespace", namespace, "name", name)
		return nil
	}

	// Apply metadata changes to Secret
	if err := ctrl.patchSecretMetadata(ctx, secret, annotationsToAdd, labelsToAdd, annotationsToRemove, labelsToRemove); err != nil {
		return fmt.Errorf("failed to patch Secret metadata: %w", err)
	}

	slog.Info("Applied metadata to Secret",
		"namespace", namespace,
		"name", name,
		"annotationsAdded", len(annotationsToAdd),
		"labelsAdded", len(labelsToAdd),
		"annotationsRemoved", len(annotationsToRemove),
		"labelsRemoved", len(labelsToRemove))

	return nil
}

// getPreviouslyManagedMetadata extracts the list of previously managed metadata keys from a tracking annotation
func getPreviouslyManagedMetadata(annotations map[string]string, trackingKey string) []string {
	if annotations == nil {
		return []string{}
	}

	value, exists := annotations[trackingKey]
	if !exists || value == "" {
		return []string{}
	}

	// Split comma-separated list
	keys := strings.Split(value, ",")
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// buildManagedMetadataList creates a comma-separated list of metadata keys for tracking
func buildManagedMetadataList(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}

	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}

	// Sort for consistency
	sort.Strings(keys)

	return strings.Join(keys, ",")
}

// findSPCForSecret finds the SecretProviderClass that manages a given Secret
func (ctrl *Controller) findSPCForSecret(ctx context.Context, secret *corev1.Secret) (string, error) {
	// Check for the secretProviderClass label (added by CSI driver)
	if secret.Labels != nil {
		if spcName, exists := secret.Labels["secrets-store.csi.k8s.io/secretProviderClass"]; exists {
			return spcName, nil
		}
	}

	// Use cache index for O(1) lookup
	spcName := ctrl.cache.FindSPCForSecret(secret.Namespace, secret.Name)
	if spcName != "" {
		return spcName, nil
	}

	// Final fallback: Search for SPC with matching secretObjects
	// This should rarely be needed if the cache is properly maintained
	slog.Debug("Cache miss for secret lookup, falling back to API list",
		"namespace", secret.Namespace,
		"secretName", secret.Name)

	spcList, err := ctrl.client.SecretsstoreV1().SecretProviderClasses(secret.Namespace).List(
		ctx,
		metav1.ListOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("failed to list SPCs: %w", err)
	}

	for _, spc := range spcList.Items {
		for _, secretObj := range spc.Spec.SecretObjects {
			if secretObj.SecretName == secret.Name {
				return spc.Name, nil
			}
		}
	}

	return "", nil
}

// patchSecretMetadata applies annotations and labels to a Secret using JSON Patch
func (ctrl *Controller) patchSecretMetadata(ctx context.Context, secret *corev1.Secret, annotationsToAdd map[string]string, labelsToAdd map[string]string, annotationsToRemove []string, labelsToRemove []string) error {
	// Build JSON Patch operations
	var patchOps []map[string]interface{}

	// Remove annotations first (must be done before adds if removing all)
	for _, key := range annotationsToRemove {
		// Escape forward slashes in annotation keys (JSON Pointer RFC 6901)
		escapedKey := strings.ReplaceAll(key, "~", "~0")
		escapedKey = strings.ReplaceAll(escapedKey, "/", "~1")

		patchOps = append(patchOps, map[string]interface{}{
			"op":   "remove",
			"path": "/metadata/annotations/" + escapedKey,
		})
	}

	// Remove labels first (must be done before adds if removing all)
	for _, key := range labelsToRemove {
		// Escape forward slashes in label keys (JSON Pointer RFC 6901)
		escapedKey := strings.ReplaceAll(key, "~", "~0")
		escapedKey = strings.ReplaceAll(escapedKey, "/", "~1")

		patchOps = append(patchOps, map[string]interface{}{
			"op":   "remove",
			"path": "/metadata/labels/" + escapedKey,
		})
	}

	// Ensure annotations map exists if we have annotations to add
	if len(annotationsToAdd) > 0 && secret.Annotations == nil {
		patchOps = append(patchOps, map[string]interface{}{
			"op":    "add",
			"path":  "/metadata/annotations",
			"value": map[string]string{},
		})
	}

	// Add each annotation
	for key, value := range annotationsToAdd {
		// Escape forward slashes in annotation keys (JSON Pointer RFC 6901)
		escapedKey := strings.ReplaceAll(key, "~", "~0")
		escapedKey = strings.ReplaceAll(escapedKey, "/", "~1")

		patchOps = append(patchOps, map[string]interface{}{
			"op":    "add",
			"path":  "/metadata/annotations/" + escapedKey,
			"value": value,
		})
	}

	// Ensure labels map exists if we have labels to add
	if len(labelsToAdd) > 0 && secret.Labels == nil {
		patchOps = append(patchOps, map[string]interface{}{
			"op":    "add",
			"path":  "/metadata/labels",
			"value": map[string]string{},
		})
	}

	// Add each label
	for key, value := range labelsToAdd {
		// Escape forward slashes in label keys (JSON Pointer RFC 6901)
		escapedKey := strings.ReplaceAll(key, "~", "~0")
		escapedKey = strings.ReplaceAll(escapedKey, "/", "~1")

		patchOps = append(patchOps, map[string]interface{}{
			"op":    "add",
			"path":  "/metadata/labels/" + escapedKey,
			"value": value,
		})
	}

	// Marshal patch to JSON
	patchBytes, err := json.Marshal(patchOps)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	// Apply the patch
	_, err = ctrl.clientset.CoreV1().Secrets(secret.Namespace).Patch(
		ctx,
		secret.Name,
		types.JSONPatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to apply patch: %w", err)
	}

	return nil
}

func (ctrl *Controller) Run(ctx context.Context) {
	defer ctrl.queue.ShutDown()

	ctrl.syncCache(ctx)
	ctrl.printCache()

	// Start periodic resync
	go ctrl.startPeriodicResync(ctx)

	// Start Secret watcher for annotation synchronization
	go ctrl.watchSecrets(ctx)

	// Start token cache cleanup routines
	go ctrl.tokenCache.StartCleanup(ctx, 5*time.Minute)
	go ctrl.azureTokenCache.StartCleanup(ctx, 5*time.Minute)

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
