# Architecture Improvements Plan

## Overview

This document outlines architectural improvements to address race conditions, concurrency issues, and production readiness concerns identified in the current controller implementation.

## Current Architecture Analysis

### Existing Design (Dual-Sync Pattern)

```
┌─────────────────────────────────────────────────────────────┐
│                     Controller.Run()                         │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. Initial syncCache() - full scan                         │
│  2. Start periodic resync goroutine (every 5 min)           │
│  3. Start watch loop (infinite)                             │
│                                                              │
│  ┌──────────────────┐      ┌──────────────────┐            │
│  │ Periodic Resync  │      │   Watch Events   │            │
│  │   (Goroutine)    │      │   (Main Loop)    │            │
│  │                  │      │                  │            │
│  │ Every 5 min:     │      │ On event:        │            │
│  │ - syncCache()    │      │ - handleEvent()  │            │
│  │   calls          │      │   calls          │            │
│  │   reconcile()    │      │   reconcile()    │            │
│  └──────────────────┘      └──────────────────┘            │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### Identified Problems

#### 1. Race Conditions ⚠️

**Scenario**: Two goroutines can reconcile the same resource simultaneously.

```
T0:  User modifies annotation on SecretProviderClass
T1:  Watch event → handleModified() → reconcileResource() starts
T2:  Periodic sync → syncCache() → reconcileResource() starts (SAME resource)
T3:  Both list vault secrets
T4:  Both patch SecretProviderClass
```

**Consequences**:
- Duplicate Azure API calls (cost + rate limits)
- Duplicate Kubernetes API calls
- Potential for conflicting patches
- Race in cache updates
- Wasted compute resources

**Evidence**:
```go
// No protection against concurrent reconciliation
func (ctrl *Controller) reconcileResource(obj *unstructured.Unstructured) error {
    // Expensive operations with no locking:
    // - List vault secrets (Azure API)
    // - List vault certificates (Azure API)
    // - Patch SecretProviderClass (K8s API)
}
```

#### 2. No Deduplication

**Problem**: Multiple events for same resource trigger multiple reconciliations.

**Example**:
```bash
# User runs: kubectl apply -f secretproviderclass.yaml
# Kubernetes fires multiple MODIFIED events (metadata, spec, status changes)

Event 1: MODIFIED (metadata.resourceVersion updated)
Event 2: MODIFIED (spec updated)
Event 3: MODIFIED (status updated)

# Result: reconcileResource() called 3 times in rapid succession
```

**Consequences**:
- Thundering herd problem
- Unnecessary vault queries
- API rate limit risk

#### 3. No Back-Pressure or Rate Limiting

**Problem**: Unlimited concurrent reconciliations possible.

**Scenario**:
```
User creates 10 SecretProviderClass resources simultaneously
→ 10 ADDED events
→ 10 concurrent reconcileResource() calls
→ 10 concurrent Azure token acquisitions
→ 10 concurrent vault listings
→ 10 concurrent patches
```

**Consequences**:
- Azure API rate limits exceeded
- Kubernetes API server load
- Memory pressure (concurrent Azure SDK calls)
- Token cache contention

#### 4. Cache Serves Limited Purpose

**Current cache usage**:
```go
ctrl.cache.Has(namespace, name)   // Check if resource tracked
ctrl.cache.Set(namespace, name, obj)  // Store full object
ctrl.cache.Delete(namespace, name)    // Remove from tracking
```

**Issues**:
- Stores entire `unstructured.Unstructured` objects (memory overhead)
- Not used for deduplication
- Not used to track "reconciliation in progress"
- Not used to prevent duplicate work
- Cache updates not atomic with reconciliation

#### 5. Error Handling Gaps

**Missing capabilities**:
- No retry logic with exponential backoff
- Transient failures = lost reconciliation
- No error metrics/tracking
- No circuit breaker for failing resources

---

## Proposed Architecture: Work Queue Pattern

### Design Overview

The **work queue pattern** is the standard Kubernetes controller architecture used by controller-runtime, kubebuilder, and operator-sdk.

```
┌────────────────────────────────────────────────────────────────┐
│                    Work Queue Architecture                      │
├────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────┐    ┌──────────────┐    ┌────────────────┐        │
│  │ Watch   │───▶│ Work Queue   │───▶│ Worker Pool    │        │
│  │ Events  │    │              │    │ (N goroutines) │        │
│  └─────────┘    │ - Dedupes    │    └────────────────┘        │
│                 │ - Rate limits│            │                  │
│  ┌─────────┐    │ - Retries    │            │                  │
│  │Periodic │───▶│              │            │                  │
│  │ Resync  │    └──────────────┘            ▼                  │
│  └─────────┘                      ┌────────────────┐           │
│                                   │ Reconcile      │           │
│                                   │ Resource       │           │
│                                   └────────────────┘           │
│                                            │                   │
│                                            ▼                   │
│                                   ┌────────────────┐           │
│                                   │ Update Cache   │           │
│                                   └────────────────┘           │
└────────────────────────────────────────────────────────────────┘
```

### Key Components

#### 1. Work Queue

**Purpose**: Central reconciliation dispatcher with built-in features.

**Capabilities**:
- **Deduplication**: Multiple events for same resource → single queue item
- **Rate limiting**: Control reconciliation throughput
- **Retry with backoff**: Automatic retry on failure
- **Delayed requeue**: Schedule future reconciliation

**Implementation**:
```go
import "k8s.io/client-go/util/workqueue"

