package main

import (
	"context"
	"fmt"
	"log"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	resyncInterval = 5 * time.Minute
	retryDelay     = 5 * time.Second

	annotationEnabled        = "azure-keyvault-sync/enabled"
	annotationServiceAccount = "azure-keyvault-sync/service-account"
	annotationSecretObjects  = "azure-keyvault-sync/secret-objects"
	annotationCertObjects    = "azure-keyvault-sync/cert-objects"
	annotationEnabledValue   = "true"
)

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
	enabled := isSyncEnabled(obj)
	serviceAccount, hasServiceAccount := getServiceAccount(obj)

	if enabled {
		if hasServiceAccount {
			log.Printf("Event: ADDED %s/%s (sync enabled, service-account: %s)", namespace, name, serviceAccount)

			// Immediate reconciliation
			err := ctrl.reconcileResource(obj)
			if err != nil {
				log.Printf("Error reconciling %s/%s: %v", namespace, name, err)
				// Still add to cache even if reconciliation fails
			}

			ctrl.cache.Set(namespace, name, obj.DeepCopy())
			ctrl.printCache()
		} else {
			log.Printf("Event: ADDED %s/%s (sync enabled but missing service-account annotation, skipping)", namespace, name)
		}
	} else {
		log.Printf("Event: ADDED %s/%s (sync disabled, skipping)", namespace, name)
	}
}

func (ctrl *Controller) handleModified(obj *unstructured.Unstructured) {
	namespace := obj.GetNamespace()
	name := obj.GetName()
	enabled := isSyncEnabled(obj)
	inCache := ctrl.cache.Has(namespace, name)
	serviceAccount, hasServiceAccount := getServiceAccount(obj)

	if enabled && !inCache {
		if hasServiceAccount {
			log.Printf("Event: MODIFIED %s/%s (annotation enabled, service-account: %s, adding to cache)", namespace, name, serviceAccount)

			// Immediate reconciliation
			err := ctrl.reconcileResource(obj)
			if err != nil {
				log.Printf("Error reconciling %s/%s: %v", namespace, name, err)
			}

			ctrl.cache.Set(namespace, name, obj.DeepCopy())
			ctrl.printCache()
		} else {
			log.Printf("Event: MODIFIED %s/%s (annotation enabled but missing service-account annotation, skipping)", namespace, name)
		}
	} else if !enabled && inCache {
		log.Printf("Event: MODIFIED %s/%s (annotation disabled, removing from cache)", namespace, name)
		ctrl.cache.Delete(namespace, name)
		ctrl.printCache()
	} else if enabled && inCache {
		if hasServiceAccount {
			log.Printf("Event: MODIFIED %s/%s (updating, service-account: %s)", namespace, name, serviceAccount)

			// Immediate reconciliation - THIS FIXES ANNOTATION REMOVAL TEST
			err := ctrl.reconcileResource(obj)
			if err != nil {
				log.Printf("Error reconciling %s/%s: %v", namespace, name, err)
			}

			ctrl.cache.Set(namespace, name, obj.DeepCopy())
		} else {
			log.Printf("Event: MODIFIED %s/%s (missing service-account annotation, removing from cache)", namespace, name)
			ctrl.cache.Delete(namespace, name)
			ctrl.printCache()
		}
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
		log.Printf("Error listing secrets from vault %s for %s/%s: %v",
			keyvaultName, namespace, name, err)
		// Continue with empty secrets slice
		secrets = nil
	} else {
		log.Printf("Found %d secrets in vault %s for %s/%s",
			len(secrets), keyvaultName, namespace, name)
		for _, secret := range secrets {
			log.Printf("  - Secret: %s", secret)
		}
	}

	// List certificates from vault
	certificates, err := ListCertificates(ctrl.ctx, keyvaultName, azureToken, azureTokenExpiration)
	if err != nil {
		log.Printf("Error listing certificates from vault %s for %s/%s: %v",
			keyvaultName, namespace, name, err)
		// Continue with empty certificates slice
		certificates = nil
	} else {
		log.Printf("Found %d certificates in vault %s for %s/%s",
			len(certificates), keyvaultName, namespace, name)
		for _, cert := range certificates {
			log.Printf("  - Certificate: %s", cert)
		}
	}

	// Update SecretProviderClass with discovered objects
	log.Printf("Updating SecretProviderClass %s/%s with discovered objects", namespace, name)

	// Use empty slices if errors occurred
	discoveredSecrets := secrets
	discoveredCerts := certificates
	if secrets == nil {
		discoveredSecrets = []string{}
	}
	if certificates == nil {
		discoveredCerts = []string{}
	}

	// Generate objects from vault (vault is source of truth)
	discoveredObjects := GenerateObjectsFromVault(discoveredSecrets, discoveredCerts)

	// Format as YAML
	newObjects, err := FormatObjectsYAML(discoveredObjects)
	if err != nil {
		return fmt.Errorf("error formatting objects: %w", err)
	}

	// Process secretObjects
	var secretObjectsToSync interface{}
	annotations := obj.GetAnnotations()
	enableSecretObjects := annotations != nil && annotations[annotationSecretObjects] == annotationEnabledValue
	enableCertObjects := annotations != nil && annotations[annotationCertObjects] == annotationEnabledValue

	if enableSecretObjects || enableCertObjects {
		log.Printf("Processing secretObjects for %s/%s (secrets: %v, certs: %v)",
			namespace, name, enableSecretObjects, enableCertObjects)

		// Generate secretObjects from vault + annotations
		generatedSecretObjects := GenerateSecretObjectsFromVault(
			discoveredSecrets,
			discoveredCerts,
			enableSecretObjects,
			enableCertObjects,
		)

		secretObjectsToSync = generatedSecretObjects
	} else {
		// Check if field exists and needs removal
		existingSecretObjects, found, _ := unstructured.NestedSlice(obj.Object, "spec", "secretObjects")
		if found && len(existingSecretObjects) > 0 {
			secretObjectsToSync = "REMOVE_FIELD"
			log.Printf("Annotation disabled for %s/%s, will clear secretObjects field", namespace, name)
		}
	}

	// Check if update needed
	currentObjects, _, _ := unstructured.NestedString(obj.Object, "spec", "parameters", "objects")
	objectsChanged := DetectChanges(currentObjects, newObjects)

	// Check if secretObjects changed
	secretObjectsChanged := secretObjectsToSync != nil

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
		namespace, name, len(discoveredObjects), len(discoveredSecrets), len(discoveredCerts))

	return nil
}

