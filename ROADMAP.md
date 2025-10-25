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

## Phase 2: Token Acquisition (IN PROGRESS)

**Status:** 🔄 Ready to start

### 2.1 Kubernetes Token Request API
- [ ] Implement service account impersonation
- [ ] Use TokenRequest API to obtain short-lived tokens
- [ ] Token expiration and renewal logic
- [ ] Error handling for impersonation failures

**Technical Details:**
- Use `k8s.io/client-go/kubernetes` client
- Call `ServiceAccounts().CreateToken()` with TokenRequest
- Set appropriate audience for Azure Workload Identity
- Token TTL: 1 hour (configurable)

**Dependencies:**
- Require RBAC permissions to create tokens for service accounts
- Controller service account needs `serviceaccounts/token` create permission

### 2.2 Azure Workload Identity Integration
- [ ] Exchange Kubernetes token for Azure AD token
- [ ] Configure OIDC federation trust
- [ ] Handle token exchange errors
- [ ] Token caching and refresh

**Technical Details:**
- Audience: `api://AzureADTokenExchange`
- Use Azure Identity SDK for Go
- Federated credential configuration required per service
- Token refresh before expiration

## Phase 3: Azure Key Vault Integration

**Status:** 📋 Planned

### 3.1 Key Vault Client Setup
- [ ] Initialize Azure Key Vault SDK client
- [ ] Use exchanged Azure AD token for authentication
- [ ] Handle per-service vault discovery
- [ ] Connection pooling and reuse

**Technical Details:**
- Use `github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets`
- Vault URL format: `https://{environment}-{service}-vault.vault.azure.net`
- Support for multiple regions/environments

### 3.2 Secret and Certificate Listing
- [ ] List all secrets in vault
- [ ] List all certificates in vault
- [ ] Handle pagination for large vaults
- [ ] Filter out disabled/expired items

**Technical Details:**
- Use ListSecrets() and ListCertificates() APIs
- Respect RBAC permissions (read-only access)
- Handle rate limiting and throttling
- Cache vault contents with TTL

### 3.3 Error Handling and Resilience
- [ ] Handle vault access denied errors
- [ ] Retry logic with exponential backoff
- [ ] Graceful degradation on failures
- [ ] Metrics and logging for vault operations

## Phase 4: SecretProviderClass Updates

**Status:** 📋 Planned

### 4.1 Object Array Generation
- [ ] Convert vault secrets to SecretProviderClass objects array
- [ ] Convert vault certificates to objects array
- [ ] Handle secret versioning strategy
- [ ] Merge existing manually-defined objects

**Technical Details:**
```yaml
objects:
  - objectName: "secret-name"
    objectType: "secret"
  - objectName: "cert-name"
    objectType: "cert"
```

### 4.2 SecretProviderClass Patching
- [ ] Detect changes in vault contents
- [ ] Generate patch for objects array
- [ ] Apply patch to SecretProviderClass
- [ ] Handle patch conflicts and retries

**Technical Details:**
- Use Strategic Merge Patch or JSON Patch
- Preserve other fields in SecretProviderClass
- Add annotation with last sync timestamp
- Event logging for updates

### 4.3 Sync Coordination
- [ ] Configurable sync interval per object
- [ ] Annotation-based sync frequency override
- [ ] Manual sync trigger mechanism
- [ ] Prevent duplicate sync operations

## Phase 5: Production Readiness

**Status:** 📋 Planned

### 5.1 Observability
- [ ] Prometheus metrics export
- [ ] Key metrics: sync operations, errors, latency
- [ ] Structured logging with log levels
- [ ] Health check endpoints

**Metrics to track:**
- `sync_operations_total{status="success|failure"}`
- `sync_duration_seconds`
- `vault_api_calls_total{operation="list_secrets|list_certs"}`
- `token_exchange_errors_total`

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
- [ ] Run as non-root user
- [ ] Read-only root filesystem
- [ ] Drop all Linux capabilities
- [ ] Network policies for egress control
- [ ] Pod Security Standards compliance

### 5.4 Testing
- [ ] Unit tests for all components
- [ ] Integration tests with mock Kubernetes API
- [ ] Integration tests with mock Azure APIs
- [ ] End-to-end tests in test cluster
- [ ] Chaos testing scenarios

### 5.5 Documentation
- [ ] Installation guide
- [ ] RBAC setup documentation
- [ ] Azure Workload Identity setup guide
- [ ] Troubleshooting guide
- [ ] Architecture diagrams
- [ ] API reference

### 5.6 Deployment
- [ ] Helm chart for installation
- [ ] Kustomize manifests
- [ ] Container image in registry
- [ ] Automated CI/CD pipeline
- [ ] Release process documentation

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
- Automatically update objects array on vault changes
- Preserve manually-defined objects
- No update loops or conflicts

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

## Current Focus

**Next Immediate Task:** Implement Kubernetes TokenRequest API (Phase 2.1)
