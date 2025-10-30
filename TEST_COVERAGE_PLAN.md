# Test Coverage Improvement Plan

> Created: 2025-10-30
> Current Overall Coverage: 27.5%
> Target Coverage: 70%+

## Executive Summary

This plan outlines a systematic approach to improve test coverage across all packages, prioritized by criticality and business impact. We'll focus on the highest-risk areas first: controller orchestration logic, Azure integration, and token management.

---

## Phase 1: Controller Package (CRITICAL) - Current: 2.4%

**Target Coverage: 70%+**
**Estimated Effort: 3-4 days**
**Impact: Highest - Core business logic**

### 1.1 Test Infrastructure Setup
- [ ] Create mock Kubernetes clientset
- [ ] Create mock Azure token cache
- [ ] Create test fixtures for SecretProviderClass resources
- [ ] Setup table-driven test framework for reconciliation scenarios

### 1.2 Event Handler Tests
**Files:** `controller.go:120-166`

- [ ] `handleAdded()` - New SecretProviderClass resources
  - Valid SPC with service-account annotation
  - SPC without required annotations (should be ignored)
  - SPC with invalid keyvault URL
  - SPC with tag filtering enabled/disabled

- [ ] `handleModified()` - Updated resources
  - Annotation changes (service-account, tags)
  - Spec changes (objects, parameters)
  - Status-only changes (should not trigger reconcile)
  - No-op updates (same content)

- [ ] `handleDeleted()` - Removed resources
  - Cleanup verification
  - Cache removal
  - Graceful handling of missing resources

### 1.3 Reconciliation Logic Tests
**Files:** `controller.go:194-528` (reconcileResource - 335 lines!)

- [ ] **Happy Path Tests**
  - Full reconciliation cycle with valid Azure credentials
  - Successful token exchange K8s → Azure
  - Vault secret/certificate retrieval
  - SPC patch with updated objects
  - Cache update verification

- [ ] **Error Handling Tests**
  - Invalid service account → skip reconciliation
  - Missing Azure credentials → error
  - Keyvault not found → error
  - Token exchange failure → retry
  - Azure throttling → backoff
  - Patch conflict → retry

- [ ] **Tag Filtering Tests** (lines 300-400)
  - respect-tags=false → all secrets
  - respect-tags=true → filtered by service/environment tags
  - Case-insensitive tag matching
  - Whitespace handling

- [ ] **Secret Generation Tests** (lines 400-450)
  - Vault secrets with secret-object tag
  - Vault certificates with cert-object tag
  - Mixed secrets and certificates
  - Secrets without object tags (opt-in only)

- [ ] **Change Detection Tests** (lines 450-480)
  - No changes → skip patch
  - Added secrets → patch
  - Removed secrets → patch
  - Modified secret metadata → patch
  - Reordered secrets (same content) → no patch

- [ ] **Circuit Breaker Integration Tests** (lines 250-300)
  - Azure failures increment circuit breaker
  - Circuit open → fast fail without Azure call
  - Circuit half-open → single test call
  - Successful call resets circuit

### 1.4 Worker Pool Tests
**Files:** `controller.go:579-658`

- [ ] `worker()` - Worker lifecycle
  - Worker starts and processes items
  - Worker stops on context cancellation
  - Multiple workers process concurrently
  - Workers respect rate limiting

- [ ] `processNextItem()` - Queue processing
  - Successfully process valid item
  - Handle invalid item gracefully
  - Retry on transient failures
  - Skip on permanent failures

- [ ] **Race Condition Tests**
  - Concurrent reconciliation of same resource
  - Queue operations under load
  - Cache updates from multiple workers

### 1.5 Controller Lifecycle Tests
**Files:** `controller.go:700-730`

- [ ] `Run()` - Main controller loop
  - Informer start and sync
  - Worker pool startup
  - Periodic resync scheduling
  - Graceful shutdown on context cancel
  - Drain queue on shutdown

### 1.6 Cache Synchronization Tests
**Files:** `controller.go:558-577`

