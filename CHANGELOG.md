# Development Log

## 2025-10-28

### Release v1.3.2: Critical Kubernetes Deployment Fix

**PR:** #29
**Branch:** `debug/bugfix` → `staging` → `main`

**Critical Bug Fix:**
- Fixed in-cluster Kubernetes configuration authentication
- Application was hardcoded to use local `~/.kube/config` file
- Would crash immediately with "Unable to find home directory" when deployed as a pod
- Now properly uses ServiceAccount token for in-cluster authentication

**Changes:**
- `main.go`: Attempt in-cluster config first, fall back to kubeconfig for local dev
- Added logging to indicate which configuration method is being used
- Maintains backward compatibility for local development

**Impact:**
- Controller can now properly run in Kubernetes pods
- Uses ServiceAccount token at `/var/run/secrets/kubernetes.io/serviceaccount/token`
- Works with RBAC permissions defined in `deploy/rbac.yaml`

## 2025-10-24

### Learning Session: Kubernetes Watch Mechanisms

Started with a learning-focused approach to understand Kubernetes controller patterns:

**Initial Exploration:**
- Created simple pod-watching controller to understand watch API
- Learned about watch event types: ADDED, MODIFIED, DELETED, ERROR
- Understood reconnection patterns and event handling
- Cleaned up AI-generated markers (emojis, overly enthusiastic language)

### Project Initialization

**Repository Setup:**
- Created GitHub repository: `jeanhaley32/azure-keyvault-sync-controller`
- Added MIT License
- Created initial README documenting project goals and architecture
- Initialized Go module with k8s.io dependencies

**First Controller Implementation:**
- Switched from pod watching to SecretProviderClass resources
- Implemented Kubernetes Dynamic Client for custom resource watching
- Created thread-safe cache with `sync.RWMutex` for concurrent access
- Added automatic reconnection on watch failures
- Implemented periodic 5-minute resync loop to catch missed events
- Basic event logging for ADDED, MODIFIED, DELETED events

**Key Learning:**
- Kubernetes watch connections can close unexpectedly, need reconnection logic
- Dynamic client allows watching any resource without generated code
- Cache must be thread-safe for concurrent watch and resync operations
- Incremental testing after each change ensures functionality preservation

## 2025-10-25

### Phase 2.1: Kubernetes Token Acquisition - ✅ COMPLETE
**Branch:** `token-acquisition`
**PR:** #4

Implemented real Kubernetes TokenRequest API integration for service account token acquisition.

**New Files:**
- `token.go`: Token cache and acquisition logic (136 lines)
  - TokenCache with thread-safe operations
  - Real TokenRequest API implementation
  - Token renewal at 80% of 3600s lifetime (48 minutes)
  - ExtractClientID() from spec.parameters.clientID
- `deploy/rbac.yaml`: RBAC permissions including serviceaccounts/token
- `deploy/deployment.yaml`: Deployment manifest template
- `planning/token-acquisition.md`: Research and planning (330 lines)
- `planning/token-acquisition-implementation.md`: Implementation guide (580 lines)

**Modified Files:**
- `controller.go`: Integrated token acquisition into syncCache()
- `main.go`: Added kubernetes clientset initialization
- `go.mod/go.sum`: Added k8s.io/api v0.34.1 for authentication/v1

**Token Configuration:**
- Expiration: 3600 seconds (1 hour) - Azure Workload Identity standard
- Renewal: 80% of lifetime - matches Kubernetes kubelet behavior
- Audience: api://AzureADTokenExchange - required by Azure federation

**Testing Results:**
- Successfully obtained real Kubernetes JWT tokens
- Verified token format and claims (aud, sub, iss, exp)
- Token expiration exactly 1 hour from issuance
- Cache working with renewal logic
- ClientID extraction from SecretProviderClass spec
- Tested against staging AKS cluster

**Security:**
- Tokens logged as snippets only (first 5 + last 5 chars)
- Service account impersonation (no centralized credentials)
- RBAC permissions defined for deployment

**Next:** Phase 2.2 - Azure AD Token Exchange

## 2025-10-26

### Phase 2.2: Azure AD Token Exchange - ✅ COMPLETE
**Branch:** `azure-token-exchange`
**PR:** #5

Implemented Azure AD token exchange using Azure Workload Identity federation to trade Kubernetes JWT tokens for Azure AD access tokens.

