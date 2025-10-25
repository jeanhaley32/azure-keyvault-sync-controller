# Development Log

## 2025-10-25

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

1. **Token Acquisition** - Implement Kubernetes TokenRequest API for service account impersonation
2. **Azure AD Token Exchange** - Trade Kubernetes tokens for Azure AD tokens via Workload Identity federation
3. **Azure Key Vault Integration** - List secrets and certificates from vault
4. **SecretProviderClass Updates** - Automatically populate objects array with discovered vault contents

## Architecture

### Current State
- Watch SecretProviderClass resources
- Filter by annotations for opt-in management
- Maintain cache of managed objects
- Track service account associations

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

- `main` - Stable, merged features
- `annotation-support` - Merged to main
- `service-account-discovery` - Merged to main
- `refactor-file-structure` - Merged to main
- `token-acquisition` - Created, ready for implementation
