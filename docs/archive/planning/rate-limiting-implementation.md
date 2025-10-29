# Rate Limiting Implementation

> **Status:** Implemented
> **Branch:** rate-limiting-enhancements
> **Date:** 2025-10-27

## Overview

This document summarizes the implementation of rate limiting enhancements to protect both Kubernetes and Azure APIs from being overwhelmed by the controller.

## Problem Statement

The controller lacked explicit rate limiting mechanisms, which could lead to:
1. Overwhelming the Kubernetes API server with unbounded requests
2. Hitting Azure Key Vault rate limits (2000 req/10sec) without proper handling
3. Cascading failures when Azure throttles requests (429 responses)
4. Poor resource utilization during Azure API failures

## Solution

Implemented two complementary rate limiting mechanisms:

### Phase 1: Kubernetes API Client Rate Limiting

**Implementation:**
- Added QPS (Queries Per Second) and Burst limit configuration
- Applied limits to the Kubernetes dynamic client at initialization
- Defaults: 10 QPS with 20 burst

**Files Modified:**
- `config.go`: Added `KubernetesQPS` and `KubernetesBurst` fields
- `main.go`: Applied rate limits to client configuration
- `deploy/deployment.yaml`: Added environment variable documentation
- `deploy/deployment-namespaced.yaml`: Added environment variable documentation

**Configuration:**
```yaml
env:
  - name: KUBERNETES_QPS
    value: "10.0"  # Range: 0-100
  - name: KUBERNETES_BURST
    value: "20"  # Range: 1-200
```

### Phase 2: Azure Circuit Breaker Pattern

**Implementation:**
- Created circuit breaker to protect against Azure API failures
- Automatically detects Azure throttling (429 responses)
- Extracts and respects Retry-After headers
- Fails fast when circuit is open to avoid wasting resources

**Files Created:**
- `circuit_breaker.go`: CircuitBreaker implementation with states (closed/open/half-open)
- `azure_errors.go`: Helper functions for detecting Azure errors and extracting retry information

**Files Modified:**
- `config.go`: Added circuit breaker configuration fields
- `vault.go`: Added 429 detection and retry logic to ListSecrets and ListCertificates
- `controller.go`: Integrated circuit breaker to wrap all Azure API calls

**Configuration:**
```yaml
env:
  - name: AZURE_CIRCUIT_BREAKER_THRESHOLD
    value: "5"  # Range: 3-10
  - name: AZURE_CIRCUIT_BREAKER_TIMEOUT
    value: "1m"  # Range: 30s-5m
```

## Circuit Breaker States

1. **Closed (Normal Operation)**
   - All requests pass through to Azure API
   - Failure counter tracks consecutive errors
   - Transitions to Open when threshold is exceeded

2. **Open (Fail Fast)**
   - All requests fail immediately without calling Azure
   - Waits for timeout duration before transitioning
   - Logs warning messages for monitoring
   - Allows controller to continue processing other resources

3. **Half-Open (Testing)**
   - Single request allowed to test if Azure recovered
   - Success → transitions back to Closed
   - Failure → transitions back to Open

## Azure 429 Handling

When Azure throttles requests:
1. `IsAzureThrottled()` detects 429 status code in error
2. `ExtractRetryAfter()` parses Retry-After header
3. `vault.go` waits for specified duration
4. Continues paging operation without failing reconciliation
5. Circuit breaker tracks persistent failures

## Testing Results

### Phase 1 Testing
- ✅ Build successful
- ✅ Default values applied correctly (QPS=10, Burst=20)
- ✅ Custom values respected (QPS=5, Burst=10)
- ✅ Validation catches invalid values:
  - QPS=150 → error
  - Burst=250 → error

### Phase 2 Testing
- ✅ Build successful
- ✅ Circuit breaker initialized correctly (Threshold=5, Timeout=1m)
- ✅ Custom values respected (Threshold=3, Timeout=30s)
- ✅ Validation catches invalid values:
  - Threshold=15 → error
  - Timeout=10m → error

## Configuration Summary

| Parameter | Default | Range | Purpose |
|-----------|---------|-------|---------|
| `KUBERNETES_QPS` | 10.0 | 0-100 | K8s API queries per second |
| `KUBERNETES_BURST` | 20 | 1-200 | K8s API burst allowance |
| `AZURE_CIRCUIT_BREAKER_THRESHOLD` | 5 | 3-10 | Failures before opening circuit |
| `AZURE_CIRCUIT_BREAKER_TIMEOUT` | 1m | 30s-5m | Wait time before retry |

## Benefits

1. **Kubernetes Protection**
   - Prevents overwhelming API server
   - Explicit control over resource usage
   - Tunable per deployment size

2. **Azure Protection**
   - Graceful handling of rate limiting
   - Automatic retry with proper backoff
   - Fail-fast during extended outages

3. **Operational Improvements**
   - Clear logging of throttling events
   - Circuit breaker state visible for monitoring
   - Other resources continue processing during failures

4. **Resource Efficiency**
   - Avoids wasted API calls during outages
   - Reduces retry storms
   - Better cluster resource utilization

## Future Enhancements (Not Implemented)

These were identified in the analysis but not implemented in this phase:

1. **Per-Resource Cooldown** (Medium Priority)
   - Track last reconciliation time per resource
   - Prevent rapid reconciliation loops
   - Would require additional state management

2. **Admission Webhook** (Medium Priority)
   - Limit number of sync-enabled resources per namespace
   - Requires webhook server deployment
   - More complex operational overhead

## References

- Analysis: `RATE-LIMITING.md`
- Circuit Breaker Pattern: https://learn.microsoft.com/en-us/azure/architecture/patterns/circuit-breaker
- Azure Key Vault Limits: https://learn.microsoft.com/en-us/azure/key-vault/general/service-limits
