# Coverage Analysis: Path to 70%

## Current Status: 41.3%

## Target: 70% Overall Coverage

---

## Package Breakdown

| Package | Current | Target | Gap | Priority |
|---------|---------|--------|-----|----------|
| **Controller** | 14.5% | 70% | **+55.5%** | 🔴 CRITICAL |
| **Azure** | 58.1% | 70% | +11.9% | 🟡 MEDIUM |
| **Update** | 84.2% | 90% | +5.8% | 🟢 LOW |
| Health | 78.3% | 90% | +11.7% | 🟢 LOW |
| Token | 97.4% | ✓ | - | ✓ DONE |
| Logger | 100% | ✓ | - | ✓ DONE |
| Cache | 100% | ✓ | - | ✓ DONE |
| CircuitBreaker | 100% | ✓ | - | ✓ DONE |
| Config | 98.5% | ✓ | - | ✓ DONE |
| **Cmd** | 0% | - | - | ⚪ SKIP |
| Testutil | 0% | - | - | ⚪ N/A |

---

## Critical Gap: Controller Package (14.5% → 70%)

### Uncovered Functions (0% coverage):
- `parseKey()` - Simple utility function
- `NewController()` - Constructor
- `handleEvent()` - Event routing
- `enqueueAll()` - Queue population
- `syncCache()` - Cache synchronization
- `startPeriodicResync()` - Background worker

### Partially Covered:
- **`reconcileResource()`** - **7.1%** - 335 lines, MAIN RECONCILIATION LOGIC
  - Lines 194-261: Parameter validation ✅ (tested)
  - Lines 262-528: Token operations, Azure calls ❌ (not tested - requires refactoring)

### Why Controller is Stuck at 14.5%:

The `reconcileResource()` function is **tightly coupled** to external dependencies:

```go
// Direct function calls that can't be mocked:
token, err := ctrl.tokenCache.GetToken(ctx, ctrl.clientset, namespace, sa)
azToken, expiration, err := ctrl.azureTokenCache.GetToken(ctx, namespace, sa, token, clientID, tenantID)
secrets, err := azure.ListSecrets(ctx, keyvaultName, azToken, expiration)
certificates, err := azure.ListCertificates(ctx, keyvaultName, azToken, expiration)
err = update.PatchSecretProviderClass(ctx, ctrl.client, spc, objects, secretObjects)
```

**All of these are embedded dependencies without interfaces.**

---

## What's Needed to Reach 70% Overall Coverage

### Option 1: Quick Wins (Get to ~50-55%)
**Focus on easy wins without refactoring:**

1. **Azure Package** (58% → 75%): +7% overall
   - Test `exchangeToken()` edge cases (currently 58.8%)
   - More error scenarios in `GetToken()` (currently 70.6%)
   - Estimated effort: 2-3 hours

2. **Update Package** (84% → 90%): +2% overall
   - Test `PatchSecretProviderClass()` function (currently 0%)
   - Requires mocking K8s client
   - Estimated effort: 1-2 hours

3. **Controller Simple Functions**: +3% overall
   - `parseKey()`, `NewController()`, `handleEvent()`
   - `enqueueAll()`, `syncCache()`, `startPeriodicResync()`
   - Most are simple and testable
   - Estimated effort: 2-3 hours

**Total Quick Wins: 41% → ~52% (+11%)**
**Total Effort: 5-8 hours**

### Option 2: Refactor for Testability (Get to 70%+)
**Refactor controller for dependency injection:**

This is the ONLY way to reach 70%+ coverage.

#### Required Refactoring:

1. **Extract Interfaces:**
```go
type TokenProvider interface {
    GetK8sToken(ctx context.Context, clientset kubernetes.Interface, namespace, sa string) (string, error)
    GetAzureToken(ctx context.Context, namespace, sa, k8sToken, clientID, tenantID string) (string, time.Time, error)
}

type VaultClient interface {
    ListSecrets(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error)
    ListCertificates(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error)
}

type PatchClient interface {
    PatchSecretProviderClass(ctx context.Context, client dynamic.Interface, spc *unstructured.Unstructured, objects, secretObjects string) error
}
```

