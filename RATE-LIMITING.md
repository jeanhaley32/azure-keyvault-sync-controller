# Rate Limiting Analysis

**Date:** 2025-10-27
**Status:** Current Implementation + Recommendations

---

## Overview

This document analyzes the controller's rate limiting mechanisms, identifies potential overload scenarios, and recommends additional controls to prevent cluster spam.

---

## Current Rate Limiting Mechanisms

### 1. Work Queue Rate Limiting ✅

**Implementation:** `workqueue.DefaultTypedControllerRateLimiter`

```go
queue: workqueue.NewTypedRateLimitingQueue(
    workqueue.DefaultTypedControllerRateLimiter[QueueKey]()
)
```

**What it does:**
- **Exponential backoff** for failed reconciliations
- **Deduplication** - Multiple events for same resource → single reconciliation
- **Rate limiting** - `AddRateLimited()` enforces backoff between retries

**Default behavior:**
- Initial delay: 5ms
- Max delay: 1000s (~16 minutes)
- Exponential growth: delay = baseDelay * 2^(failures)

**Example:**
```
Attempt 1: 5ms delay
Attempt 2: 10ms delay
Attempt 3: 20ms delay
Attempt 4: 40ms delay
Attempt 5: 80ms delay
```

### 2. Concurrent Worker Limit ✅

**Configuration:** `WORKER_COUNT` (default: 5, range: 1-100)

```go
WorkerCount: parseIntEnv("WORKER_COUNT", 5)
```

**What it does:**
- Limits **concurrent reconciliations**
- Prevents work queue from overwhelming the controller
- Acts as natural rate limiter (max 5 resources processing simultaneously)

**Effect:**
- With 5 workers: Maximum 5 concurrent API calls to Kubernetes/Azure
- With 100 workers: Maximum 100 concurrent operations (not recommended)

### 3. Periodic Resync Interval ✅

**Configuration:** `SYNC_INTERVAL` (default: 5m, minimum: 30s)

```go
SyncInterval: parseDurationEnv("SYNC_INTERVAL", 5*time.Minute)

// Validation enforces minimum
if c.SyncInterval < 30*time.Second {
    return fmt.Errorf("SYNC_INTERVAL must be at least 30s")
}
```

**What it does:**
- Controls how often **all resources** are re-enqueued for reconciliation
- Prevents excessive API calls from periodic resyncs
- Minimum 30s prevents DoS-level API call rates

### 4. Max Retry Attempts ✅

**Configuration:** Hardcoded `maxRetries = 5`

```go
const maxRetries = 5

// In handleReconcileResult:
if ctrl.queue.NumRequeues(key) >= maxRetries {
    ctrl.queue.Forget(key)
    return
}
```

**What it does:**
- Drops resources after 5 failed attempts
- Prevents infinite retry loops
- Frees work queue capacity

### 5. Token Caching ✅

**Kubernetes Tokens:**
- Cached for 1 hour
- Renewed at 80% lifetime (48 min)
- Prevents excessive TokenRequest API calls

**Azure AD Tokens:**
- Cached for ~28 hours
- Renewed at 80% lifetime (22.4 hours)
- Single token reused across multiple vaults

**Effect:** Dramatic reduction in authentication API calls

---

## Rate Limiting Gaps ❌

### 1. No Kubernetes API Client QPS Limits ⚠️ CRITICAL

**Current State:**
```go
config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
dynamicClient, err := dynamic.NewForConfig(config)
```

**Problem:** No explicit QPS/Burst limits set on client

**Default Kubernetes Client Limits:**
- QPS: 5 queries per second
- Burst: 10 simultaneous requests

**Risk:** Controller can overwhelm Kubernetes API server if:
- Many SecretProviderClass resources exist
- Rapid updates trigger many reconciliations
- Multiple controllers running (namespace-scoped deployment)

**Recommendation:** Explicitly configure QPS limits:

```go
config.QPS = 10.0  // Max 10 queries per second
config.Burst = 15  // Allow burst of 15 simultaneous requests

dynamicClient, err := dynamic.NewForConfig(config)
```