type Controller struct {
    // ... existing fields ...

    // New: Work queue
    queue workqueue.RateLimitingInterface
}

func NewController(...) *Controller {
    ctrl := &Controller{
        // ... existing initialization ...

        // Create rate-limited work queue
        queue: workqueue.NewRateLimitingQueue(
            workqueue.DefaultControllerRateLimiter(),
        ),
    }
    return ctrl
}
```

**Queue Item Format**:
```go
// Simple string key: "namespace/name"
type QueueKey string

func keyFor(namespace, name string) QueueKey {
    return QueueKey(fmt.Sprintf("%s/%s", namespace, name))
}
```

#### 2. Worker Pool

**Purpose**: Fixed number of worker goroutines process queue.

**Benefits**:
- **Concurrency control**: N workers = max N concurrent reconciliations
- **Back-pressure**: Queue backs up if workers busy (vs unlimited goroutines)
- **Resource limits**: Predictable CPU/memory usage

**Implementation**:
```go
const numWorkers = 5  // Configurable

func (ctrl *Controller) Run(stopCh <-chan struct{}) error {
    defer ctrl.queue.ShutDown()

    // Initial sync
    ctrl.syncCache()

    // Start periodic resync
    go ctrl.startPeriodicResync(stopCh)

    // Start workers
    for i := 0; i < numWorkers; i++ {
        go ctrl.worker(stopCh)
    }

    <-stopCh
    return nil
}

func (ctrl *Controller) worker(stopCh <-chan struct{}) {
    for ctrl.processNextItem() {
        select {
        case <-stopCh:
            return
        default:
        }
    }
}

func (ctrl *Controller) processNextItem() bool {
    // Get next item from queue
    key, shutdown := ctrl.queue.Get()
    if shutdown {
        return false
    }
    defer ctrl.queue.Done(key)

    // Reconcile
    err := ctrl.reconcile(key.(QueueKey))

    // Handle result
    ctrl.handleReconcileResult(key, err)

    return true
}
```

#### 3. Reconcile with Retry

**Purpose**: Single reconciliation function with error handling.

**Implementation**:
```go
func (ctrl *Controller) reconcile(key QueueKey) error {
    // Parse key
    namespace, name, err := parseKey(key)
    if err != nil {
        return err
    }

    // Get resource
    obj, err := ctrl.client.Resource(ctrl.gvr).Namespace(namespace).Get(
        ctrl.ctx, name, metav1.GetOptions{},
    )
    if err != nil {
        if errors.IsNotFound(err) {
            // Resource deleted, remove from cache
            ctrl.cache.Delete(namespace, name)
            return nil
        }
        return err
    }

    // Validate
    if valid, _ := isValidForSync(obj); !valid {
        return nil
    }

    // Reconcile
    err = ctrl.reconcileResource(obj)
    if err != nil {
        return fmt.Errorf("reconciliation failed: %w", err)
    }

    // Update cache
    ctrl.cache.Set(namespace, name, obj.DeepCopy())

    return nil
}

func (ctrl *Controller) handleReconcileResult(key interface{}, err error) {
    if err == nil {
        // Success - remove from rate limiter
        ctrl.queue.Forget(key)
        return
    }

    // Retry with exponential backoff
    if ctrl.queue.NumRequeues(key) < 5 {
        log.Printf("Error reconciling %v, retrying: %v", key, err)
        ctrl.queue.AddRateLimited(key)
        return
    }

    // Max retries exceeded
    log.Printf("Dropping %v from queue after max retries: %v", key, err)
    ctrl.queue.Forget(key)
}
```

#### 4. Event Handler Simplification

**Purpose**: Events just enqueue items, don't reconcile directly.

**Benefits**:
- No race conditions (queue deduplicates)
- Fast event handling (just enqueue)
- All reconciliation in workers

**Implementation**:
```go
func (ctrl *Controller) handleAdded(obj *unstructured.Unstructured) {
    if valid, serviceAccount := isValidForSync(obj); valid {
        log.Printf("Event: ADDED %s/%s (sync enabled, service-account: %s)",
            obj.GetNamespace(), obj.GetName(), serviceAccount)

        // Enqueue for reconciliation
        key := keyFor(obj.GetNamespace(), obj.GetName())
        ctrl.queue.Add(key)
    }
}

