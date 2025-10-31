package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/cache"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/circuitbreaker"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/config"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/health"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	spcfake "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned/fake"
)

// TestReconcileResource_MissingServiceAccount tests error handling when service account annotation is missing
func TestReconcileResource_MissingServiceAccount(t *testing.T) {
	// Create test SPC without service account annotation
	spc := &secretsstorev1.SecretProviderClass{}
	spc.Name = "test-spc"
	spc.Namespace = "default"
	spc.Spec.Parameters = map[string]string{
		"clientID":     "test-client-id",
		"tenantId":     "test-tenant-id",
		"keyvaultName": "test-vault",
	}

	// Create controller with mocks
	ctrl := createTestController()

	// Call reconcileResource
	err := ctrl.reconcileResource(context.Background(), spc)

	// Verify error
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing service-account annotation")
}

// TestReconcileResource_MissingClientID tests error handling when clientID parameter is missing
func TestReconcileResource_MissingClientID(t *testing.T) {
	spc := &secretsstorev1.SecretProviderClass{}
	spc.Name = "test-spc"
	spc.Namespace = "default"
	spc.Annotations = map[string]string{
		annotationServiceAccount: "test-sa",
	}
	spc.Spec.Parameters = map[string]string{
		"tenantId":     "test-tenant-id",
		"keyvaultName": "test-vault",
	}

	ctrl := createTestController()
	err := ctrl.reconcileResource(context.Background(), spc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing clientID")
}

// TestReconcileResource_MissingTenantID tests error handling when tenantId parameter is missing
func TestReconcileResource_MissingTenantID(t *testing.T) {
	spc := &secretsstorev1.SecretProviderClass{}
	spc.Name = "test-spc"
	spc.Namespace = "default"
	spc.Annotations = map[string]string{
		annotationServiceAccount: "test-sa",
	}
	spc.Spec.Parameters = map[string]string{
		"clientID":     "test-client-id",
		"keyvaultName": "test-vault",
	}

	ctrl := createTestController()
	err := ctrl.reconcileResource(context.Background(), spc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing tenantId")
}

// TestReconcileResource_MissingKeyvaultName tests error handling when keyvaultName parameter is missing
func TestReconcileResource_MissingKeyvaultName(t *testing.T) {
	spc := &secretsstorev1.SecretProviderClass{}
	spc.Name = "test-spc"
	spc.Namespace = "default"
	spc.Annotations = map[string]string{
		annotationServiceAccount: "test-sa",
	}
	spc.Spec.Parameters = map[string]string{
		"clientID": "test-client-id",
		"tenantId": "test-tenant-id",
	}

	ctrl := createTestController()
	err := ctrl.reconcileResource(context.Background(), spc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing keyvaultName")
}

// TestReconcileResource_K8sTokenError tests error handling when K8s token retrieval fails
func TestReconcileResource_K8sTokenError(t *testing.T) {
	spc := createValidSPC()

	ctrl := createTestController()
	// Mock K8s token error
	ctrl.tokenProvider = &MockTokenProvider{
		GetK8sTokenFunc: func(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error) {
			return "", errors.New("token retrieval failed")
		},
	}

	err := ctrl.reconcileResource(context.Background(), spc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error getting token")
}

// TestReconcileResource_AzureTokenError tests error handling when Azure token exchange fails
func TestReconcileResource_AzureTokenError(t *testing.T) {
	spc := createValidSPC()

	ctrl := createTestController()
	// Mock Azure token error
	ctrl.tokenProvider = &MockTokenProvider{
		GetK8sTokenFunc: func(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error) {
			return "test-k8s-token", nil
		},
		GetAzureTokenFunc: func(ctx context.Context, namespace, serviceAccount, k8sToken, clientID, tenantID string) (string, time.Time, error) {
			return "", time.Time{}, errors.New("azure token exchange failed")
		},
	}

	err := ctrl.reconcileResource(context.Background(), spc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error getting Azure AD token")
}

// TestReconcileResource_ListSecretsError tests error handling when ListSecrets fails
func TestReconcileResource_ListSecretsError(t *testing.T) {
	spc := createValidSPC()

	ctrl := createTestController()
	// Mock vault client to return error on ListSecrets
	ctrl.vaultClient = &MockVaultClient{
		ListSecretsFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
			return nil, errors.New("failed to list secrets")
		},
	}

	err := ctrl.reconcileResource(context.Background(), spc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list secrets from vault")
}

// TestReconcileResource_ListCertificatesError tests error handling when ListCertificates fails
func TestReconcileResource_ListCertificatesError(t *testing.T) {
	spc := createValidSPC()

	ctrl := createTestController()
	// Mock vault client to return error on ListCertificates
	ctrl.vaultClient = &MockVaultClient{
		ListSecretsFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
			return []azure.VaultSecret{
				{Name: "secret1", Tags: map[string]*string{}},
			}, nil
		},
		ListCertificatesFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
			return nil, errors.New("failed to list certificates")
		},
	}

	err := ctrl.reconcileResource(context.Background(), spc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list certificates from vault")
}

// TestReconcileResource_SuccessfulReconciliation tests successful reconciliation flow
func TestReconcileResource_SuccessfulReconciliation(t *testing.T) {
	spc := createValidSPC()
	spc.Spec.Parameters["objects"] = "" // Empty to force change detection

	ctrl := createTestController()

	// Track what was patched
	var patchedNamespace, patchedName, patchedObjects string

	ctrl.patchClient = &MockPatchClient{
		PatchSecretProviderClassFunc: func(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, timestamp string) error {
			patchedNamespace = namespace
			patchedName = name
			patchedObjects = objectsYAML
			return nil
		},
	}

	err := ctrl.reconcileResource(context.Background(), spc)

	assert.NoError(t, err)
	assert.Equal(t, "default", patchedNamespace)
	assert.Equal(t, "test-spc", patchedName)
	assert.NotEmpty(t, patchedObjects)
	// Default mocks don't set secret-object tags, so secretObjects should be nil or empty
}

// TestReconcileResource_NoChangesDetected tests that no patch occurs when objects haven't changed
func TestReconcileResource_NoChangesDetected(t *testing.T) {
	spc := createValidSPC()

	ctrl := createTestController()

	// Capture the generated YAML so we can match it exactly
	var generatedYAML string
	ctrl.patchClient = &MockPatchClient{
		PatchSecretProviderClassFunc: func(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, timestamp string) error {
			generatedYAML = objectsYAML
			return nil
		},
	}

	// First run - capture what gets generated
	spc.Spec.Parameters["objects"] = "" // Force change
	err := ctrl.reconcileResource(context.Background(), spc)
	assert.NoError(t, err)
	assert.NotEmpty(t, generatedYAML)

	// Second run - use the captured YAML to test no-change detection
	spc.Spec.Parameters["objects"] = generatedYAML
	patchCalled := false
	ctrl.patchClient = &MockPatchClient{
		PatchSecretProviderClassFunc: func(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, timestamp string) error {
			patchCalled = true
			return nil
		},
	}

	err = ctrl.reconcileResource(context.Background(), spc)
	assert.NoError(t, err)
	assert.False(t, patchCalled, "PatchClient should not be called when no changes detected")
}

// TestReconcileResource_TagFiltering tests tag filtering functionality
func TestReconcileResource_TagFiltering(t *testing.T) {
	spc := createValidSPC()
	spc.Spec.Parameters["objects"] = "" // Force change

	// Enable tag filtering
	spc.Annotations[annotationRespectTags] = "true"
	spc.Labels = map[string]string{
		labelService:     "api",
		labelEnvironment: "production",
	}

	ctrl := createTestController()

	// Mock vault with tagged secrets
	ctrl.vaultClient = &MockVaultClient{
		ListSecretsFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
			return []azure.VaultSecret{
				{Name: "secret-api-prod", Tags: map[string]*string{"service": ptr("api"), "environment": ptr("production")}},
				{Name: "secret-api-dev", Tags: map[string]*string{"service": ptr("api"), "environment": ptr("development")}},
				{Name: "secret-other", Tags: map[string]*string{"service": ptr("other"), "environment": ptr("production")}},
			}, nil
		},
		ListCertificatesFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
			return []azure.VaultCertificate{
				{Name: "cert-api-prod", Tags: map[string]*string{"service": ptr("api"), "environment": ptr("production")}},
			}, nil
		},
	}

	var patchedObjects string
	ctrl.patchClient = &MockPatchClient{
		PatchSecretProviderClassFunc: func(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, timestamp string) error {
			patchedObjects = objectsYAML
			return nil
		},
	}

	err := ctrl.reconcileResource(context.Background(), spc)

	assert.NoError(t, err)
	// Verify only matching secrets/certs are included
	assert.Contains(t, patchedObjects, "secret-api-prod")
	assert.Contains(t, patchedObjects, "cert-api-prod")
	assert.NotContains(t, patchedObjects, "secret-api-dev")
	assert.NotContains(t, patchedObjects, "secret-other")
}

// TestReconcileResource_SecretObjectGeneration tests that secret-object and cert-object tags are respected
func TestReconcileResource_SecretObjectGeneration(t *testing.T) {
	spc := createValidSPC()
	spc.Spec.Parameters["objects"] = "" // Force change

	ctrl := createTestController()

	// Mock vault with secret-object tags
	ctrl.vaultClient = &MockVaultClient{
		ListSecretsFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
			return []azure.VaultSecret{
				{Name: "secret-with-k8s", Tags: map[string]*string{"secret-object": ptr("true")}},
				{Name: "secret-without-k8s", Tags: map[string]*string{}},
			}, nil
		},
		ListCertificatesFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
			return []azure.VaultCertificate{
				{Name: "cert-with-k8s", Tags: map[string]*string{"cert-object": ptr("true")}},
			}, nil
		},
	}

	var patchedSecretObjects interface{}
	ctrl.patchClient = &MockPatchClient{
		PatchSecretProviderClassFunc: func(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, timestamp string) error {
			patchedSecretObjects = secretObjects
			return nil
		},
	}

	err := ctrl.reconcileResource(context.Background(), spc)

	assert.NoError(t, err)
	assert.NotNil(t, patchedSecretObjects)
	// secretObjects should be generated for items with secret-object or cert-object tags
}

