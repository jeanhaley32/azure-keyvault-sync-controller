# Project Roadmap

## Overview

This roadmap outlines the development plan for the Azure Key Vault Sync Controller. The controller automates synchronization of Azure Key Vault secrets to Kubernetes SecretProviderClass objects using service account impersonation.

## Phase 1: Foundation (COMPLETED)

**Status:** ✅ Complete

### 1.1 Basic Controller Infrastructure
- [x] Kubernetes Dynamic Client integration
- [x] SecretProviderClass resource watching
- [x] Thread-safe in-memory cache
- [x] Automatic watch reconnection
- [x] Periodic resync (5-minute intervals)
- [x] Event handling (ADDED, MODIFIED, DELETED, ERROR)

### 1.2 Annotation-Based Opt-In
- [x] Annotation support: `azure-keyvault-sync/enabled: "true"`
- [x] Dynamic annotation lifecycle handling
- [x] Cache management based on annotation state

### 1.3 Service Account Discovery
- [x] Annotation support: `azure-keyvault-sync/service-account`
- [x] Validation helpers (isValidForSync)
- [x] Extracted event handlers for clarity
- [x] Service account tracking in cache

### 1.4 Code Organization
- [x] Split main.go into focused files
- [x] cache.go - Cache implementation
- [x] controller.go - Controller logic
- [x] main.go - Application initialization

## Phase 2: Token Acquisition - ✅ COMPLETE

**Status:** ✅ Phase 2.1 Complete, Phase 2.2 Complete

### 2.1 Kubernetes Token Request API - ✅ COMPLETE
- [x] Implement service account impersonation
- [x] Use TokenRequest API to obtain short-lived tokens
- [x] Token expiration and renewal logic
- [x] Error handling for impersonation failures

**Technical Details:**
- Use `k8s.io/client-go/kubernetes` client
- Call `ServiceAccounts().CreateToken()` with TokenRequest
- Set appropriate audience for Azure Workload Identity
- Token TTL: 1 hour (configurable)

**Dependencies:**
- Require RBAC permissions to create tokens for service accounts
- Controller service account needs `serviceaccounts/token` create permission

### 2.2 Azure Workload Identity Integration - ✅ COMPLETE
- [x] Exchange Kubernetes token for Azure AD token
- [x] Configure OIDC federation trust
- [x] Handle token exchange errors
- [x] Token caching and refresh

**Technical Details:**
- Audience: `api://AzureADTokenExchange`
- Use Azure Identity SDK for Go (`azidentity v1.13.0`)
- Federated credential configuration required per service
- Token refresh before expiration (80% of lifetime)
- Scope: `https://vault.azure.net/.default` (service-level)
- Secure temporary file handling for K8s JWT
- WorkloadIdentityCredential for token exchange

**Implementation:**
- Created `azure.go` with AzureTokenCache
- Integrated into controller syncCache loop
- Tested with real AKS cluster and federated identity
- Multi-vault support via service-level token scope

## Phase 3: Azure Key Vault Integration - ✅ COMPLETE

**Status:** ✅ Complete

### 3.1 Key Vault Client Setup
- [x] Initialize Azure Key Vault SDK client
- [x] Use exchanged Azure AD token for authentication
- [x] Handle per-service vault discovery
- [x] Connection pooling and reuse

**Technical Details:**
- Use `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets`
- Use `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates`
- Vault URL format: `https://{keyvaultName}.vault.azure.net`
- Support for multiple vaults per service account

**Implementation:**
- Created `vault.go` with CachedTokenCredential wrapper
- Integrated into controller syncCache loop
- Tested with real staging vault successfully
- Vault URL extracted from SecretProviderClass spec.parameters.keyvaultName

### 3.2 Secret and Certificate Listing
- [x] List all secrets in vault
- [x] List all certificates in vault
- [x] Handle pagination for large vaults
- [x] Filter out disabled/expired items

**Technical Details:**
- Use `NewListSecretPropertiesPager()` for secrets
- Use `NewListCertificatePropertiesPager()` for certificates
- Respect RBAC permissions (read-only access)
- Pagination handled automatically by SDK pager pattern
- Only returns enabled items (checks Attributes.Enabled)

**Testing Results:**
- Successfully listed 3 secrets from staging-flow-vault
- Pagination working (pager.More() and pager.NextPage())
- Disabled items filtered correctly

### 3.3 Error Handling and Resilience
- [x] Handle vault access denied errors
- [x] Graceful degradation on failures
- [x] Metrics and logging for vault operations

**Implementation:**
- RBAC errors (403) logged but don't stop sync
- Network errors logged but don't stop sync
- Each vault processed independently
- Comprehensive logging of discovered contents
- Retry logic deferred to Phase 5 (production hardening)

## Phase 4: SecretProviderClass Updates

**Status:** ✅ Complete

### 4.1 Object Array Generation
- [x] Convert vault secrets to SecretProviderClass objects array
- [x] Convert vault certificates to objects array
- [x] ~~Handle secret versioning strategy~~ Vault as source of truth (no merge)
- [x] ~~Merge existing manually-defined objects~~ CHANGED: Vault as source of truth

**Implementation:**
- Created `update.go` with GenerateObjectsFromVault()
- Vault is single source of truth - no manual object preservation
- Automatic YAML formatting for Azure provider
- Change detection to avoid unnecessary updates

### 4.2 SecretProviderClass Patching
- [x] Detect changes in vault contents
- [x] Generate patch for objects array
- [x] Apply patch to SecretProviderClass
- [x] Handle patch conflicts and retries

**Implementation:**
- JSON Patch (RFC 6902) implementation
- PatchSecretProviderClass() function
- Last-sync timestamp annotation
- Work queue retry logic (5 attempts with exponential backoff)
- Field removal when annotations disabled

