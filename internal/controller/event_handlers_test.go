package controller

import (
	"testing"
	"time"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/cache"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/circuitbreaker"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/config"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/health"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/testutil"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/token"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/util/workqueue"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

// Helper function to create a test controller with mocked dependencies
func newTestController(t *testing.T) (*Controller, *testutil.K8sTestEnvironment) {
	t.Helper()

	env := testutil.NewK8sTestEnvironment()
	cfg := &config.Config{
		AzureCircuitBreakerThreshold: 5,
		AzureCircuitBreakerTimeout:   1 * time.Minute,
	}

	ctrl := &Controller{
		client:              env.SPCClient,
		clientset:           env.KubeClient,
		cache:               cache.NewCache(),
		tokenCache:          token.NewTokenCache(),
		azureTokenCache:     azure.NewAzureTokenCache(),
		queue:               workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[QueueKey]()),
		HealthChecker:       health.NewHealthChecker(),
		config:              cfg,
		watchNamespace:      "",
		azureCircuitBreaker: circuitbreaker.NewCircuitBreaker(cfg.AzureCircuitBreakerThreshold, cfg.AzureCircuitBreakerTimeout),
	}

	return ctrl, env
}

// Helper to drain the queue and return keys
func drainQueue(queue workqueue.TypedRateLimitingInterface[QueueKey]) []QueueKey {
	var keys []QueueKey
	for queue.Len() > 0 {
		key, shutdown := queue.Get()
		if shutdown {
			break
		}
		keys = append(keys, key)
		queue.Done(key)
	}
	return keys
}

// TestHandleAdded tests the handleAdded event handler
func TestHandleAdded(t *testing.T) {
	tests := []struct {
		name           string
		spc            *secretsstorev1.SecretProviderClass
		expectEnqueued bool
	}{
		{
			name: "valid SPC with service-account annotation",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				Build(),
			expectEnqueued: true,
		},
		{
			name: "SPC without service-account annotation",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				Build(),
			expectEnqueued: false,
		},
		{
			name: "SPC with empty service-account annotation",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("").
				Build(),
			expectEnqueued: true, // Empty string is still valid (annotation exists)
		},
		{
			name: "SPC with tag filtering enabled",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithRespectTags(true).
				WithLabel("service", "app1").
				Build(),
			expectEnqueued: true,
		},
		{
			name: "SPC in different namespace",
			spc: testutil.NewSecretProviderClass("kube-system", "test-spc").
				WithServiceAccount("test-sa").
				Build(),
			expectEnqueued: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl, _ := newTestController(t)
			defer ctrl.queue.ShutDown()

			// Call handleAdded
			ctrl.handleAdded(tt.spc)

			// Check if item was enqueued
			keys := drainQueue(ctrl.queue)

			if tt.expectEnqueued {
				assert.Len(t, keys, 1, "expected item to be enqueued")
				if len(keys) > 0 {
					expectedKey := keyFor(tt.spc.Namespace, tt.spc.Name)
					assert.Equal(t, expectedKey, keys[0], "enqueued key should match SPC namespace/name")
				}
			} else {
				assert.Len(t, keys, 0, "expected no items to be enqueued")
			}
		})
	}
}

// TestHandleModified tests the handleModified event handler
func TestHandleModified(t *testing.T) {
	tests := []struct {
		name           string
		spc            *secretsstorev1.SecretProviderClass
		inCache        bool
		expectEnqueued bool
		expectInCache  bool // After handling
	}{
		{
			name: "valid SPC with service-account annotation",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				Build(),
			inCache:        false,
			expectEnqueued: true,
			expectInCache:  false, // Not in cache yet (will be after reconciliation)
		},
		{
			name: "SPC annotation changed - added service-account",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				Build(),
			inCache:        false,
			expectEnqueued: true,
			expectInCache:  false,
		},
		{
			name: "SPC annotation changed - removed service-account",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				Build(),
			inCache:        true,
			expectEnqueued: false,
			expectInCache:  false, // Should be removed from cache
		},
		{
			name: "SPC modified but already in cache with valid annotation",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				Build(),
			inCache:        true,
			expectEnqueued: true,
			expectInCache:  true, // Stays in cache
		},
		{
			name: "SPC modified without annotation and not in cache",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				Build(),
			inCache:        false,
			expectEnqueued: false,
			expectInCache:  false,
		},
		{
			name: "SPC spec changed (objects array)",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithObjects("array:\n  - objectName: secret1\n").
				Build(),
			inCache:        true,
			expectEnqueued: true,
			expectInCache:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl, _ := newTestController(t)
			defer ctrl.queue.ShutDown()

			// Pre-populate cache if needed
			if tt.inCache {
				ctrl.cache.Set(tt.spc.Namespace, tt.spc.Name, tt.spc)
			}

			// Call handleModified
			ctrl.handleModified(tt.spc)

			// Check if item was enqueued
			keys := drainQueue(ctrl.queue)

			if tt.expectEnqueued {
				assert.Len(t, keys, 1, "expected item to be enqueued")
				if len(keys) > 0 {
					expectedKey := keyFor(tt.spc.Namespace, tt.spc.Name)
					assert.Equal(t, expectedKey, keys[0], "enqueued key should match SPC namespace/name")
				}
			} else {
				assert.Len(t, keys, 0, "expected no items to be enqueued")
			}

			// Check cache state
			inCache := ctrl.cache.Has(tt.spc.Namespace, tt.spc.Name)
			assert.Equal(t, tt.expectInCache, inCache, "cache state mismatch")
		})
	}
}