### 2. No Azure API Rate Limiting ⚠️ HIGH

**Current State:** No rate limiting on Azure Key Vault API calls

**Azure Key Vault Limits:**
- **Secrets operations:** 2000 requests per 10 seconds per vault
- **Certificate operations:** 200 requests per 10 seconds per vault
- **Throttling response:** HTTP 429 with `Retry-After` header

**Problem:** Controller can hit Azure rate limits if:
- Vault has hundreds of secrets/certificates
- Multiple controllers sync same vault
- Rapid reconciliation loops

**Current Behavior on Throttle:**
- No special handling for 429 responses
- Treats as regular error → retry with backoff
- Ignores `Retry-After` header

**Recommendation:** Implement Azure-aware rate limiting with circuit breaker

### 3. No Per-Resource Rate Limiting ⚠️ MEDIUM

**Problem:** Single misbehaving resource can spam work queue

**Scenario:**
```
Resource A updates every second (annotation changes, etc.)
  ↓
Event stream generates constant MODIFIED events
  ↓
Work queue constantly re-enqueues resource A
  ↓
Workers spend all time on resource A
  ↓
Other resources starved of reconciliation
```

**Current Mitigation:** Work queue deduplication helps, but not perfect

**Recommendation:** Per-resource cooldown period

### 4. No Resource Count Limits ⚠️ MEDIUM

**Problem:** No limit on number of sync-enabled SecretProviderClasses

**Attack Scenario:**
```
Malicious user creates 1000 SecretProviderClasses with sync enabled
  ↓
Controller tries to sync all 1000
  ↓
Each sync = K8s API calls + Azure API calls
  ↓
Controller overwhelmed, cluster API server stressed
```

**Recommendation:** Admission webhook to limit sync-enabled resources

### 5. No Circuit Breaker for Azure ⚠️ HIGH

**Problem:** Continued Azure API calls even when throttled

**Better Approach:**
```
Detect 429 responses
  ↓
Open circuit breaker (stop Azure calls for period)
  ↓
Return to half-open state (test with single request)
  ↓
Close circuit if successful
```

---

## Potential Spam Scenarios

### Scenario 1: Reconciliation Loop

**Trigger:** Controller modifies resource → Kubernetes fires MODIFIED event → Controller reconciles → modifies resource → loop

**Example:**
```go
// BAD: Controller updates resource every reconciliation
func (ctrl *Controller) reconcile(obj *unstructured.Unstructured) error {
    // ... do work ...

    // Always updates, even if nothing changed
    obj.SetAnnotations(map[string]string{
        "last-sync": time.Now().String(),  // Changes every time!
    })
    ctrl.client.Update(ctx, obj)  // Triggers new MODIFIED event
}
```

**Current Protection:** ✅ Our controller only updates when objects actually change

```go
// GOOD: Only update if different
if CompareObjects(obj, discovered) {
    slog.Debug("Objects unchanged, skipping update")
    return nil  // No update = no new event
}
```

### Scenario 2: Mass Resource Creation

**Trigger:** User/automation creates many sync-enabled resources rapidly

```bash
# Attack: Create 500 SecretProviderClasses at once
for i in {1..500}; do
  kubectl apply -f - <<EOF
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: spam-$i
  annotations:
    azure-keyvault-sync/enabled: "true"
    azure-keyvault-sync/service-account: "app-sa"
...
EOF
done
```

**Impact:**
- 500 ADDED events → 500 reconciliations queued
- 500 x (K8s TokenRequest + Azure Token Exchange + Azure Vault List + K8s Update)
- Potentially thousands of API calls

**Current Protection:**
- ✅ Work queue deduplication (prevents duplicate work)
- ✅ Worker limit (max 5 concurrent)
- ⚠️ No admission control to prevent creation

### Scenario 3: Flapping Resource

**Trigger:** External process constantly updates resource

```bash
# Spam: Update annotation every second
while true; do
  kubectl annotate secretproviderclass example \
    timestamp=$(date +%s) --overwrite
  sleep 1
done
```

