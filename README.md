# Azure Key Vault Sync Controller

A Kubernetes controller that automatically synchronizes Azure Key Vault secrets to SecretProviderClass objects.

## Overview

This controller watches SecretProviderClass resources and automatically updates their `objects` array to match the contents of the associated Azure Key Vault. It eliminates the need to manually maintain secret lists in SecretProviderClass manifests.

## Current State

**Status:** Phase 2.1 Complete - Kubernetes Token Acquisition ✅

### Implemented Features

**Phase 1: Foundation** ✅
- Watch SecretProviderClass resources across all namespaces
- Annotation-based opt-in filtering (`azure-keyvault-sync/enabled: "true"`)
- Service account discovery via annotations
- Thread-safe in-memory cache with mutex protection
- Automatic watch reconnection on failures
- Periodic 5-minute resync to catch missed events

**Phase 2.1: Kubernetes Token Acquisition** ✅
- Service account impersonation via TokenRequest API
- Real Kubernetes JWT token acquisition
- Token caching with automatic renewal (80% of 1-hour lifetime)
- ClientID extraction from SecretProviderClass spec.parameters
- Tested and verified against real AKS cluster

### Token Configuration
- **Expiration:** 3600 seconds (1 hour - Azure standard)
- **Renewal:** 80% of lifetime (48 minutes before expiry)
- **Audience:** `api://AzureADTokenExchange`
- **Format:** JWT with claims for Azure Workload Identity federation

## Implementation Progress

1. ✅ **Watch SecretProviderClass resources** - Monitor all SecretProviderClass objects in the cluster
2. ✅ **Discover service accounts** - Identify the Kubernetes service account associated with each SecretProviderClass
3. ✅ **Impersonate service accounts** - Use the Kubernetes TokenRequest API to obtain tokens for individual service accounts
4. ⏳ **Exchange tokens** - Trade Kubernetes tokens for Azure AD tokens via Azure Workload Identity federation (Phase 2.2)
5. 📋 **Query Azure Key Vault** - List all secrets and certificates in the vault using the service's existing permissions (Phase 3)
6. 📋 **Update SecretProviderClass** - Automatically populate the `objects` array with discovered vault contents (Phase 4)

## Architecture

### Security Model

The controller uses **service account impersonation** rather than a single privileged credential:

- No centralized credential with access to all vaults
- Reuses existing Azure Workload Identity infrastructure
- Maintains accurate audit attribution (vault logs show actual service identity)
- Reduces blast radius if controller is compromised

### Infrastructure Requirements

Each service should have:
- Azure Key Vault: `{environment}-{service}-vault`
- User-Assigned Managed Identity: `{service}-{environment}-identity`
- Federated Identity Credential linking to Kubernetes ServiceAccount
- RBAC roles: Key Vault Secrets User + Certificate User

### Authentication Flow

```
Controller → Impersonates ServiceAccount
  → Kubernetes TokenRequest API
  → Azure Workload Identity federation
  → Azure Managed Identity
  → Azure Key Vault RBAC
  → List secrets/certificates
  → Update SecretProviderClass
```

## Usage

### Required Annotations

For a SecretProviderClass to be managed by the controller, it must have these annotations:

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: example-secrets
  namespace: default
  annotations:
    azure-keyvault-sync/enabled: "true"
    azure-keyvault-sync/service-account: "workload-service-account"
spec:
  provider: azure
  parameters:
    clientID: "aac3d546-358f-4e74-94e5-bb4c472d7cc0"
    # ... other parameters
```

### Running Locally

```bash
# Initialize dependencies
go mod tidy

# Run the controller (uses ~/.kube/config)
go run .
```

The controller will connect to your cluster and begin watching SecretProviderClass resources.

### In-Cluster Deployment

```bash
# Apply RBAC permissions
kubectl apply -f deploy/rbac.yaml

# Deploy the controller
kubectl apply -f deploy/deployment.yaml
```

## Project Structure

```
.
├── .background/                              # Session documentation
│   └── session-2025-10-25.md
├── deploy/                                   # Deployment manifests
│   ├── deployment.yaml                       # Controller deployment
│   └── rbac.yaml                             # RBAC permissions
├── planning/                                 # Implementation plans
│   ├── token-acquisition.md                  # Research findings
│   └── token-acquisition-implementation.md   # Implementation guide
├── cache.go                                  # SecretProviderClass cache
├── controller.go                             # Main controller logic
├── main.go                                   # Application entry point
├── token.go                                  # Token acquisition and caching
├── CHANGELOG.md                              # Development history
├── LICENSE                                   # MIT License
├── README.md                                 # This file
├── ROADMAP.md                                # Development roadmap
├── go.mod                                    # Go module definition
└── go.sum                                    # Dependency checksums
```

## Documentation

### In This Repository
- `ROADMAP.md` - Complete development roadmap with all phases
- `CHANGELOG.md` - Detailed development history and changes
- `.background/session-2025-10-25.md` - Comprehensive session transcript
- `planning/` - Implementation plans and research

### Parent Directory
- `OPS2-984/Identity-Impersonation-Design.md` - Technical design of the impersonation approach
- `OPS2-984/Planning-Notes.md` - Architecture decisions and risk analysis

## RBAC Permissions

The controller requires these Kubernetes permissions:

```yaml
# SecretProviderClass management
- apiGroups: ["secrets-store.csi.x-k8s.io"]
  resources: ["secretproviderclasses"]
  verbs: ["get", "list", "watch", "update", "patch"]

# Token acquisition (Phase 2.1)
- apiGroups: [""]
  resources: ["serviceaccounts/token"]
  verbs: ["create"]
```

## Next Steps

**Phase 2.2: Azure AD Token Exchange** (Next)
- Implement Azure Workload Identity credential exchange
- Trade Kubernetes JWT for Azure AD access token
- Cache Azure tokens with refresh logic
- Handle federated credential validation errors

**Phase 3: Azure Key Vault Integration**
- Initialize Azure Key Vault SDK client
- List secrets and certificates from vault
- Handle vault RBAC permissions
- Implement pagination and error handling

**Phase 4: SecretProviderClass Updates**
- Generate objects array from vault contents
- Patch SecretProviderClass resources
- Handle merge strategy for existing objects
- Add last-sync timestamp annotation

## License

MIT License - See LICENSE file for details