func (ctrl *Controller) syncCache() {
	log.Println("Performing full resync")
	result, err := ctrl.client.Resource(ctrl.gvr).Namespace("").List(ctrl.ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("Error listing SecretProviderClasses: %v", err)
		return
	}

	enabledCount := 0
	validCount := 0
	for _, item := range result.Items {
		if isSyncEnabled(&item) {
			enabledCount++
		}
		if valid, _ := isValidForSync(&item); valid {
			// Reconcile this resource
			err := ctrl.reconcileResource(&item)
			if err != nil {
				log.Printf("Error reconciling %s/%s: %v", item.GetNamespace(), item.GetName(), err)
				continue
			}

			ctrl.cache.Set(item.GetNamespace(), item.GetName(), item.DeepCopy())
			validCount++
		} else if isSyncEnabled(&item) {
			log.Printf("Warning: %s/%s has sync enabled but missing service-account annotation", item.GetNamespace(), item.GetName())
		}
	}

	log.Printf("Resync complete: %d objects in cache (%d total, %d enabled, %d valid)", validCount, len(result.Items), enabledCount, validCount)
	ctrl.printCache()
}

func (ctrl *Controller) startPeriodicResync() {
	ticker := time.NewTicker(resyncInterval)
	defer ticker.Stop()
	for range ticker.C {
		ctrl.syncCache()
	}
}

func (ctrl *Controller) Run() {
	ctrl.syncCache()
	ctrl.printCache()

	go ctrl.startPeriodicResync()

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