**Impact:**
- Constant MODIFIED events
- Constant reconciliations
- Work queue always has this resource

**Current Protection:**
- ✅ Work queue deduplication (coalesces rapid updates)
- ⚠️ No cooldown period per resource

### Scenario 4: Azure Throttling Cascade

**Trigger:** Multiple controllers access same vault

```
Controller 1 (namespace A) → Lists vault secrets
Controller 2 (namespace B) → Lists vault secrets
Controller 3 (namespace C) → Lists vault secrets
...
Controller 20 (namespace T) → Hit Azure rate limit (429)
```

**Impact:**
- Azure returns 429 to controllers 20+
- Controllers retry → more 429s
- Cascade of failures
- Legitimate operations blocked

**Current Protection:**
- ✅ Token caching reduces auth overhead
- ⚠️ No circuit breaker
- ⚠️ No 429-aware retry logic

### Scenario 5: Kubernetes API Overload

**Trigger:** Many namespace-scoped controllers running

```
Namespace 1: Controller watching 50 resources
Namespace 2: Controller watching 50 resources
...
Namespace 20: Controller watching 50 resources
= 20 controllers * 50 resources = 1000 watch connections
```

**Impact:**
- Each controller has watch connection
- Each generates API calls
- API server under load

**Current Protection:**
- ✅ Default K8s client QPS limits
- ✅ Event-driven (not polling)
- ⚠️ No cluster-wide coordination

---

## Recommended Rate Limiting Enhancements

### Priority 1: Kubernetes API Client Limits (CRITICAL)

**Add explicit QPS configuration:**

```go
// In main.go
func main() {
    // ... existing code ...

    config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
    if err != nil {
        slog.Error("Error building kubeconfig", "error", err)
        os.Exit(1)
    }

    // NEW: Set explicit rate limits
    config.QPS = 10.0   // 10 queries per second
    config.Burst = 20   // Allow burst of 20

    dynamicClient, err := dynamic.NewForConfig(config)
    // ... rest of code ...
}
```

**Configuration via environment:**

```go
// In config.go
type Config struct {
    // ... existing fields ...

    // Kubernetes API rate limiting
    KubernetesQPS   float32
    KubernetesBurst int
}

func LoadConfig() (*Config, error) {
    cfg := &Config{
        // ... existing defaults ...

        KubernetesQPS:   parseFloat32Env("KUBERNETES_QPS", 10.0),
        KubernetesBurst: parseIntEnv("KUBERNETES_BURST", 20),
    }
    // ...
}
```

**Why this matters:**
- Prevents controller from overwhelming API server
- Explicit control over resource usage
- Tunable per deployment size

### Priority 2: Azure Rate Limit Handling (HIGH)

**Implement 429 detection and retry-after:**

```go
// In vault.go
func ListSecrets(ctx context.Context, vaultName string, ...) ([]string, error) {
    // ... existing setup ...

    for pager.More() {
        page, err := pager.NextPage(ctx)
        if err != nil {
            // NEW: Check for throttling
            if isAzureThrottled(err) {
                retryAfter := extractRetryAfter(err)
                slog.Warn("Azure throttled request",
                    "vault", vaultName,
                    "retryAfter", retryAfter)

                time.Sleep(retryAfter)
                continue  // Retry after waiting
            }

            return nil, fmt.Errorf("failed to list secrets: %w", err)
        }
        // ... process page ...
    }
}

func isAzureThrottled(err error) bool {
    // Check for 429 status code in Azure error
    // Implementation depends on Azure SDK error types
    return strings.Contains(err.Error(), "429") ||
           strings.Contains(err.Error(), "TooManyRequests")
}

func extractRetryAfter(err error) time.Duration {
    // Parse Retry-After header from error
    // Default to 10 seconds if not present
    return 10 * time.Second
}
```

### Priority 3: Per-Resource Cooldown (MEDIUM)

**Add cooldown tracking:**