- [ ] Initial cache sync from Kubernetes
- Periodic resync behavior
- Cache consistency after events
- Handle watch disconnection

---

## Phase 2: Azure Package (HIGH) - Current: 12.4%

**Target Coverage: 80%+**
**Estimated Effort: 2-3 days**
**Impact: High - External integration reliability**

### 2.1 Token Exchange Tests
**Files:** `azure.go:39-187`

- [ ] `NewAzureTokenCache()` - Initialization
- [ ] `GetToken()` - Token retrieval with caching
  - Cache hit → return cached token
  - Cache miss → exchange token
  - Expired token → refresh
  - Token near expiration (80% threshold) → proactive refresh

- [ ] `exchangeToken()` - K8s token → Azure token
  - Successful token exchange
  - Write K8s token to temp file with 0600 permissions
  - Set environment variables (AZURE_FEDERATED_TOKEN_FILE, CLIENT_ID, TENANT_ID)
  - Handle os.Setenv failures
  - Handle file write failures
  - Cleanup temp file on success
  - Cleanup temp file on failure

- [ ] `IsTokenValid()` - Expiration logic
  - Valid token within threshold
  - Token past renewal threshold
  - Expired token
  - Invalid expiration time

- [ ] `ExtractTenantID()` - Parse service account annotation
  - Valid azure-tenant-id annotation
  - Missing annotation
  - Empty annotation value
  - Invalid format

### 2.2 Error Handling Tests
**Files:** `errors.go:15-87`

- [ ] `IsAzureThrottled()` - Throttling detection
  - Azure 429 status code
  - Non-429 errors
  - Nil error

- [ ] `ExtractRetryAfter()` - Retry header parsing
  - Valid Retry-After header (seconds)
  - Valid Retry-After header (HTTP date)
  - Missing header → default duration
  - Invalid header → default duration

- [ ] `IsAzureAuthError()` - Auth failure detection
  - 401 Unauthorized
  - 403 Forbidden
  - Other status codes
  - Nil error

### 2.3 Vault Operations Tests
**Files:** `vault.go:24-160`

- [ ] `GetToken()` - Vault client authentication
  - Use cached token when valid
  - Refresh token when needed
  - Handle token exchange failures

- [ ] `ExtractKeyvaultName()` - Parse keyvault URL
  - Valid keyvault URL
  - Invalid URL format
  - Missing scheme
  - Empty URL

- [ ] `ListSecrets()` - Secret enumeration
  - Successful secret listing
  - Filter by tags (service, environment)
  - Handle pagination
  - Handle throttling (circuit breaker)
  - Handle auth errors
  - Empty vault

- [ ] `ListCertificates()` - Certificate enumeration
  - Successful certificate listing
  - Filter by tags (service, environment)
  - Handle pagination
  - Handle throttling (circuit breaker)
  - Handle auth errors
  - Empty vault

### 2.4 Tag Filtering Integration Tests
**Files:** `azure.go` + vault operations

- [ ] `MatchesTags()` integration (already has unit tests)
- [ ] End-to-end filtering in ListSecrets/ListCertificates
- [ ] respect-tags annotation behavior
- [ ] Case-insensitive matching
- [ ] Whitespace normalization

---

## Phase 3: Token Package (MEDIUM) - Current: 0%

**Target Coverage: 80%+**
**Estimated Effort: 1-2 days**
**Impact: Medium - K8s authentication reliability**

### 3.1 Token Cache Tests
**Files:** `token.go:38-126`

- [ ] `NewTokenCache()` - Initialization
- [ ] `tokenCacheKey()` - Cache key generation
  - Valid namespace and service account
  - Empty namespace
  - Empty service account

- [ ] `GetToken()` - Token retrieval
  - Cache hit with valid token
  - Cache hit with expired token → refresh
  - Cache miss → request new token
  - Token near expiration → proactive refresh

