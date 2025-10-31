package controller

import (
	"context"
	"testing"
	"time"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/testutil"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/kubernetes"
)

// TestProcessNextItem tests the processNextItem function
func TestProcessNextItem(t *testing.T) {
	t.Run("process valid item from queue", func(t *testing.T) {
		ctrl, env := newTestController(t)
		defer ctrl.queue.ShutDown()

		// Create a valid SPC
		spc := testutil.NewSecretProviderClass("default", "test-spc").
			WithServiceAccount("test-sa").
			Build()
		// Add clientID manually
		spc.Spec.Parameters["clientID"] = "test-client-id"
		env.WithSecretProviderClass(spc)

		// Mock token provider to return success
		ctrl.tokenProvider = &MockTokenProvider{
			GetK8sTokenFunc: func(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error) {
				return "mock-k8s-token", nil
			},
			GetAzureTokenFunc: func(ctx context.Context, namespace, serviceAccount, k8sToken, clientID, tenantID string) (string, time.Time, error) {
				return "mock-azure-token", time.Now().Add(time.Hour), nil
			},
		}

		// Mock vault client to return empty results
		ctrl.vaultClient = &MockVaultClient{
			ListSecretsFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
				return []azure.VaultSecret{}, nil
			},
			ListCertificatesFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
				return []azure.VaultCertificate{}, nil
			},
		}

		// Mock patch client
		ctrl.patchClient = &MockPatchClient{
			PatchSecretProviderClassFunc: func(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, timestamp string) error {
				return nil
			},
		}

		// Add item to queue
		key := keyFor("default", "test-spc")
		ctrl.queue.Add(key)

		// Process the item
		ctx := context.Background()
		result := ctrl.processNextItem(ctx)

		// Should return true (processed successfully)
		assert.True(t, result)

		// Queue should be empty now
		assert.Equal(t, 0, ctrl.queue.Len())
	})

	t.Run("shutdown returns false", func(t *testing.T) {
		ctrl, _ := newTestController(t)

		// Shutdown the queue
		ctrl.queue.ShutDown()

		// Process should return false on shutdown
		ctx := context.Background()
		result := ctrl.processNextItem(ctx)

		assert.False(t, result)
	})
}

// TestHandleReconcileResult tests the handleReconcileResult function
func TestHandleReconcileResult(t *testing.T) {
	t.Run("success removes from rate limiter", func(t *testing.T) {
		ctrl := newMinimalController()
		defer ctrl.queue.ShutDown()

		key := keyFor("default", "test-spc")
		ctrl.queue.Add(key)

		// Handle successful result
		ctrl.handleReconcileResult(key, nil)

		// Should be forgotten from rate limiter
		// Queue length should be 0 after processing
		assert.Equal(t, 0, ctrl.queue.NumRequeues(key))
	})

	t.Run("error triggers retry", func(t *testing.T) {
		ctrl := newMinimalController()
		defer ctrl.queue.ShutDown()

		key := keyFor("default", "test-spc")

		// Handle error result
		ctrl.handleReconcileResult(key, assert.AnError)

		// Should be added back to queue with rate limiting
		// The item might not be immediately visible in queue length due to rate limiting,
		// but NumRequeues should be incremented
		assert.Equal(t, 1, ctrl.queue.NumRequeues(key))
	})
}

