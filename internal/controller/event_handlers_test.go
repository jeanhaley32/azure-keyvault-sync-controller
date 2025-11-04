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

	return &Controller{
		client:              env.SPCClient,
		clientset:           env.KubeClient,
		ctrlClient:          env.CtrlClient,
		cache:               cache.NewCache(),
		tokenCache:          token.NewTokenCache(),
		azureTokenCache:     azure.NewAzureTokenCache(),
		queue:               workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[QueueKey]()),
		HealthChecker:       health.NewHealthChecker(),
		config:              cfg,
		watchNamespace:      "",
		azureCircuitBreaker: circuitbreaker.NewCircuitBreaker(cfg.AzureCircuitBreakerThreshold, cfg.AzureCircuitBreakerTimeout),
	}, env
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

			// Create SPC object for testing
			spc := testutil.NewSecretProviderClass(tt.namespace, tt.spcName).
				WithServiceAccount("test-sa").
				Build()

			// Pre-populate cache if needed
			if tt.inCache {
				ctrl.cache.Set(tt.namespace, tt.spcName, spc)
			}

			// Verify initial cache state
			assert.Equal(t, tt.inCache, ctrl.cache.Has(tt.namespace, tt.spcName), "initial cache state")

			// Call handleDeleted
			ctrl.handleDeleted( spc, tt.inCache)

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

// TestHandleOwnedSPCDeletion tests the handleOwnedSPCDeletion helper function
func TestHandleOwnedSPCDeletion(t *testing.T) {
	tests := []struct {
		name              string
		spc               *secretsstorev1.SecretProviderClass
		setupMockCRDState func(*testutil.K8sTestEnvironment)
		expectNoAction    bool // If true, expects no reconciliation attempt
	}{
		{
			name: "SPC with no owner references",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				Build(),
			expectNoAction: true,
		},
		{
			name: "SPC with wrong API version",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1beta1", "AzureKeyVaultSync", "test-akv").
				Build(),
			expectNoAction: true,
		},
		{
			name: "SPC with wrong kind",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "WrongKind", "test-akv").
				Build(),
			expectNoAction: true,
		},
		{
			name: "SPC with owner but controller=false",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "test-akv").
				WithControllerFalse().
				Build(),
			expectNoAction: true,
		},
		{
			name: "SPC with owner but controller=nil",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "test-akv").
				WithControllerNil().
				Build(),
			expectNoAction: true,
		},
		{
			name: "SPC owned by AzureKeyVaultSync - CRD not found",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "missing-akv").
				Build(),
			setupMockCRDState: func(env *testutil.K8sTestEnvironment) {
				// Don't create the CRD - it should not be found
			},
			expectNoAction: false, // Will attempt but CRD not found
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl, env := newTestController(t)
			defer ctrl.queue.ShutDown()

			// Setup mock CRD state if needed
			if tt.setupMockCRDState != nil {
				tt.setupMockCRDState(env)
			}

			// Call handleOwnedSPCDeletion - should not panic
			ctrl.handleOwnedSPCDeletion(tt.spc)

			// Verify behavior based on expectation
			if tt.expectNoAction {
				// For cases where no action should be taken (wrong owner ref, nil controller, etc),
				// we verify the function completes without panicking. The function should return
				// early from the loop without attempting reconciliation.
				// Verify nothing was enqueued
			keys := drainQueue(ctrl.queue)
			assert.Len(t, keys, 0, "expected no items enqueued for invalid owner refs")
			}

			// Note: Full reconciliation verification would require mocking the reconcile call.
			// The key behavior being tested is that owner-check logic correctly identifies
			// owned SPCs and handles all edge cases without crashing.
		})
	}
}

// TestHandleDeletedNilSPC tests handleDeleted with nil SPC object
func TestHandleDeletedNilSPC(t *testing.T) {
	ctrl, _ := newTestController(t)
	defer ctrl.queue.ShutDown()

	// Call handleDeleted with nil - should not panic
	ctrl.handleDeleted(nil, false)
	ctrl.handleDeleted(nil, true)

	// No assertions needed - test passes if no panic occurs
}