### 4.3 Sync Coordination
- [x] Immediate event-driven reconciliation (work queue)
- [x] Periodic resync (5 minutes default)
- [x] Automatic deduplication of concurrent events
- [x] Prevent duplicate sync operations via work queue

**Implementation:**
- Work queue architecture with 5 concurrent workers
- Automatic event deduplication
- Rate limiting with exponential backoff
- Graceful error handling (one failed resource doesn't block others)

### 4.4 secretObjects Generation (Bonus)
- [x] Automatic secretObjects array population
- [x] Kubernetes Secret generation for vault secrets (type: Opaque)
- [x] Kubernetes TLS Secret generation for certificates (type: kubernetes.io/tls)
- [x] Annotation-based control (secret-objects, cert-objects)

**Implementation:**
- GenerateSecretObjectsFromVault() function
- CompareSecretObjects() for change detection
- Integrated into JSON Patch workflow

## Phase 5: Production Enhancements

**Status:** 📋 Planned

### 5.1 Observability
- [ ] Structured logging with log levels (info, warn, error, debug)
- [x] Health check endpoints (/healthz, /readyz)

### 5.2 Configuration Management
- [ ] ConfigMap-based configuration
- [ ] Environment variable overrides
- [ ] Validation of configuration on startup
- [ ] Hot reload for non-critical config

**Configurable items:**
- Sync interval (default: 5 minutes)
- Token TTL (default: 1 hour)
- Retry backoff parameters
- Log level
- Vault URL patterns

### 5.3 Security Hardening
- [x] Run as non-root user (UID 65534 in deployment)
- [x] Read-only root filesystem (distroless static)
- [x] Drop all Linux capabilities (securityContext)
- [x] Pod Security Standards Restricted compliance (seccompProfile)
- [ ] Network policies for egress control (optional)

### 5.4 Testing
- [ ] Unit tests for all components
- [ ] Integration tests with mock Kubernetes API
- [ ] Integration tests with mock Azure APIs
- [ ] End-to-end tests in test cluster
- [ ] Chaos testing scenarios

### 5.5 Documentation
- [x] Installation guide (README.md Quick Start)
- [x] RBAC setup documentation (deploy/rbac.yaml + README)
- [x] Azure Workload Identity setup guide (README.md + examples)
- [x] Troubleshooting guide (README.md)
- [x] Architecture diagrams (README.md + PRESENTATION.md)
- [ ] API reference (GoDoc comments)

### 5.6 Deployment
- [x] Container image in registry (GHCR)
- [x] Automated CI/CD pipeline (GitHub Actions)
- [x] Plain YAML manifests (deploy/ directory)
- [ ] Release process documentation (tagging strategy)

## Phase 6: Advanced Features

**Status:** 💡 Future Enhancements

### 6.1 Advanced Filtering
- [ ] Annotation-based secret name filtering
- [ ] Support for secret tags/labels
- [ ] Regex patterns for secret selection
- [ ] Exclude patterns

### 6.2 Multi-Tenant Support
- [ ] Namespace isolation
- [ ] Per-namespace vault mapping
- [ ] Cross-namespace secret sharing controls

### 6.3 Secret Rotation Coordination
- [ ] Detect secret rotation in vault
- [ ] Trigger pod restarts on secret changes
- [ ] Annotation-based restart policies
- [ ] Integration with external-secrets operator

### 6.4 Backup and Recovery
- [ ] Backup SecretProviderClass states
- [ ] Disaster recovery procedures
- [ ] Manual override mechanisms

## Dependencies and Prerequisites

### Kubernetes Requirements
- Kubernetes 1.20+ (for TokenRequest API)
- SecretProviderClass CRD installed (Azure Key Vault Provider)
- RBAC enabled

### Azure Requirements
- Azure Workload Identity installed in cluster
- Federated identity credentials configured per service
- User-Assigned Managed Identities
- Key Vault RBAC permissions (Secrets User, Certificates User)

### Controller RBAC Permissions
```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: azure-keyvault-sync-controller
rules:
- apiGroups: ["secrets-store.csi.x-k8s.io"]
  resources: ["secretproviderclasses"]
  verbs: ["get", "list", "watch", "update", "patch"]
- apiGroups: [""]
  resources: ["serviceaccounts/token"]
  verbs: ["create"]
```

## Success Criteria

### Phase 2 (Token Acquisition)
- Successfully obtain Kubernetes tokens for service accounts
- Successfully exchange tokens for Azure AD tokens
- Token refresh working before expiration

### Phase 3 (Key Vault Integration)
- Successfully list secrets from vault using service identity
- Handle authentication errors gracefully
- Respect vault RBAC permissions

### Phase 4 (SecretProviderClass Updates)
- ✅ Automatically update objects array on vault changes
- ✅ ~~Preserve manually-defined objects~~ CHANGED: Vault as source of truth
- ✅ No update loops or conflicts (deep struct comparison + work queue)

### Phase 5 (Production Readiness)
- All tests passing
- Metrics exported and queryable
- Running in production with zero trust violations
- Documentation complete

## Timeline Estimate

- Phase 2: 2-3 development sessions
- Phase 3: 2-3 development sessions
- Phase 4: 2-3 development sessions
- Phase 5: 3-5 development sessions
- Phase 6: Future, as needed

## Current Status

**Phase 4 Complete:** ✅ Production-ready controller with full automation

The controller is now feature-complete for production use:
- Phases 1-4: Complete
- Work queue architecture: Complete
- Error handling with retry logic: Complete
- Azure Key Vault integration: Complete
- Automatic SecretProviderClass updates: Complete
- Comprehensive documentation: Complete

**Next Steps:** Deploy to production, monitor, and gather feedback for Phase 5 enhancements.