// TestReconcile tests the reconcile function
func TestReconcile(t *testing.T) {
	t.Run("reconcile existing resource", func(t *testing.T) {
		ctrl, env := newTestController(t)
		defer ctrl.queue.ShutDown()

		// Create a valid SPC
		spc := testutil.NewSecretProviderClass("default", "test-spc").
			WithServiceAccount("test-sa").
			Build()
		// Add clientID manually (no builder method for this)
		spc.Spec.Parameters["clientID"] = "test-client-id"
		env.WithSecretProviderClass(spc)

		// Mock token provider
		ctrl.tokenProvider = &MockTokenProvider{
			GetK8sTokenFunc: func(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error) {
				return "mock-k8s-token", nil
			},
			GetAzureTokenFunc: func(ctx context.Context, namespace, serviceAccount, k8sToken, clientID, tenantID string) (string, time.Time, error) {
				return "mock-azure-token", time.Now().Add(time.Hour), nil
			},
		}

		// Mock vault client
		ctrl.vaultClient = &MockVaultClient{
			ListSecretsFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
				return []azure.VaultSecret{}, nil
			},
			ListCertificatesFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
				return []azure.VaultCertificate{}, nil
			},
		}

		// Mock patch client
		ctrl.patchClient = &MockPatchClient{
			PatchSecretProviderClassFunc: func(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, timestamp string) error {
				return nil
			},
		}

		// Reconcile the resource
		key := keyFor("default", "test-spc")
		ctx := context.Background()
		err := ctrl.reconcile(ctx, key)

		// Should succeed
		assert.NoError(t, err)

		// Should be in cache
		assert.True(t, ctrl.cache.Has("default", "test-spc"))
	})

	t.Run("reconcile non-existent resource", func(t *testing.T) {
		ctrl, _ := newTestController(t)
		defer ctrl.queue.ShutDown()

		// Try to reconcile a non-existent resource
		key := keyFor("default", "does-not-exist")
		ctx := context.Background()
		err := ctrl.reconcile(ctx, key)

		// Should not error (resource was deleted)
		assert.NoError(t, err)
	})

	t.Run("reconcile with invalid key format", func(t *testing.T) {
		ctrl, _ := newTestController(t)
		defer ctrl.queue.ShutDown()

		// Try to reconcile with invalid key
		key := QueueKey("invalid-key-no-slash")
		ctx := context.Background()
		err := ctrl.reconcile(ctx, key)

		// Should error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid key format")
	})
}

// TestDrainQueue tests the drainQueue function
func TestDrainQueue(t *testing.T) {
	t.Run("drain empty queue", func(t *testing.T) {
		ctrl := newMinimalController()
		defer ctrl.queue.ShutDown()

		// Drain empty queue (should return immediately)
		ctrl.drainQueue()

		// Queue should still be empty
		assert.Equal(t, 0, ctrl.queue.Len())
	})

	t.Run("drain queue with items", func(t *testing.T) {
		ctrl := newMinimalController()
		defer ctrl.queue.ShutDown()

		// Add some items
		ctrl.queue.Add(keyFor("default", "spc-1"))
		ctrl.queue.Add(keyFor("default", "spc-2"))

		// Get the items to mark them as processing
		key1, _ := ctrl.queue.Get()
		key2, _ := ctrl.queue.Get()

		// Start draining in goroutine
		done := make(chan bool)
		go func() {
			ctrl.drainQueue()
			done <- true
		}()

		// Mark items as done after a short delay
		time.Sleep(100 * time.Millisecond)
		ctrl.queue.Done(key1)
		ctrl.queue.Done(key2)

		// Wait for drain to complete
		select {
		case <-done:
			// Success
		case <-time.After(2 * time.Second):
			t.Fatal("drainQueue did not complete in time")
		}

		assert.Equal(t, 0, ctrl.queue.Len())
	})
}

// TestStartPeriodicResync tests the startPeriodicResync function
func TestStartPeriodicResync(t *testing.T) {
	t.Run("periodic resync triggers enqueueAll", func(t *testing.T) {
		ctrl, env := newTestController(t)
		defer ctrl.queue.ShutDown()

		// Set a very short sync interval for testing
		ctrl.config.SyncInterval = 100 * time.Millisecond

		// Create a test SPC in the cluster
		spc := testutil.NewSecretProviderClass("default", "test-spc").
			WithServiceAccount("test-sa").
			Build()
		env.WithSecretProviderClass(spc)

		// Start periodic resync in background
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		go ctrl.startPeriodicResync(ctx)

		// Wait for at least one resync to occur
		time.Sleep(250 * time.Millisecond)

		// Queue should have been populated (at least once)
		// Note: There might be multiple entries due to multiple ticks
		assert.GreaterOrEqual(t, ctrl.queue.Len(), 1)
	})

	t.Run("periodic resync stops on context cancellation", func(t *testing.T) {
		ctrl, _ := newTestController(t)
		defer ctrl.queue.ShutDown()

		// Set a short sync interval
		ctrl.config.SyncInterval = 50 * time.Millisecond

		// Create a context that cancels immediately
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		// Start periodic resync - should exit quickly
		done := make(chan bool)
		go func() {
			ctrl.startPeriodicResync(ctx)
			done <- true
		}()

		// Should exit within 100ms
		select {
		case <-done:
			// Success
		case <-time.After(200 * time.Millisecond):
			t.Fatal("startPeriodicResync did not stop after context cancellation")
		}
	})
}

