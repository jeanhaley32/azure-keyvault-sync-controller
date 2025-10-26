# Development Log

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

## Next Steps

1. ✅ ~~**Token Acquisition**~~ - COMPLETE (Phase 2.1)
2. **Azure AD Token Exchange** - Trade Kubernetes tokens for Azure AD tokens via Workload Identity federation (Phase 2.2 - NEXT)
3. **Azure Key Vault Integration** - List secrets and certificates from vault (Phase 3)
4. **SecretProviderClass Updates** - Automatically populate objects array with discovered vault contents (Phase 4)

## Architecture

### Current State
- Watch SecretProviderClass resources
- Filter by annotations for opt-in management
- Maintain cache of managed objects
- Track service account associations
- ✅ Acquire Kubernetes tokens via TokenRequest API
- ✅ Cache tokens with automatic renewal (Phase 2.1 COMPLETE)

### Target Architecture
```
Controller → Impersonate ServiceAccount
  → Kubernetes TokenRequest API
  → Azure Workload Identity federation
  → Azure Managed Identity
  → Azure Key Vault RBAC
  → List secrets/certificates
  → Update SecretProviderClass
```

## Branch Status

- `main` - Stable, merged features (includes Phase 2.1)
- `annotation-support` - Merged to main (PR #1)
- `service-account-discovery` - Merged to main (PR #2)
- `refactor-file-structure` - Merged to main (PR #3)
- `token-acquisition` - Merged to main (PR #4, Phase 2.1 COMPLETE)