// TestHandleDeletedWithOwnerReference tests handleDeleted cache behavior with owned SPCs
func TestHandleDeletedWithOwnerReference(t *testing.T) {
	ctrl, _ := newTestController(t)
	defer ctrl.queue.ShutDown()

	// Create SPC with owner reference (but don't create the actual CRD to avoid triggering reconciliation)
	spc := testutil.NewSecretProviderClass("default", "test-spc").
		WithServiceAccount("test-sa").
		WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "test-akv").
		Build()

	// Add to cache
	ctrl.cache.Set("default", "test-spc", spc)
	assert.True(t, ctrl.cache.Has("default", "test-spc"), "SPC should be in cache")

	// Call handleDeleted with inCache=true
	ctrl.handleDeleted(spc, true)

	// Cache should be cleared
	assert.False(t, ctrl.cache.Has("default", "test-spc"), "SPC should be removed from cache")

	// Note: The owner-check logic will run but the CRD won't be found, so it will log
	// "Owner CRD not found" and return early without attempting reconciliation. This is
	// the expected behavior for deleted resources and tests cache cleanup works correctly.
}

// TestHandleOwnedSPCDeletion_QueueBehavior tests that owner CRD is enqueued for valid owned SPCs
func TestHandleOwnedSPCDeletion_QueueBehavior(t *testing.T) {
	tests := []struct {
		name           string
		spc            *secretsstorev1.SecretProviderClass
		expectEnqueued bool
		expectedKey    string
	}{
		{
			name: "valid owned SPC enqueues owner CRD",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "test-akv").
				Build(),
			expectEnqueued: true,
			expectedKey:    "default/test-akv",
		},
		{
			name: "SPC with no owner references - nothing enqueued",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				Build(),
			expectEnqueued: false,
		},
		{
			name: "SPC with wrong API version - nothing enqueued",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1beta1", "AzureKeyVaultSync", "test-akv").
				Build(),
			expectEnqueued: false,
		},
		{
			name: "SPC with wrong kind - nothing enqueued",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "WrongKind", "test-akv").
				Build(),
			expectEnqueued: false,
		},
		{
			name: "SPC with controller=false - nothing enqueued",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "test-akv").
				WithControllerFalse().
				Build(),
			expectEnqueued: false,
		},
		{
			name: "SPC with controller=nil - nothing enqueued",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "test-akv").
				WithControllerNil().
				Build(),
			expectEnqueued: false,
		},
		{
			name: "owned SPC in different namespace",
			spc: testutil.NewSecretProviderClass("kube-system", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "system-akv").
				Build(),
			expectEnqueued: true,
			expectedKey:    "kube-system/system-akv",
		},
		{
			name: "multiple controller owners - only first is enqueued",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "first-akv").
				WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "second-akv").
				Build(),
			expectEnqueued: true,
			expectedKey:    "default/first-akv", // Should only enqueue first owner
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl, _ := newTestController(t)
			defer ctrl.queue.ShutDown()

			// Call handleOwnedSPCDeletion
			ctrl.handleOwnedSPCDeletion(tt.spc)

			// Check queue state
			keys := drainQueue(ctrl.queue)

			if tt.expectEnqueued {
				assert.Len(t, keys, 1, "expected owner CRD to be enqueued")
				if len(keys) > 0 {
					assert.Equal(t, QueueKey(tt.expectedKey), keys[0], "enqueued key should match owner CRD namespace/name")
				}
			} else {
				assert.Len(t, keys, 0, "expected nothing to be enqueued")
			}
		})
	}
}
// TestHandleOwnedSPCDeletion_Deduplication tests that multiple SPCs with the same owner
// result in only a single queue entry due to WorkQueue deduplication
func TestHandleOwnedSPCDeletion_Deduplication(t *testing.T) {
	ctrl, _ := newTestController(t)
	defer ctrl.queue.ShutDown()

	// Create two different SPCs owned by the same AzureKeyVaultSync CRD
	spc1 := testutil.NewSecretProviderClass("default", "first-spc").
		WithServiceAccount("test-sa").
		WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "shared-owner").
		Build()

	spc2 := testutil.NewSecretProviderClass("default", "second-spc").
		WithServiceAccount("test-sa").
		WithOwnerReference("azure-keyvault-sync.io/v1alpha1", "AzureKeyVaultSync", "shared-owner").
		Build()

	// Handle deletion of both SPCs
	ctrl.handleOwnedSPCDeletion(spc1)
	ctrl.handleOwnedSPCDeletion(spc2)

	// Verify only one queue entry exists due to WorkQueue deduplication
	keys := drainQueue(ctrl.queue)
	assert.Len(t, keys, 1, "expected single deduplicated key for shared owner")
	assert.Equal(t, QueueKey("default/shared-owner"), keys[0], "key should be owner CRD")
}