// TestHandleDeleted tests the handleDeleted event handler
func TestHandleDeleted(t *testing.T) {
	tests := []struct {
		name          string
		namespace     string
		spcName       string
		inCache       bool
		expectInCache bool // After handling
	}{
		{
			name:          "delete SPC that is in cache",
			namespace:     "default",
			spcName:       "test-spc",
			inCache:       true,
			expectInCache: false,
		},
		{
			name:          "delete SPC that is not in cache",
			namespace:     "default",
			spcName:       "test-spc",
			inCache:       false,
			expectInCache: false,
		},
		{
			name:          "delete SPC in different namespace",
			namespace:     "kube-system",
			spcName:       "test-spc",
			inCache:       true,
			expectInCache: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl, _ := newTestController(t)
			defer ctrl.queue.ShutDown()

			// Pre-populate cache if needed
			if tt.inCache {
				spc := testutil.NewSecretProviderClass(tt.namespace, tt.spcName).
					WithServiceAccount("test-sa").
					Build()
				ctrl.cache.Set(tt.namespace, tt.spcName, spc)
			}

			// Verify initial cache state
			assert.Equal(t, tt.inCache, ctrl.cache.Has(tt.namespace, tt.spcName), "initial cache state")

			// Call handleDeleted
			ctrl.handleDeleted(tt.namespace, tt.spcName, tt.inCache)

			// Check cache state after deletion
			inCache := ctrl.cache.Has(tt.namespace, tt.spcName)
			assert.Equal(t, tt.expectInCache, inCache, "cache state after deletion")

			// handleDeleted should not enqueue anything
			keys := drainQueue(ctrl.queue)
			assert.Len(t, keys, 0, "handleDeleted should not enqueue items")
		})
	}
}

// TestHandleAddedConcurrency tests handleAdded with concurrent calls
func TestHandleAddedConcurrency(t *testing.T) {
	ctrl, _ := newTestController(t)
	defer ctrl.queue.ShutDown()

	// Create multiple SPCs
	spcs := make([]*secretsstorev1.SecretProviderClass, 10)
	for i := 0; i < 10; i++ {
		name := "test-spc-" + string(rune('a'+i))
		spcs[i] = testutil.NewSecretProviderClass("default", name).
			WithServiceAccount("test-sa").
			Build()
	}

	// Call handleAdded concurrently
	done := make(chan bool)
	for _, spc := range spcs {
		go func(s *secretsstorev1.SecretProviderClass) {
			ctrl.handleAdded(s)
			done <- true
		}(spc)
	}

	// Wait for all goroutines
	for i := 0; i < len(spcs); i++ {
		<-done
	}

	// All SPCs should be enqueued
	keys := drainQueue(ctrl.queue)
	assert.Len(t, keys, len(spcs), "all SPCs should be enqueued")
}

// TestHandleModifiedCacheBehavior tests cache behavior during modifications
func TestHandleModifiedCacheBehavior(t *testing.T) {
	ctrl, _ := newTestController(t)
	defer ctrl.queue.ShutDown()

	spc := testutil.NewSecretProviderClass("default", "test-spc").
		WithServiceAccount("test-sa").
		Build()

	// Initial add - not in cache
	assert.False(t, ctrl.cache.Has("default", "test-spc"))

	// Add to cache manually (simulate previous reconciliation)
	ctrl.cache.Set("default", "test-spc", spc)
	assert.True(t, ctrl.cache.Has("default", "test-spc"))

	// Modify with valid annotation - should stay in cache
	ctrl.handleModified(spc)
	assert.True(t, ctrl.cache.Has("default", "test-spc"))

	// Remove service-account annotation
	spcWithoutSA := testutil.NewSecretProviderClass("default", "test-spc").Build()
	ctrl.handleModified(spcWithoutSA)

	// Should be removed from cache
	assert.False(t, ctrl.cache.Has("default", "test-spc"))
}
