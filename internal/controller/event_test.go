package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

// TestHandleEvent tests the handleEvent function
func TestHandleEvent(t *testing.T) {
	tests := []struct {
		name       string
		event      watch.Event
		setupCache func(*Controller)
		checkQueue func(*testing.T, *Controller)
	}{
		{
			name: "Added event with service-account",
			event: watch.Event{
				Type: watch.Added,
				Object: &secretsstorev1.SecretProviderClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-spc",
						Namespace: "default",
						Annotations: map[string]string{
							"azure-keyvault-sync/service-account": "test-sa",
						},
					},
				},
			},
			setupCache: func(ctrl *Controller) {
				// Cache should be empty initially
			},
			checkQueue: func(t *testing.T, ctrl *Controller) {
				// After Added event, item should be enqueued
				assert.Equal(t, 1, ctrl.queue.Len())
			},
		},
		{
			name: "Modified event with service-account",
			event: watch.Event{
				Type: watch.Modified,
				Object: &secretsstorev1.SecretProviderClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-spc",
						Namespace: "default",
						Annotations: map[string]string{
							"azure-keyvault-sync/service-account": "test-sa",
						},
					},
				},
			},
			setupCache: func(ctrl *Controller) {
				// Pre-populate cache
				spc := &secretsstorev1.SecretProviderClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-spc",
						Namespace: "default",
					},
				}
				ctrl.cache.Set("default", "test-spc", spc)
			},
			checkQueue: func(t *testing.T, ctrl *Controller) {
				// Should enqueue for reconciliation
				assert.Equal(t, 1, ctrl.queue.Len())
				// Should still be in cache (only removed after reconciliation or annotation removal)
				assert.True(t, ctrl.cache.Has("default", "test-spc"))
			},
		},
		{
			name: "Deleted event - existing in cache",
			event: watch.Event{
				Type: watch.Deleted,
				Object: &secretsstorev1.SecretProviderClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-spc",
						Namespace: "default",
					},
				},
			},
			setupCache: func(ctrl *Controller) {
				spc := &secretsstorev1.SecretProviderClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-spc",
						Namespace: "default",
					},
				}
				ctrl.cache.Set("default", "test-spc", spc)
			},
			checkQueue: func(t *testing.T, ctrl *Controller) {
				// Should be removed from cache
				assert.False(t, ctrl.cache.Has("default", "test-spc"))
				// Should not enqueue deleted items
				assert.Equal(t, 0, ctrl.queue.Len())
			},
		},
		{
			name: "Error event",
			event: watch.Event{
				Type: watch.Error,
				Object: &secretsstorev1.SecretProviderClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-spc",
						Namespace: "default",
					},
				},
			},
			setupCache: func(ctrl *Controller) {},
			checkQueue: func(t *testing.T, ctrl *Controller) {
				// Error events don't modify cache or queue
				assert.Equal(t, 0, ctrl.queue.Len())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := newMinimalController()
			tt.setupCache(ctrl)

			// Call handleEvent
			ctrl.handleEvent(tt.event)

			// Verify queue/cache state
			tt.checkQueue(t, ctrl)
		})
	}
}

// TestHandleEvent_UnexpectedObjectType tests handling of unexpected object types
func TestHandleEvent_UnexpectedObjectType(t *testing.T) {
	ctrl := newMinimalController()

	// Create event with wrong object type (Pod instead of SecretProviderClass)
	event := watch.Event{
		Type:   watch.Added,
		Object: &corev1.Pod{}, // Wrong type
	}

	// Should not panic and should handle gracefully
	ctrl.handleEvent(event)

	// Cache should not be modified
	assert.Equal(t, 0, len(ctrl.cache.List()))
}

// TestHandleEvent_AllEventTypes tests all watch event types
func TestHandleEvent_AllEventTypes(t *testing.T) {
	eventTypes := []watch.EventType{
		watch.Added,
		watch.Modified,
		watch.Deleted,
		watch.Error,
		watch.Bookmark,
	}

	for _, eventType := range eventTypes {
		t.Run(string(eventType), func(t *testing.T) {
			ctrl := newMinimalController()

			spc := &secretsstorev1.SecretProviderClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-spc",
					Namespace: "default",
				},
			}

			// Pre-populate cache for Modified/Deleted
			if eventType == watch.Modified || eventType == watch.Deleted {
				ctrl.cache.Set("default", "test-spc", spc)
			}

			event := watch.Event{
				Type:   eventType,
				Object: spc,
			}

			// Should not panic
			ctrl.handleEvent(event)
		})
	}
}

// TestSyncCache tests the syncCache function
func TestSyncCache(t *testing.T) {
	ctrl := newMinimalController()

	// Add some items to cache
	spc1 := &secretsstorev1.SecretProviderClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spc-1",
			Namespace: "default",
		},
	}
	spc2 := &secretsstorev1.SecretProviderClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spc-2",
			Namespace: "kube-system",
		},
	}

	ctrl.cache.Set("default", "spc-1", spc1)
	ctrl.cache.Set("kube-system", "spc-2", spc2)

	// Call syncCache with context
	ctx := context.Background()
	ctrl.syncCache(ctx)

	// Should print cache contents without error
	// We can't easily verify the log output, but we can verify it doesn't panic
	assert.NotNil(t, ctrl.cache)
}

// TestSyncCache_EmptyCache tests syncCache with empty cache
func TestSyncCache_EmptyCache(t *testing.T) {
	ctrl := newMinimalController()

	// Call syncCache with empty cache
	ctx := context.Background()
	ctrl.syncCache(ctx)

	// Should handle empty cache gracefully
	assert.NotNil(t, ctrl.cache)
}