// Helper functions

func createTestController() *Controller {
	cfg := &config.Config{
		AzureCircuitBreakerThreshold: 5,
		AzureCircuitBreakerTimeout:   60 * time.Second,
	}

	// Create fake SPC client
	scheme := runtime.NewScheme()
	_ = secretsstorev1.AddToScheme(scheme)
	spcClient := spcfake.NewSimpleClientset()

	return &Controller{
		client:              spcClient,
		clientset:           nil, // Not needed for these tests
		cache:               cache.NewCache(),
		HealthChecker:       health.NewHealthChecker(),
		config:              cfg,
		watchNamespace:      "",
		azureCircuitBreaker: circuitbreaker.NewCircuitBreaker(cfg.AzureCircuitBreakerThreshold, cfg.AzureCircuitBreakerTimeout),
		tokenProvider:       &MockTokenProvider{},
		vaultClient:         &MockVaultClient{},
		patchClient:         &MockPatchClient{},
	}
}

func createValidSPC() *secretsstorev1.SecretProviderClass {
	spc := &secretsstorev1.SecretProviderClass{}
	spc.Name = "test-spc"
	spc.Namespace = "default"
	spc.Annotations = map[string]string{
		annotationServiceAccount: "test-sa",
	}
	spc.Spec.Parameters = map[string]string{
		"clientID":     "test-client-id",
		"tenantId":     "test-tenant-id",
		"keyvaultName": "test-vault",
		"objects":      "existing-objects", // Will trigger change detection
	}
	return spc
}