**New Files:**
- `azure.go`: Azure AD token cache and exchange logic (186 lines)
  - AzureTokenCache with thread-safe operations
  - Real WorkloadIdentityCredential implementation
  - Token renewal at 80% of token lifetime
  - ExtractTenantID() from spec.parameters.tenantId
  - Secure temporary file handling (0600 permissions)
- `planning/azure-token-exchange.md`: Research and planning (577 lines)
  - Azure Workload Identity mechanism research
  - Token scope architecture analysis
  - Multi-vault support strategy
  - Security considerations

**Modified Files:**
- `controller.go`: Integrated Azure token acquisition into syncCache()
  - Added azureTokenCache field to Controller
  - Extract tenantID from SecretProviderClass
  - Acquire Azure AD token after K8s token
  - Log token snippets for verification
- `go.mod/go.sum`: Added Azure SDK dependencies
  - github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.13.0
  - github.com/Azure/azure-sdk-for-go/sdk/azcore v1.19.1

**Token Exchange Flow:**
1. Write K8s JWT to secure temporary file (0600 permissions)
2. Set environment variables (AZURE_FEDERATED_TOKEN_FILE, AZURE_CLIENT_ID, AZURE_TENANT_ID)
3. Create WorkloadIdentityCredential
4. Call GetToken with scope: https://vault.azure.net/.default
5. Receive Azure AD access token
6. Cache by namespace/serviceAccount
7. Clean up temporary file

**Token Configuration:**
- Scope: https://vault.azure.net/.default (service-level, not vault-specific)
- Expiration: 28 hours (Azure-configured lifetime)
- Renewal: 80% of lifetime (22.4 hours)
- Cache key: namespace/serviceAccount

**Testing Results:**
- Successfully exchanged K8s JWT for real Azure AD token
- Tested against staging AKS cluster with real federated identity
- Verified token format and claims
- Token caching and renewal logic working
- Temporary file cleanup verified

**Multi-Vault Architecture:**
- Service-level token scope allows one token to access multiple vaults
- Each SecretProviderClass can target different vault
- Token reused across vaults for same service account
- Scalable to 100+ vaults efficiently

**Security:**
- Tokens logged as snippets only (first 10 + last 10 chars)
- Temporary files with restrictive permissions (0600)
- Automatic cleanup with defer
- Service account impersonation maintained
- No centralized credentials

**Next:** Phase 4 - SecretProviderClass Updates

### Phase 3: Azure Key Vault Integration - ✅ COMPLETE
**Branch:** `keyvault-integration`
**PR:** #6

Implemented Azure Key Vault integration to list secrets and certificates using Azure AD tokens from Phase 2.2.

**New Files:**
- `vault.go`: Key Vault client infrastructure (153 lines)
  - CachedTokenCredential wrapper implementing azcore.TokenCredential
  - ListSecrets() function with pagination support
  - ListCertificates() function with pagination support
  - ExtractKeyvaultName() helper function
  - Filters for enabled items only (skip disabled secrets/certs)
- `planning/keyvault-integration.md`: Comprehensive planning document (511 lines)
  - Azure Key Vault SDK research findings
  - Architecture decisions and token credential wrapper design
  - Implementation approach with error handling strategy
  - Testing strategy and success criteria

**Modified Files:**
- `azure.go`: Modified GetToken() to return expiration time
  - Changed signature to return (string, time.Time, error)
  - Returns both token and expiration for vault client usage
- `controller.go`: Integrated vault operations into syncCache()
  - Extract keyvaultName from SecretProviderClass spec
  - Call ListSecrets() with Azure AD token and expiration
  - Call ListCertificates() with Azure AD token and expiration
  - Comprehensive logging of discovered vault contents
  - Error handling continues processing on vault failures
- `go.mod/go.sum`: Added Azure Key Vault SDK dependencies
  - github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets v1.4.0
  - github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates v1.4.0