2. **Modify Controller:**
```go
type Controller struct {
    // ... existing fields ...
    tokenProvider TokenProvider  // NEW
    vaultClient   VaultClient    // NEW
    patchClient   PatchClient    // NEW
}
```

3. **Create Adapters:**
```go
// Real implementation wraps existing code
type RealTokenProvider struct {
    tokenCache      *token.TokenCache
    azureTokenCache *azure.AzureTokenCache
    clientset       kubernetes.Interface
}

// Mock implementation for testing
type MockTokenProvider struct {
    GetK8sTokenFunc   func(...) (string, error)
    GetAzureTokenFunc func(...) (string, time.Time, error)
}
```

4. **Update Tests:**
```go
func TestReconcileResource(t *testing.T) {
    mockTokenProvider := &MockTokenProvider{
        GetK8sTokenFunc: func(...) (string, error) {
            return "test-k8s-token", nil
        },
        GetAzureTokenFunc: func(...) (string, time.Time, error) {
            return "test-azure-token", time.Now().Add(1*time.Hour), nil
        },
    }

    mockVaultClient := &MockVaultClient{
        ListSecretsFunc: func(...) ([]azure.VaultSecret, error) {
            return []azure.VaultSecret{{Name: "test-secret"}}, nil
        },
    }

    ctrl := &Controller{
        tokenProvider: mockTokenProvider,
        vaultClient:   mockVaultClient,
        // ... other fields ...
    }

    // Now we can test reconcileResource!
    err := ctrl.reconcileResource(ctx, spc)
    assert.NoError(t, err)
}
```

**Estimated Coverage After Refactoring:**
- Controller: 14.5% → 75% (+60.5%)
- Overall: 41.3% → 73% (+31.7%)

**Estimated Effort:**
- Interface extraction: 2-3 hours
- Adapter implementation: 3-4 hours
- Test writing: 4-6 hours
- **Total: 9-13 hours**

---

## Recommendation

### For 70%+ Coverage (Production-Ready):
**Option 2: Refactor for Testability**
- This is the RIGHT way to do it
- Makes the codebase more maintainable
- Enables proper unit testing
- Sets good patterns for future development

### For Quick Progress (50-55%):
**Option 1: Quick Wins**
- Gets you halfway to 70%
- Much faster (5-8 hours vs 9-13 hours)
- But leaves the core reconciliation logic untested
- Technical debt remains

---

## Impact Analysis

### Current State (41.3%):
- ✅ Infrastructure well-tested (cache, config, logger)
- ✅ Support packages solid (token, health)
- ❌ Core business logic poorly tested (controller)
- ❌ Integration points not tested (Azure operations, K8s patching)

### After Quick Wins (52%):
- ✅ All simple functions tested
- ✅ Most packages >80% coverage
- ❌ Main reconciliation loop still untested
- ❌ Can't confidently refactor core logic

### After Refactoring (73%):
- ✅ Core business logic fully tested
- ✅ All integration points mockable
- ✅ Can confidently refactor reconciliation
- ✅ Production-ready test suite

---

## Files That Need Work

### Priority 1 - Controller (for 70%):
- `internal/controller/controller.go` - Lines 262-528 in reconcileResource()
- `internal/controller/controller.go` - NewController, handleEvent, enqueueAll, etc.

### Priority 2 - Azure (for extra coverage):
- `internal/azure/azure.go` - exchangeToken() edge cases
- `internal/azure/vault.go` - Already well-documented as integration test only

### Priority 3 - Update (minor improvement):
- `internal/update/update.go` - PatchSecretProviderClass() function

---

## Conclusion

**To reach 70% overall coverage, you MUST refactor the controller package for testability.**

Quick wins will get you to ~52%, but the gap from 52% → 70% requires tackling the 335-line `reconcileResource()` function, which is impossible to test without dependency injection.

The refactoring is worth it - it will make the codebase more maintainable and set good patterns for future development.
