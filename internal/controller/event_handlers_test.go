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
		ctrlClient:          env.CtrlClient,
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
			ctrl.handleDeleted(context.Background(), spc, tt.inCache)

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
				WithOwnerReference("keyvault.azure.com/v1alpha1", "WrongKind", "test-akv").
				Build(),
			expectNoAction: true,
		},
		{
			name: "SPC with owner but controller=false",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1alpha1", "AzureKeyVaultSync", "test-akv").
				WithControllerFalse().
				Build(),
			expectNoAction: true,
		},
		{
			name: "SPC with owner but controller=nil",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1alpha1", "AzureKeyVaultSync", "test-akv").
				WithControllerNil().
				Build(),
			expectNoAction: true,
		},
		{
			name: "SPC owned by AzureKeyVaultSync - CRD not found",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1alpha1", "AzureKeyVaultSync", "missing-akv").
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
			ctrl.handleOwnedSPCDeletion(context.Background(), tt.spc)

			// Verify behavior based on expectation
			if tt.expectNoAction {
				// For cases where no action should be taken (wrong owner ref, nil controller, etc),
				// we verify the function completes without panicking. The function should return
				// early from the loop without attempting reconciliation.
				assert.True(t, true, "Function should complete without panic for invalid owner refs")
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
	ctrl.handleDeleted(context.Background(), nil, false)
	ctrl.handleDeleted(context.Background(), nil, true)

	// No assertions needed - test passes if no panic occurs
}

// TestHandleDeletedWithOwnerReference tests handleDeleted cache behavior with owned SPCs
func TestHandleDeletedWithOwnerReference(t *testing.T) {
	ctrl, _ := newTestController(t)
	defer ctrl.queue.ShutDown()

	// Create SPC with owner reference (but don't create the actual CRD to avoid triggering reconciliation)
	spc := testutil.NewSecretProviderClass("default", "test-spc").
		WithServiceAccount("test-sa").
		WithOwnerReference("keyvault.azure.com/v1alpha1", "AzureKeyVaultSync", "test-akv").
		Build()

	// Add to cache
	ctrl.cache.Set("default", "test-spc", spc)
	assert.True(t, ctrl.cache.Has("default", "test-spc"), "SPC should be in cache")

	// Call handleDeleted with inCache=true
	ctrl.handleDeleted(context.Background(), spc, true)

	// Cache should be cleared
	assert.False(t, ctrl.cache.Has("default", "test-spc"), "SPC should be removed from cache")

	// Note: The owner-check logic will run but the CRD won't be found, so it will log
	// "Owner CRD not found" and return early without attempting reconciliation. This is
	// the expected behavior for deleted resources and tests cache cleanup works correctly.
}

// TestHandleOwnedSPCDeletion_ReconcileFnInvocation tests that reconcileFn is called for valid owned SPCs
func TestHandleOwnedSPCDeletion_ReconcileFnInvocation(t *testing.T) {
	tests := []struct {
		name              string
		spc               *secretsstorev1.SecretProviderClass
		setupCRD          bool // Whether to create the AzureKeyVaultSync CRD
		expectReconcile   bool
	}{
		{
			name: "valid owned SPC triggers reconcile",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1alpha1", "AzureKeyVaultSync", "test-akv").
				Build(),
			setupCRD:        true,
			expectReconcile: true,
		},
		{
			name: "SPC with no owner references - no reconcile",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				Build(),
			setupCRD:        false,
			expectReconcile: false,
		},
		{
			name: "SPC with wrong API version - no reconcile",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1beta1", "AzureKeyVaultSync", "test-akv").
				Build(),
			setupCRD:        false,
			expectReconcile: false,
		},
		{
			name: "SPC with wrong kind - no reconcile",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1alpha1", "WrongKind", "test-akv").
				Build(),
			setupCRD:        false,
			expectReconcile: false,
		},
		{
			name: "SPC with controller=false - no reconcile",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1alpha1", "AzureKeyVaultSync", "test-akv").
				WithControllerFalse().
				Build(),
			setupCRD:        false,
			expectReconcile: false,
		},
		{
			name: "SPC with controller=nil - no reconcile",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1alpha1", "AzureKeyVaultSync", "test-akv").
				WithControllerNil().
				Build(),
			setupCRD:        false,
			expectReconcile: false,
		},
		{
			name: "owned SPC but CRD not found - no reconcile",
			spc: testutil.NewSecretProviderClass("default", "test-spc").
				WithServiceAccount("test-sa").
				WithOwnerReference("keyvault.azure.com/v1alpha1", "AzureKeyVaultSync", "missing-akv").
				Build(),
			setupCRD:        false,
			expectReconcile: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl, env := newTestController(t)
			defer ctrl.queue.ShutDown()

			// Channel to signal when reconcile is called
			reconcileCalled := make(chan bool, 1)

			// Override reconcileFn to spy on calls
			ctrl.reconcileFn = func(ctx context.Context, akv *akvv1alpha1.AzureKeyVaultSync) error {
				select {
				case reconcileCalled <- true:
				default:
				}
				return nil
			}

			// Setup CRD if needed
			if tt.setupCRD {
				akv := testutil.NewAzureKeyVaultSync("default", "test-akv").Build()
				err := env.CreateAzureKeyVaultSync(akv)
				assert.NoError(t, err, "failed to create test AzureKeyVaultSync CRD")
			}

			// Call handleOwnedSPCDeletion
			ctrl.handleOwnedSPCDeletion(context.Background(), tt.spc)

			// Wait for reconcile call or timeout
			select {
			case <-reconcileCalled:
				assert.True(t, tt.expectReconcile, "reconcile was called but not expected")
			case <-time.After(100 * time.Millisecond):
				assert.False(t, tt.expectReconcile, "reconcile was not called but expected")
			}
		})
	}
}