func (ctrl *Controller) handleModified(obj *unstructured.Unstructured) {
    namespace := obj.GetNamespace()
    name := obj.GetName()
    key := keyFor(namespace, name)

    enabled := isSyncEnabled(obj)
    inCache := ctrl.cache.Has(namespace, name)

    if enabled && (inCache || !inCache) {
        // Resource should be synced, enqueue
        log.Printf("Event: MODIFIED %s/%s (enqueuing for reconciliation)", namespace, name)
        ctrl.queue.Add(key)
    } else if !enabled && inCache {
        // Sync disabled, remove from cache
        log.Printf("Event: MODIFIED %s/%s (sync disabled, removing from cache)", namespace, name)
        ctrl.cache.Delete(namespace, name)
    }
}

func (ctrl *Controller) handleDeleted(namespace, name string, inCache bool) {
    if inCache {
        log.Printf("Event: DELETED %s/%s (removing from cache)", namespace, name)
        ctrl.cache.Delete(namespace, name)
    }
}
```

#### 5. Periodic Resync Integration

**Purpose**: Enqueue all resources periodically for drift detection.

**Implementation**:
```go
func (ctrl *Controller) startPeriodicResync(stopCh <-chan struct{}) {
    ticker := time.NewTicker(resyncInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            ctrl.enqueueAll()
        case <-stopCh:
            return
        }
    }
}