- [ ] `IsTokenValid()` - Expiration checking
  - Valid token within threshold (80%)
  - Token past threshold
  - Zero expiration time
  - Future expiration

### 3.2 Token Request Tests
**Files:** `token.go:50-77`

- [ ] `requestToken()` - K8s token request
  - Mock successful TokenRequest API call
  - Verify audience parameter
  - Verify expiration seconds (3600)
  - Handle API errors
  - Handle context cancellation
  - Validate returned token format

### 3.3 Service Account Parsing Tests
**Files:** `token.go:127-139`

- [ ] `ExtractClientID()` - Parse azure-client-id annotation
  - Valid annotation present
  - Missing annotation → error
  - Empty annotation value → error
  - Whitespace handling

---

## Phase 4: Health Package (MEDIUM) - Current: 0%

**Target Coverage: 90%+**
**Estimated Effort: 1 day**
**Impact: Medium - Observability and K8s probes**

### 4.1 HealthChecker State Management Tests
**Files:** `health.go:21-80`

- [ ] `NewHealthChecker()` - Initialization
  - Verify initial state (not ready)
  - Verify start time set

- [ ] `SetWatchConnected()` - Watch state
  - Set connected → updates lastWatchUpdate
  - Set disconnected → keeps old timestamp
  - Concurrent access safety

- [ ] `SetWorkersRunning()` - Worker state
  - Set running
  - Set stopped
  - Concurrent access safety

- [ ] `UpdateWatchActivity()` - Activity tracking
  - Updates timestamp on event
  - Concurrent access safety

- [ ] `IsReady()` - Readiness logic
  - Watch connected + workers running = ready
  - Watch disconnected = not ready
  - Workers stopped = not ready
  - Both false = not ready

- [ ] `GetStatus()` - Status map
  - Contains all expected fields
  - watch_connected boolean
  - workers_running boolean
  - uptime_seconds calculated correctly
  - last_watch_update (when available)
  - seconds_since_watch_update (when available)

### 4.2 HTTP Handler Tests
**Files:** `health.go:84-127`

- [ ] `HealthzHandler()` - Liveness probe
  - Returns 200 OK
  - Returns "ok" body
  - Handle write errors gracefully

- [ ] `ReadyzHandler()` - Readiness probe
  - Returns 200 + "ready" when IsReady() = true
  - Returns 503 + JSON status when IsReady() = false
  - JSON contains ready=false and status map
  - Handle write errors gracefully
  - Handle JSON encoding errors gracefully

- [ ] `StatusHandler()` - Status endpoint
  - Returns 200 OK
  - Sets Content-Type: application/json
  - Returns status map + ready field
  - Handle JSON encoding errors gracefully

### 4.3 Server Lifecycle Tests
**Files:** `health.go:130-145`

- [ ] `StartHealthCheckServer()` - Server startup
  - Mock server listening (test setup)
  - Verify routes registered (/healthz, /readyz, /status)
  - Verify timeouts configured
  - Handle bind errors gracefully

---

## Phase 5: Update Package Completion (LOW) - Current: 84.2%

**Target Coverage: 90%+**
**Estimated Effort: 0.5 day**
**Impact: Low - Only one function missing**

### 5.1 Patch Operation Tests
**Files:** `update.go:120-193`

- [ ] `PatchSecretProviderClass()` - Kubernetes patch
  - Mock successful patch
  - Verify patch payload structure
  - Handle patch conflicts (retry logic)
  - Handle API errors
  - Handle context cancellation
  - Verify retry backoff

---

## Phase 6: Logger Package (LOW) - Current: 0%

**Target Coverage: 80%+**
**Estimated Effort: 0.5 day**
**Impact: Low - Simple initialization logic**

### 6.1 Logger Initialization Tests
**Files:** `logger.go:14-60`

- [ ] `InitLogger()` - Setup
  - Text format initialization
  - JSON format initialization
  - Debug level
  - Info level
  - Warn level
  - Error level
  - Invalid format → default to text
  - Invalid level → default to info