```go
// In controller.go
type Controller struct {
    // ... existing fields ...

    lastReconcile map[QueueKey]time.Time
    cooldownMu    sync.RWMutex
}

const reconcileCooldown = 30 * time.Second

func (ctrl *Controller) shouldReconcile(key QueueKey) bool {
    ctrl.cooldownMu.RLock()
    defer ctrl.cooldownMu.RUnlock()

    lastTime, exists := ctrl.lastReconcile[key]
    if !exists {
        return true
    }

    elapsed := time.Since(lastTime)
    if elapsed < reconcileCooldown {
        slog.Debug("Resource in cooldown",
            "key", key,
            "elapsed", elapsed,
            "cooldown", reconcileCooldown)
        return false
    }

    return true
}

func (ctrl *Controller) recordReconcile(key QueueKey) {
    ctrl.cooldownMu.Lock()
    defer ctrl.cooldownMu.Unlock()
    ctrl.lastReconcile[key] = time.Now()
}
```

**Add to reconcile flow:**

```go
func (ctrl *Controller) reconcile(namespace, name string) error {
    key := keyFor(namespace, name)

    // NEW: Check cooldown
    if !ctrl.shouldReconcile(key) {
        ctrl.queue.AddAfter(key, reconcileCooldown)
        return nil
    }

    // ... existing reconciliation logic ...

    // NEW: Record reconciliation time
    ctrl.recordReconcile(key)
    return nil
}
```

### Priority 4: Circuit Breaker for Azure (HIGH)

**Implement circuit breaker pattern:**

```go
// circuit_breaker.go
package main

import (
    "sync"
    "time"
)

type CircuitBreaker struct {
    maxFailures   int
    resetTimeout  time.Duration

    failures      int
    lastFailTime  time.Time
    state         string // "closed", "open", "half-open"
    mu            sync.RWMutex
}

func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
    return &CircuitBreaker{
        maxFailures:  maxFailures,
        resetTimeout: resetTimeout,
        state:        "closed",
    }
}

func (cb *CircuitBreaker) Call(fn func() error) error {
    cb.mu.Lock()
    defer cb.mu.Unlock()

    // Check if circuit is open
    if cb.state == "open" {
        if time.Since(cb.lastFailTime) > cb.resetTimeout {
            cb.state = "half-open"
            cb.failures = 0
        } else {
            return fmt.Errorf("circuit breaker is open")
        }
    }

    // Execute function
    err := fn()

    if err != nil {
        cb.failures++
        cb.lastFailTime = time.Now()

        if cb.failures >= cb.maxFailures {
            cb.state = "open"
        }
        return err
    }

    // Success - reset circuit
    if cb.state == "half-open" {
        cb.state = "closed"
    }
    cb.failures = 0
    return nil
}
```

**Use in Azure calls:**

```go
// In controller.go
type Controller struct {
    // ... existing fields ...

    azureCircuitBreaker *CircuitBreaker
}

func NewController(...) *Controller {
    return &Controller{
        // ... existing fields ...

        azureCircuitBreaker: NewCircuitBreaker(5, 1*time.Minute),
    }
}

// In reconcile:
err = ctrl.azureCircuitBreaker.Call(func() error {
    return ListSecrets(ctx, vaultName, token, expiration)
})
if err != nil {
    if err.Error() == "circuit breaker is open" {
        slog.Warn("Azure circuit breaker open, skipping vault call")
        return nil  // Skip for now, will retry later
    }
    return err
}
```

### Priority 5: Admission Webhook for Resource Limits (MEDIUM)

**ValidatingWebhook to limit sync-enabled resources:**

```yaml
# webhook.yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: azure-keyvault-sync-validator
webhooks:
  - name: secretproviderclass.validator.azure-keyvault-sync
    rules:
      - apiGroups: ["secrets-store.csi.x-k8s.io"]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["secretproviderclasses"]
    clientConfig:
      service:
        name: azure-keyvault-sync-webhook
        namespace: kube-system
        path: /validate
    admissionReviewVersions: ["v1"]
    sideEffects: None
```

**Webhook logic:**

