package controller

import (
	"testing"
	"time"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/cache"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/circuitbreaker"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/config"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/health"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/token"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/util/workqueue"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	spcfake "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned/fake"
)

// TestParseKey tests the parseKey helper function
func TestParseKey(t *testing.T) {
	tests := []struct {
		name          string
		key           QueueKey
		wantNamespace string
		wantName      string
		wantErr       bool
	}{
		{
			name:          "valid key",
			key:           "default/my-spc",
			wantNamespace: "default",
			wantName:      "my-spc",
			wantErr:       false,
		},
		{
			name:          "valid key with hyphens",
			key:           "kube-system/csi-secrets-store",
			wantNamespace: "kube-system",
			wantName:      "csi-secrets-store",
			wantErr:       false,
		},
		{
			name:          "key with multiple slashes",
			key:           "default/my/spc",
			wantNamespace: "default",
			wantName:      "my/spc",
			wantErr:       false,
		},
		{
			name:    "invalid key - no slash",
			key:     "defaultmy-spc",
			wantErr: true,
		},
		{
			name:    "invalid key - empty",
			key:     "",
			wantErr: true,
		},
		{
			name:          "key with only slash",
			key:           "/",
			wantNamespace: "",
			wantName:      "",
			wantErr:       false,
		},
		{
			name:          "key starting with slash",
			key:           "/my-spc",
			wantNamespace: "",
			wantName:      "my-spc",
			wantErr:       false,
		},
		{
			name:          "key ending with slash",
			key:           "default/",
			wantNamespace: "default",
			wantName:      "",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace, name, err := parseKey(tt.key)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid key format")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantNamespace, namespace)
				assert.Equal(t, tt.wantName, name)
			}
		})
	}
}

// TestNewController tests the NewController constructor
func TestNewController(t *testing.T) {
	// Create fake clients
	scheme := runtime.NewScheme()
	_ = secretsstorev1.AddToScheme(scheme)
	spcClient := spcfake.NewSimpleClientset()
	k8sClient := fake.NewSimpleClientset()

	// Create config
	cfg := &config.Config{
		AzureCircuitBreakerThreshold: 5,
		AzureCircuitBreakerTimeout:   60 * time.Second,
		SyncInterval:                 5 * time.Minute,
		WorkerCount:                  3,
	}

	// Call NewController
	ctrl := NewController(spcClient, k8sClient, cfg, "test-namespace")

	// Verify controller is properly initialized
	assert.NotNil(t, ctrl)
	assert.Equal(t, spcClient, ctrl.client)
	assert.Equal(t, k8sClient, ctrl.clientset)
	assert.Equal(t, cfg, ctrl.config)
	assert.Equal(t, "test-namespace", ctrl.watchNamespace)

	// Verify caches are initialized
	assert.NotNil(t, ctrl.cache)
	assert.NotNil(t, ctrl.tokenCache)
	assert.NotNil(t, ctrl.azureTokenCache)

	// Verify queue is initialized
	assert.NotNil(t, ctrl.queue)

	// Verify health checker is initialized
	assert.NotNil(t, ctrl.HealthChecker)

	// Verify circuit breaker is initialized
	assert.NotNil(t, ctrl.azureCircuitBreaker)

	// Verify interfaces are initialized
	assert.NotNil(t, ctrl.tokenProvider)
	assert.NotNil(t, ctrl.vaultClient)
	assert.NotNil(t, ctrl.patchClient)

	// Verify the interfaces are the real implementations
	_, ok := ctrl.tokenProvider.(*RealTokenProvider)
	assert.True(t, ok, "tokenProvider should be RealTokenProvider")

	_, ok = ctrl.vaultClient.(*RealVaultClient)
	assert.True(t, ok, "vaultClient should be RealVaultClient")

	_, ok = ctrl.patchClient.(*RealPatchClient)
	assert.True(t, ok, "patchClient should be RealPatchClient")

	// Clean up
	ctrl.queue.ShutDown()
}

// TestNewController_CircuitBreakerConfiguration tests circuit breaker is configured correctly
func TestNewController_CircuitBreakerConfiguration(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = secretsstorev1.AddToScheme(scheme)
	spcClient := spcfake.NewSimpleClientset()
	k8sClient := fake.NewSimpleClientset()

	tests := []struct {
		name      string
		threshold int
		timeout   time.Duration
	}{
		{
			name:      "default configuration",
			threshold: 5,
			timeout:   60 * time.Second,
		},
		{
			name:      "custom threshold",
			threshold: 10,
			timeout:   120 * time.Second,
		},
		{
			name:      "low threshold",
			threshold: 2,
			timeout:   30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				AzureCircuitBreakerThreshold: tt.threshold,
				AzureCircuitBreakerTimeout:   tt.timeout,
			}

			ctrl := NewController(spcClient, k8sClient, cfg, "")
			assert.NotNil(t, ctrl.azureCircuitBreaker)

			// Clean up
			ctrl.queue.ShutDown()
		})
	}
}