- [ ] `getLogLevel()` - Level parsing
  - Valid levels (DEBUG, INFO, WARN, WARNING, ERROR)
  - Case insensitive
  - Invalid level → default
  - Empty string → default

- [ ] `Logger()` - Global accessor
  - Returns configured logger

---

## Test Infrastructure Requirements

### Mocking Libraries
```go
// Already in go.mod or to be added:
- "k8s.io/client-go/kubernetes/fake" // Mock K8s client
- "github.com/stretchr/testify/mock" // Manual mocks
- "github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets/fake" // Mock Azure
```

### Test Fixtures
Create `internal/testutil/fixtures.go`:
- Sample SecretProviderClass resources
- Sample Azure secrets/certificates
- Sample service accounts with annotations
- Sample vault tags for filtering

### Test Helpers
Enhance `internal/testutil/`:
- HTTP test helpers (for health handlers)
- Time manipulation helpers (for token expiration)
- Mock Azure client builder
- Mock K8s clientset builder

---

## Execution Timeline

### Week 1: Controller Package (Phase 1)
- **Days 1-2:** Test infrastructure + event handler tests
- **Days 3-4:** Reconciliation logic tests (core 335 lines)
- **Day 5:** Worker pool + lifecycle tests

### Week 2: Azure + Token Packages (Phases 2-3)
- **Days 1-2:** Azure token exchange + error handling
- **Day 3:** Azure vault operations
- **Day 4:** Token package (K8s token management)
- **Day 5:** Integration tests + cleanup

### Week 3: Health + Final Packages (Phases 4-6)
- **Day 1:** Health package (handlers + state management)
- **Day 2:** Update package completion + logger package
- **Days 3-4:** Integration testing + coverage verification
- **Day 5:** Documentation + PR review

---

## Success Criteria

### Coverage Targets
- [ ] Controller: 70%+ (from 2.4%)
- [ ] Azure: 80%+ (from 12.4%)
- [ ] Token: 80%+ (from 0%)
- [ ] Health: 90%+ (from 0%)
- [ ] Update: 90%+ (from 84.2%)
- [ ] Logger: 80%+ (from 0%)
- [ ] **Overall: 70%+** (from 27.5%)

### Quality Gates
- [ ] All tests pass with `-race` flag
- [ ] No test flakiness (run 10x without failure)
- [ ] Integration tests cover critical paths
- [ ] Mock isolation (no external dependencies)
- [ ] Test execution time < 30 seconds

### Documentation
- [ ] Test README explaining mock setup
- [ ] Examples of adding new tests
- [ ] CI/CD integration instructions
- [ ] Coverage reporting in PR checks

---

## Risk Mitigation

### Challenges & Solutions

**Challenge 1: Controller complexity**
- Solution: Break into smaller test suites per function
- Use table-driven tests for scenario coverage
- Focus on critical paths first

**Challenge 2: Azure SDK mocking**
- Solution: Use Azure's fake client packages where available
- Create interface wrappers for testability
- Mock at interface boundaries

**Challenge 3: Time-dependent tests**
- Solution: Use time.Now() injection via interfaces
- Mock time for token expiration tests
- Use short durations in tests (milliseconds)

**Challenge 4: Concurrency testing**
- Solution: Use `-race` detector consistently
- Stress test with high concurrency (100+ goroutines)
- Test deadlock scenarios with timeouts

---

## Maintenance Plan

### Ongoing Coverage Monitoring
- Add coverage requirements to CI/CD pipeline
- Block PRs that decrease coverage by >1%
- Monthly coverage review meetings
- Quarterly test refactoring sprints

### Test Ownership
- Each package has designated test owner
- New features require tests before merge
- Bug fixes require regression tests
- Refactoring must maintain coverage

---

## Notes

- This plan assumes familiarity with Go testing, mocking, and K8s client-go
- Estimated efforts are for one engineer; adjust for team size
- Integration tests may require local K8s cluster (kind/minikube)
- Azure tests can run without real Azure credentials using mocks