func (ctrl *Controller) enqueueAll() {
    log.Println("Periodic resync: enqueuing all tracked resources")

    result, err := ctrl.client.Resource(ctrl.gvr).Namespace("").List(
        ctrl.ctx, metav1.ListOptions{},
    )
    if err != nil {
        log.Printf("Error listing resources for resync: %v", err)
        return
    }

    for _, item := range result.Items {
        if valid, _ := isValidForSync(&item); valid {
            key := keyFor(item.GetNamespace(), item.GetName())
            ctrl.queue.Add(key)
        }
    }

    log.Printf("Enqueued %d resources for periodic resync", len(result.Items))
}
```

---

## Migration Strategy

### Phase 1: Add Work Queue (No Behavior Change)

**Goal**: Introduce queue infrastructure without changing reconciliation behavior.

**Steps**:
1. Add `workqueue` dependency to go.mod
2. Add `queue` field to Controller struct
3. Keep existing reconciliation logic
4. Events still call reconcileResource() directly (no queue yet)
5. Verify compilation and basic functionality

**Validation**: No functional changes, queue exists but unused.

---

### Phase 2: Route Events Through Queue

**Goal**: Events enqueue items instead of direct reconciliation.

**Steps**:
1. Update handleAdded() to enqueue instead of reconcile
2. Update handleModified() to enqueue instead of reconcile
3. Implement worker pool (start with 1 worker for safety)
4. Implement processNextItem() and reconcile()
5. Test event-driven reconciliation through queue

**Validation**: Single-threaded reconciliation via queue.

---

### Phase 3: Add Concurrency

**Goal**: Enable worker pool for concurrent reconciliation.

**Steps**:
1. Increase numWorkers from 1 to 5 (configurable)
2. Test concurrent reconciliation
3. Monitor for race conditions
4. Add metrics for queue depth, worker utilization

**Validation**: Concurrent reconciliation without race conditions.

---

### Phase 4: Add Retry Logic

**Goal**: Handle transient failures gracefully.

**Steps**:
1. Implement handleReconcileResult() with retry logic
2. Configure exponential backoff
3. Add max retry limits
4. Test failure scenarios

**Validation**: Failed reconciliations retry automatically.

---

### Phase 5: Optimize Periodic Resync

**Goal**: Periodic resync uses queue for controlled processing.

**Steps**:
1. Replace syncCache() loop with enqueueAll()
2. Let workers process queued items
3. Test periodic resync behavior

**Validation**: Periodic resync doesn't overwhelm system.

---

## Benefits of Work Queue Pattern

### 1. Race Condition Elimination

**Before**:
```
Event 1: handleModified() → reconcileResource() (concurrent)
Event 2: Periodic sync → reconcileResource() (concurrent)
Both access same resource simultaneously → RACE
```

**After**:
```
Event 1: handleModified() → queue.Add("default/my-resource")
Event 2: Periodic sync → queue.Add("default/my-resource")
Queue deduplicates → Single reconciliation
Worker processes → No race
```

### 2. Automatic Deduplication

**Queue behavior**:
- Multiple Add() calls for same key → Single queue item
- Events processed serially per resource
- No duplicate work

### 3. Rate Limiting

**Built-in rate limiter**:
- Per-item exponential backoff on failure
- Global rate limiting possible
- Prevents API abuse

### 4. Retry Logic

**Automatic retry**:
- Transient failures retry with backoff
- Configurable max retries
- Permanent failures dropped after limit

### 5. Back-Pressure

**Controlled concurrency**:
- N workers = max N concurrent reconciliations
- Queue backs up under load (vs unlimited goroutines)
- Predictable resource usage

### 6. Observability

**Metrics available**:
- Queue depth
- Worker utilization
- Retry counts
- Processing duration

---

## Configuration Options

### Tunable Parameters

```go
const (
    // Worker pool size
    numWorkers = 5  // Default: 5 concurrent reconciliations

    // Rate limiting
    baseDelay     = 5 * time.Second   // Initial retry delay
    maxDelay      = 1000 * time.Second // Max retry delay
    maxRetries    = 5  // Max retry attempts before dropping

    // Resync
    resyncInterval = 5 * time.Minute  // Periodic resync frequency
)
```

### Environment Variable Override

```go
func loadConfig() {
    if val := os.Getenv("NUM_WORKERS"); val != "" {
        numWorkers = parseInt(val)
    }
    if val := os.Getenv("RESYNC_INTERVAL"); val != "" {
        resyncInterval = parseDuration(val)
    }
}
```

---

## Testing Strategy

### Unit Tests

**Test deduplication**:
```go
func TestQueueDeduplication(t *testing.T) {
    queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

    // Add same key multiple times
    queue.Add("default/test")
    queue.Add("default/test")
    queue.Add("default/test")

    // Should only process once
    key, _ := queue.Get()
    assert.Equal(t, "default/test", key)

    // Queue should be empty
    assert.Equal(t, 0, queue.Len())
}
```

**Test retry logic**:
```go
func TestRetryBackoff(t *testing.T) {
    // Verify exponential backoff increases delay
    // Verify max retries honored
}
```

### Integration Tests

**Test concurrent reconciliation**:
```go
func TestConcurrentReconciliation(t *testing.T) {
    // Create multiple resources
    // Trigger simultaneous events
    // Verify no race conditions
    // Verify all reconciled successfully
}
```

**Test event deduplication**:
```go
func TestEventDeduplication(t *testing.T) {
    // Fire multiple MODIFIED events rapidly
    // Verify single reconciliation
    // Verify final state correct
}
```

---

## Success Criteria

- [ ] Work queue infrastructure added
- [ ] Events route through queue
- [ ] Worker pool operational (5 workers)
- [ ] Retry logic with exponential backoff
- [ ] Periodic resync uses queue
- [ ] No race conditions detected
- [ ] Deduplication working
- [ ] Rate limiting functional
- [ ] Metrics exposed
- [ ] Documentation updated

---

## Risks and Mitigation

### Risk: Breaking Existing Behavior

**Mitigation**: Phased migration with validation at each step.

### Risk: Performance Regression

**Mitigation**: Benchmark before/after, tune worker count.

### Risk: Queue Overflow

**Mitigation**: Monitor queue depth, alert if > 1000 items.

### Risk: Worker Starvation

**Mitigation**: Add worker utilization metrics, tune numWorkers.

---

## Estimated Complexity

**Development Time**: 8-12 hours
- Phase 1 (Add queue): 2 hours
- Phase 2 (Route events): 3 hours
- Phase 3 (Concurrency): 2 hours
- Phase 4 (Retry logic): 2 hours
- Phase 5 (Periodic resync): 1 hour
- Testing/debugging: 2-4 hours

**Testing Time**: 4-6 hours
- Unit tests: 2 hours
- Integration tests: 2 hours
- Load testing: 2 hours

**Total**: 12-18 hours for complete implementation

---

## References

- [Kubernetes Sample Controller](https://github.com/kubernetes/sample-controller)
- [controller-runtime Work Queue](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/internal/controller/controller.go)
- [client-go workqueue docs](https://pkg.go.dev/k8s.io/client-go/util/workqueue)