```go
func validateSecretProviderClass(spc *SecretProviderClass) error {
    // Check if sync is enabled
    if spc.Annotations["azure-keyvault-sync/enabled"] != "true" {
        return nil  // Not managed by controller
    }

    // Count existing sync-enabled resources in namespace
    count := countSyncEnabled(spc.Namespace)

    const maxPerNamespace = 50
    if count >= maxPerNamespace {
        return fmt.Errorf(
            "namespace %s already has %d sync-enabled SecretProviderClasses (max: %d)",
            spc.Namespace, count, maxPerNamespace)
    }

    return nil
}
```

---

## Configuration Summary

### Current Configurable Limits

| Parameter | Default | Range | Purpose |
|-----------|---------|-------|---------|
| `SYNC_INTERVAL` | 5m | ≥30s | Periodic resync frequency |
| `WORKER_COUNT` | 5 | 1-100 | Concurrent reconciliations |

### Recommended New Configuration

| Parameter | Default | Range | Purpose |
|-----------|---------|-------|---------|
| `KUBERNETES_QPS` | 10.0 | 1.0-50.0 | K8s API queries per second |
| `KUBERNETES_BURST` | 20 | 5-100 | K8s API burst limit |
| `AZURE_CIRCUIT_BREAKER_THRESHOLD` | 5 | 3-10 | Failures before opening circuit |
| `AZURE_CIRCUIT_BREAKER_TIMEOUT` | 1m | 30s-5m | Time before retry after open |
| `RECONCILE_COOLDOWN` | 30s | 10s-5m | Min time between reconciliations |
| `MAX_SYNC_RESOURCES_PER_NS` | 50 | 10-500 | Max sync-enabled resources per namespace |

---

## Testing Rate Limits

### Test 1: Kubernetes API QPS Limit

```bash
# Create many resources rapidly
for i in {1..100}; do
  kubectl apply -f resource-$i.yaml &
done

# Monitor API server logs for rate limiting
kubectl logs -n kube-system kube-apiserver-* | grep -i "rate limit"
```

### Test 2: Azure Throttling

```bash
# Set very low sync interval
export SYNC_INTERVAL=5s  # Aggressive

# Monitor for 429 errors
kubectl logs -n production -l app=azure-keyvault-sync-controller | grep -i "429\|throttl"
```

### Test 3: Work Queue Behavior

```bash
# Watch work queue metrics (would need metrics endpoint)
curl localhost:8080/metrics | grep workqueue
```

---

## Operational Best Practices

### 1. Set Conservative Defaults

```yaml
env:
  - name: SYNC_INTERVAL
    value: "5m"  # Not too aggressive
  - name: WORKER_COUNT
    value: "5"   # Reasonable concurrency
  - name: KUBERNETES_QPS
    value: "10"  # Conservative
```

### 2. Monitor Rate Limiting Events

```bash
# Watch for rate limit logs
kubectl logs -f -l app=azure-keyvault-sync-controller | grep -i "rate\|limit\|throttle\|cooldown"
```

### 3. Resource Quotas

```yaml
# Limit sync-enabled resources per namespace
apiVersion: v1
kind: ResourceQuota
metadata:
  name: secretproviderclass-quota
  namespace: production
spec:
  hard:
    count/secretproviderclasses.secrets-store.csi.x-k8s.io: "50"
```

### 4. Alerts

Set up alerts for:
- High reconciliation failure rate
- Azure 429 responses
- Work queue depth growing
- Long reconciliation times

---

## Conclusion

### Current State: GOOD

✅ Work queue rate limiting
✅ Concurrent worker limits
✅ Periodic resync limits
✅ Max retry limits
✅ Token caching

### Gaps to Address:

⚠️ **Priority 1 (Critical):** Kubernetes API client QPS limits
⚠️ **Priority 2 (High):** Azure 429 handling and circuit breaker
⚠️ **Priority 3 (Medium):** Per-resource cooldown
⚠️ **Priority 4 (Medium):** Admission webhook for resource count limits

### Recommendation

Implement Priority 1 and 2 before production deployment. Priority 3 and 4 can be added as operational experience grows.

The current implementation is solid but would benefit from explicit Kubernetes API limits and Azure-aware rate limiting to prevent cluster spam scenarios.