// TestWorker tests the worker function
func TestWorker(t *testing.T) {
	t.Run("worker processes items from queue", func(t *testing.T) {
		ctrl, env := newTestController(t)

		// Create a test SPC
		spc := testutil.NewSecretProviderClass("default", "test-spc").
			WithServiceAccount("test-sa").
			Build()
		// Add clientID manually
		spc.Spec.Parameters["clientID"] = "test-client-id"
		env.WithSecretProviderClass(spc)

		// Mock dependencies for successful processing
		ctrl.tokenProvider = &MockTokenProvider{
			GetK8sTokenFunc: func(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error) {
				return "mock-token", nil
			},
			GetAzureTokenFunc: func(ctx context.Context, namespace, serviceAccount, k8sToken, clientID, tenantID string) (string, time.Time, error) {
				return "mock-azure-token", time.Now().Add(time.Hour), nil
			},
		}
		ctrl.vaultClient = &MockVaultClient{
			ListSecretsFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
				return []azure.VaultSecret{}, nil
			},
			ListCertificatesFunc: func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
				return []azure.VaultCertificate{}, nil
			},
		}
		ctrl.patchClient = &MockPatchClient{
			PatchSecretProviderClassFunc: func(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, timestamp string) error {
				return nil
			},
		}

		// Add work to queue
		ctrl.queue.Add(keyFor("default", "test-spc"))

		// Start worker
		ctx := context.Background()

		done := make(chan bool)
		go func() {
			ctrl.worker(ctx)
			done <- true
		}()

		// Wait for item to be processed
		time.Sleep(200 * time.Millisecond)

		// Item should be in cache after processing
		assert.True(t, ctrl.cache.Has("default", "test-spc"))

		// Shutdown queue to make worker exit
		ctrl.queue.ShutDown()

		// Wait for worker to finish
		select {
		case <-done:
			// Worker finished
		case <-time.After(500 * time.Millisecond):
			t.Fatal("worker did not exit after queue shutdown")
		}
	})

	t.Run("worker exits on queue shutdown", func(t *testing.T) {
		ctrl, _ := newTestController(t)

		// Start worker
		ctx := context.Background()
		done := make(chan bool)
		go func() {
			ctrl.worker(ctx)
			done <- true
		}()

		// Shutdown queue immediately - worker should exit
		ctrl.queue.ShutDown()

		// Worker should exit when processNextItem returns false on shutdown
		select {
		case <-done:
			// Success - worker exited on queue shutdown
		case <-time.After(200 * time.Millisecond):
			t.Fatal("worker did not stop after queue shutdown")
		}
	})
}

// TestRun tests the Run function (basic test - full integration test would be in e2e)
func TestRun(t *testing.T) {
	t.Run("Run starts and stops cleanly", func(t *testing.T) {
		ctrl, _ := newTestController(t)
		// Note: queue will be shut down by Run()

		// Set minimal intervals for quick test
		ctrl.config.SyncInterval = 100 * time.Millisecond
		ctrl.config.WorkerCount = 1

		// Create a context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		// Run should execute and exit when context is cancelled
		done := make(chan bool)
		go func() {
			ctrl.Run(ctx)
			done <- true
		}()

		// Wait for Run to complete
		select {
		case <-done:
			// Success - Run exited cleanly
		case <-time.After(1 * time.Second):
			t.Fatal("Run did not complete in time")
		}
	})
}
