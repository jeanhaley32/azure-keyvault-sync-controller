package controller

import (
	"context"
	"testing"
	"time"

	akvv1alpha1 "github.com/jeanhaley32/azure-keyvault-sync-controller/api/v1alpha1"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/cache"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/circuitbreaker"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/config"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/health"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	spcfake "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned/fake"
)

// TestAzureKeyVaultSyncReconciler_BasicReconciliation tests that the reconciler can be created and called
func TestAzureKeyVaultSyncReconciler_BasicReconciliation(t *testing.T) {
	// Setup scheme with CRD types
	scheme := runtime.NewScheme()
	_ = akvv1alpha1.AddToScheme(scheme)
	_ = secretsstorev1.AddToScheme(scheme)

	// Create test AzureKeyVaultSync resource
	akv := &akvv1alpha1.AzureKeyVaultSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-akv",
			Namespace: "default",
			UID:       "test-uid-123",
		},
		Spec: akvv1alpha1.AzureKeyVaultSyncSpec{
			KeyvaultName:   "test-vault",
			TenantID:       "tenant-123",
			ClientID:       "client-123",
			ServiceAccount: "test-sa",
			DeletePolicy:   akvv1alpha1.DeletePolicyCascade,
		},
	}

	// Create fake controller-runtime client with the resource
	fakeClient := fakeclient.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(akv).
		Build()

	// Create fake SPC client
	spcClient := spcfake.NewSimpleClientset()

	// Create controller with mocks
	cfg := &config.Config{
		SyncInterval:                 30 * time.Second,
		AzureCircuitBreakerThreshold: 5,
		AzureCircuitBreakerTimeout:   60 * time.Second,
	}

	ctrl := &Controller{
		client:              spcClient,
		clientset:           fake.NewSimpleClientset(),
		ctrlClient:          fakeClient,
		cache:               cache.NewCache(),
		HealthChecker:       health.NewHealthChecker(),
		config:              cfg,
		watchNamespace:      "",
		azureCircuitBreaker: circuitbreaker.NewCircuitBreaker(cfg.AzureCircuitBreakerThreshold, cfg.AzureCircuitBreakerTimeout),
		tokenProvider:       &MockTokenProvider{},
		vaultClient: &MockVaultClient{
			ListSecretsFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
				return []azure.VaultSecret{
					{Name: "test-secret", Tags: map[string]*string{"sync": ptr("true")}},
				}, nil
			},
			ListCertificatesFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
				return []azure.VaultCertificate{}, nil
			},
		},
		patchClient: &MockPatchClient{},
	}

	// Create reconciler
	reconciler := &AzureKeyVaultSyncReconciler{
		Client:     fakeClient,
		Controller: ctrl,
	}

	// Test reconciliation
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-akv",
			Namespace: "default",
		},
	}

	result, err := reconciler.Reconcile(context.Background(), req)

	// Verify reconciliation succeeded
	assert.NoError(t, err)
	assert.Equal(t, cfg.SyncInterval, result.RequeueAfter)

	// Verify SPC was created
	spcList, err := spcClient.SecretsstoreV1().SecretProviderClasses("default").List(context.Background(), metav1.ListOptions{})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(spcList.Items), "SecretProviderClass should be created")
	assert.Equal(t, "test-akv", spcList.Items[0].Name, "SPC name should match AKV name")
}

// TestAzureKeyVaultSyncReconciler_ResourceNotFound tests deletion handling
func TestAzureKeyVaultSyncReconciler_ResourceNotFound(t *testing.T) {
	// Setup scheme
	scheme := runtime.NewScheme()
	_ = akvv1alpha1.AddToScheme(scheme)

	// Create fake client WITHOUT the resource (simulating deletion)
	fakeClient := fakeclient.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Create minimal controller
	ctrl := &Controller{
		client:     spcfake.NewSimpleClientset(),
		ctrlClient: fakeClient,
	}

	// Create reconciler
	reconciler := &AzureKeyVaultSyncReconciler{
		Client:     fakeClient,
		Controller: ctrl,
	}

	// Test reconciliation of non-existent resource
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "non-existent",
			Namespace: "default",
		},
	}

	result, err := reconciler.Reconcile(context.Background(), req)

	// Should succeed without error (resource deleted, nothing to do)
	assert.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result, "Should return empty result for deleted resource")
}

// TestAzureKeyVaultSyncReconciler_RequeueAfter tests that reconciler returns correct requeue duration
func TestAzureKeyVaultSyncReconciler_RequeueAfter(t *testing.T) {
	// Setup scheme
	scheme := runtime.NewScheme()
	_ = akvv1alpha1.AddToScheme(scheme)
	_ = secretsstorev1.AddToScheme(scheme)

	akv := &akvv1alpha1.AzureKeyVaultSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-akv",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: akvv1alpha1.AzureKeyVaultSyncSpec{
			KeyvaultName:   "test-vault",
			TenantID:       "tenant-123",
			ClientID:       "client-123",
			ServiceAccount: "test-sa",
			DeletePolicy:   akvv1alpha1.DeletePolicyCascade,
		},
	}

	fakeClient := fakeclient.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(akv).
		Build()

	syncInterval := 42 * time.Second
	cfg := &config.Config{
		SyncInterval:                 syncInterval,
		AzureCircuitBreakerThreshold: 5,
		AzureCircuitBreakerTimeout:   60 * time.Second,
	}

	ctrl := &Controller{
		client:              spcfake.NewSimpleClientset(),
		clientset:           fake.NewSimpleClientset(),
		ctrlClient:          fakeClient,
		cache:               cache.NewCache(),
		HealthChecker:       health.NewHealthChecker(),
		config:              cfg,
		watchNamespace:      "",
		azureCircuitBreaker: circuitbreaker.NewCircuitBreaker(cfg.AzureCircuitBreakerThreshold, cfg.AzureCircuitBreakerTimeout),
		tokenProvider:       &MockTokenProvider{},
		vaultClient: &MockVaultClient{
			ListSecretsFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
				return []azure.VaultSecret{{Name: "test", Tags: map[string]*string{"sync": ptr("true")}}}, nil
			},
			ListCertificatesFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
				return []azure.VaultCertificate{}, nil
			},
		},
		patchClient: &MockPatchClient{},
	}

	reconciler := &AzureKeyVaultSyncReconciler{
		Client:     fakeClient,
		Controller: ctrl,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-akv",
			Namespace: "default",
		},
	}

	result, err := reconciler.Reconcile(context.Background(), req)

	assert.NoError(t, err)
	assert.Equal(t, syncInterval, result.RequeueAfter, "Should requeue after configured sync interval")
}