// TestNewController_WithEmptyNamespace tests controller with empty watch namespace
func TestNewController_WithEmptyNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = secretsstorev1.AddToScheme(scheme)
	spcClient := spcfake.NewSimpleClientset()
	k8sClient := fake.NewSimpleClientset()

	cfg := &config.Config{
		AzureCircuitBreakerThreshold: 5,
		AzureCircuitBreakerTimeout:   60 * time.Second,
	}

	// Empty namespace means watch all namespaces
	ctrl := NewController(spcClient, k8sClient, cfg, "")
	assert.NotNil(t, ctrl)
	assert.Equal(t, "", ctrl.watchNamespace)

	ctrl.queue.ShutDown()
}

// TestNewController_CachesAreIndependent tests that caches are separate instances
func TestNewController_CachesAreIndependent(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = secretsstorev1.AddToScheme(scheme)
	spcClient := spcfake.NewSimpleClientset()
	k8sClient := fake.NewSimpleClientset()

	cfg := &config.Config{
		AzureCircuitBreakerThreshold: 5,
		AzureCircuitBreakerTimeout:   60 * time.Second,
	}

	// Create two controllers
	ctrl1 := NewController(spcClient, k8sClient, cfg, "ns1")
	ctrl2 := NewController(spcClient, k8sClient, cfg, "ns2")

	// Verify they have independent caches
	assert.NotSame(t, ctrl1.cache, ctrl2.cache)
	assert.NotSame(t, ctrl1.tokenCache, ctrl2.tokenCache)
	assert.NotSame(t, ctrl1.azureTokenCache, ctrl2.azureTokenCache)
	assert.NotSame(t, ctrl1.queue, ctrl2.queue)

	ctrl1.queue.ShutDown()
	ctrl2.queue.ShutDown()
}

// TestKeyFor tests the keyFor helper function (already 100% covered but adding for completeness)
func TestKeyFor(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		spcName   string
		want      QueueKey
	}{
		{
			name:      "standard key",
			namespace: "default",
			spcName:   "my-spc",
			want:      "default/my-spc",
		},
		{
			name:      "with hyphens",
			namespace: "kube-system",
			spcName:   "csi-secrets-store",
			want:      "kube-system/csi-secrets-store",
		},
		{
			name:      "empty namespace",
			namespace: "",
			spcName:   "my-spc",
			want:      "/my-spc",
		},
		{
			name:      "empty name",
			namespace: "default",
			spcName:   "",
			want:      "default/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := keyFor(tt.namespace, tt.spcName)
			assert.Equal(t, tt.want, result)
		})
	}
}

// Helper to create a minimal test controller
func newMinimalController() *Controller {
	cfg := &config.Config{
		AzureCircuitBreakerThreshold: 5,
		AzureCircuitBreakerTimeout:   60 * time.Second,
	}

	// Create fake clients
	scheme := runtime.NewScheme()
	_ = secretsstorev1.AddToScheme(scheme)
	spcClient := spcfake.NewSimpleClientset()
	k8sClient := fake.NewSimpleClientset()

	return &Controller{
		client:              spcClient,
		clientset:           k8sClient,
		cache:               cache.NewCache(),
		tokenCache:          token.NewTokenCache(),
		azureTokenCache:     azure.NewAzureTokenCache(),
		queue:               workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[QueueKey]()),
		HealthChecker:       health.NewHealthChecker(),
		config:              cfg,
		azureCircuitBreaker: circuitbreaker.NewCircuitBreaker(cfg.AzureCircuitBreakerThreshold, cfg.AzureCircuitBreakerTimeout),
		watchNamespace:      "",
	}
}

// TestPrintCache tests the printCache debug function
func TestPrintCache(t *testing.T) {
	t.Run("prints empty cache", func(t *testing.T) {
		// Create controller with empty cache
		ctrl := newMinimalController()
		defer ctrl.queue.ShutDown()

		// printCache should not panic with empty cache
		assert.NotPanics(t, func() {
			ctrl.printCache()
		})
	})

	t.Run("prints cache with objects", func(t *testing.T) {
		// Create controller with some cached objects
		ctrl := newMinimalController()
		defer ctrl.queue.ShutDown()

		// Add test objects to cache using Set method
		obj1 := &secretsstorev1.SecretProviderClass{}
		obj1.Namespace = "default"
		obj1.Name = "test-spc-1"

		obj2 := &secretsstorev1.SecretProviderClass{}
		obj2.Namespace = "kube-system"
		obj2.Name = "test-spc-2"

		ctrl.cache.Set("default", "test-spc-1", obj1)
		ctrl.cache.Set("kube-system", "test-spc-2", obj2)

		// printCache should not panic with objects
		assert.NotPanics(t, func() {
			ctrl.printCache()
		})
	})
}