**Key Features:**
- Reuses Azure AD tokens from Phase 2.2 (no additional token acquisition)
- Custom token credential wrapper integrates seamlessly with Azure SDK
- Pagination support for vaults with hundreds of secrets/certificates
- Filters enabled-only secrets and certificates automatically
- Best-effort error handling (vault failures don't stop sync)
- Comprehensive logging for debugging and verification
- RBAC-aware (gracefully handles 403 Forbidden errors)

**Testing Results:**
- Successfully connected to staging vault: `staging-flow-vault.vault.azure.net`
- Discovered 3 secrets: `azure-flow-api-secret`, `flow-api-secret`, `testing-secret`
- Token caching verified working (28-hour Azure AD token lifetime)
- Periodic resync working with cached tokens
- No crashes or errors during testing
- End-to-end authentication chain validated (K8s → Azure AD → Key Vault)

**Architecture Validated:**
- Service account impersonation: `aks-staging-flow`
- Token reuse across multiple syncs
- Multi-vault support ready (each SecretProviderClass targets different vault)
- RBAC permissions verified (Key Vault Secrets User role working)

**Security:**
- Tokens remain cached from Phase 2.2 with proper lifecycle
- No additional credential storage required
- Vault operations use least-privilege RBAC roles
- Audit trail maintained (vault logs show correct service identity)

**Next:** Phase 4 - SecretProviderClass Updates (automatically populate objects array)

### Refactor: File Structure Organization
**Branch:** `refactor-file-structure`
**PR:** #3

Split monolithic main.go (256 lines) into three focused files:
- `cache.go`: SecretProviderClassCache implementation (52 lines)
- `controller.go`: Controller logic and event handlers (227 lines)
- `main.go`: Application initialization only (36 lines)

Improves code maintainability and follows Go best practices for package organization.

### Feature: Service Account Discovery
**Branch:** `service-account-discovery`
**PR:** #2

Added service account annotation support for Azure Workload Identity integration:
- New annotation: `azure-keyvault-sync/service-account`
- Objects must have both `enabled: "true"` and `service-account` annotations to be managed
- Implemented `isValidForSync()` validation helper
- Extracted event handlers into separate methods:
  - `handleAdded()` - Process new SecretProviderClass objects
  - `handleModified()` - Handle annotation lifecycle changes
  - `handleDeleted()` - Remove objects from cache

**Security Model:**
Controller will use service account impersonation to access Azure Key Vault with each service's existing permissions, maintaining audit attribution and reducing blast radius.

### Feature: Annotation-Based Opt-In
**Branch:** `annotation-support`
**PR:** #1

Implemented opt-in filtering for controller management:
- Annotation: `azure-keyvault-sync/enabled: "true"`
- Controller only manages SecretProviderClass objects with this annotation
- Dynamic annotation lifecycle handling (add/remove/modify)
- Objects are added/removed from cache based on annotation state

### Initial Implementation

**Core Functionality:**
- Kubernetes Dynamic Client integration
- SecretProviderClass resource watching across all namespaces
- Thread-safe in-memory cache with `sync.RWMutex`
- Automatic reconnection on watch failures
- Periodic 5-minute resync to catch missed events
- Event handling for ADDED, MODIFIED, DELETED, ERROR events

**Project Setup:**
- MIT License
- Go module: `github.com/jeanhaley32/azure-keyvault-sync-controller`
- Dependencies: k8s.io/client-go, k8s.io/apimachinery

## 2025-10-27

### Phase 4: SecretProviderClass Updates - ✅ COMPLETE
**Branch:** `secretproviderclass-updates`
**PR:** #8

Implemented automatic SecretProviderClass object and secretObjects array population from Azure Key Vault contents.

**New Files:**
- `update.go`: SecretProviderClass patching logic (384 lines)
  - GenerateObjectsFromVault() - Creates objects array from vault secrets/certs
  - GenerateSecretObjectsFromVault() - Creates secretObjects for K8s Secrets
  - PatchSecretProviderClass() - JSON Patch updates with change detection
  - CompareObjects/CompareSecretObjects() - Deep comparison for change detection
- `planning/secretproviderclass-updates.md`: Implementation planning (462 lines)
  - Vault as source of truth design decision
  - JSON Patch strategy with change detection
  - secretObjects generation bonus feature

**Modified Files:**
- `controller.go`: Integrated patching into syncCache()
  - Calls GenerateObjectsFromVault() with discovered vault contents
  - Calls GenerateSecretObjectsFromVault() based on annotations
  - Applies JSON Patch to update SecretProviderClass
  - Adds last-sync timestamp annotation

**Key Features:**
- Vault is single source of truth (no manual object preservation)
- Automatic objects array population from all enabled secrets/certs
- Optional secretObjects generation (Opaque for secrets, TLS for certs)
- JSON Patch updates with RFC 6902 compliance
- Change detection prevents unnecessary updates
- Field removal when annotations disabled
- Last-sync timestamp tracking

**Annotation Support:**
- `azure-keyvault-sync/secret-objects: "true"` - Enable K8s Secret creation
- `azure-keyvault-sync/cert-objects: "true"` - Enable TLS Secret creation
- `azure-keyvault-sync/last-sync` - Auto-populated timestamp

**Testing Results:**
- Successfully patched SecretProviderClass with vault contents
- Change detection working (skips updates when no changes)
- secretObjects generation validated (Opaque + TLS types)
- No update loops or race conditions
- Field removal verified when annotations removed

**Next:** Architecture improvements for production readiness

### Architecture Improvements: Work Queue Pattern - ✅ COMPLETE
**Branch:** `architecture-improvements`
**PR:** #9

Implemented industry-standard Kubernetes controller work queue pattern for improved reliability and performance.

**Modified Files:**
- `controller.go`: Complete refactor with work queue architecture (446 lines)
  - Added workqueue.RateLimitingInterface with exponential backoff
  - Implemented 5 concurrent worker goroutines
  - Event handlers now enqueue items instead of direct processing
  - Added reconcileResource() for single-resource processing
  - Retry logic with max 5 attempts before dropping
  - Graceful error handling (failed resources don't block others)

**Key Features:**
- Immediate event-driven reconciliation (no 5-minute wait)
- Automatic event deduplication (multiple events → single reconciliation)
- Rate limiting (max 5 concurrent reconciliations)
- Retry logic with exponential backoff
- Graceful degradation (permission errors don't block other resources)
- No race conditions or reconciliation loops

**Architecture Benefits:**
- Separation of event watching from reconciliation processing
- Work queue provides natural deduplication
- Rate limiting prevents API server overload
- Retry logic handles transient failures
- Each resource processed independently

**Testing Results:**
- Event deduplication verified (3 events → 1 reconciliation)
- Concurrent processing working (5 workers)
- Retry logic validated (5 attempts on errors)
- Failed resources don't block others
- No crashes or race conditions during testing

**Planning Documentation:**
- `planning/architecture-improvements.md`: Comprehensive analysis (385 lines)
- `planning/workflow-blindspot-fixes.md`: Vault as source of truth validation

### Critical Bug Fix: 403 Forbidden Error Handling
**Branch:** `main`
**Commit:** 3a34a96

Fixed critical bug where Azure Key Vault permission errors (403 Forbidden) would continue reconciliation with empty secrets/certs lists, effectively clearing the SecretProviderClass objects.

**Fix Applied:**
- Modified vault error handling in controller.go (lines 268-290)
- ListSecrets() errors now fail reconciliation instead of continuing with nil
- ListCertificates() errors now fail reconciliation instead of continuing with nil
- Triggers retry logic (5 attempts with exponential backoff)
- After max retries, item dropped from queue while preserving existing objects

**Impact:**
- Vault permission errors no longer clear SecretProviderClass data
- Existing objects preserved during permission failures
- Retry logic gives time for RBAC issues to be resolved

### Deployment Infrastructure - ✅ COMPLETE
**Branch:** `main`

Created complete deployment infrastructure for container image distribution and automated builds.

**New Files:**
- `Dockerfile`: Multi-stage build with Go 1.25 and distroless runtime
- `.dockerignore`: Build context optimization
- `Makefile`: Build automation (build, docker-build, docker-push, deploy)
- `.github/workflows/build-and-push.yaml`: GitHub Actions CI/CD
- `examples/basic-sync.yaml`: Minimal configuration example
- `examples/with-secrets.yaml`: With secretObjects generation
- `examples/full-example.yaml`: Complete example with ServiceAccount and Pod
- `examples/README.md`: Usage guide and troubleshooting

**Modified Files:**
- `deploy/deployment.yaml`: Updated image to ghcr.io/jeanhaley32/azure-keyvault-sync-controller:latest
- `README.md`: Added Container Images section and CI/CD documentation
- `ROADMAP.md`: Marked Phase 4 complete

**Key Features:**
- Multi-arch builds (linux/amd64, linux/arm64)
- Automated builds on push to main
- GitHub Container Registry (GHCR) hosting
- Distroless runtime for minimal attack surface
- Non-root user (UID 65532)
- Makefile for development workflow

**Image Repository:**
- `ghcr.io/jeanhaley32/azure-keyvault-sync-controller:latest`
- Public repository, no authentication required
- Automated builds via GitHub Actions

## Current Status

**✅ Production Ready**

All planned phases (1-4) complete with production-grade features:
- Work queue architecture with retry logic
- Automatic vault synchronization
- Kubernetes Secret generation
- Comprehensive error handling
- Container images available on GHCR
- Complete documentation and examples

See [ROADMAP.md](ROADMAP.md) for detailed implementation history and future enhancements.
