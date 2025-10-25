# Azure Key Vault Sync Controller

A Kubernetes controller that automatically synchronizes Azure Key Vault secrets to SecretProviderClass objects.

## Overview

This controller watches SecretProviderClass resources and automatically updates their `objects` array to match the contents of the associated Azure Key Vault. It eliminates the need to manually maintain secret lists in SecretProviderClass manifests.

## Current State

**Status:** Early Development

Currently implemented:
- Watch SecretProviderClass resources across all namespaces
- Maintain in-memory cache of all SecretProviderClass objects
- Automatic reconnection on watch failures
- Periodic 5-minute resync to catch missed events
- Thread-safe concurrent access with mutex protection

## Eventual Objective

The controller will:

1. **Watch SecretProviderClass resources** - Monitor all SecretProviderClass objects in the cluster
2. **Discover service accounts** - Identify the Kubernetes service account associated with each SecretProviderClass
3. **Impersonate service accounts** - Use the Kubernetes TokenRequest API to obtain tokens for individual service accounts
4. **Exchange tokens** - Trade Kubernetes tokens for Azure AD tokens via Azure Workload Identity federation
5. **Query Azure Key Vault** - List all secrets and certificates in the vault using the service's existing permissions
6. **Update SecretProviderClass** - Automatically populate the `objects` array with discovered vault contents

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

## Running Locally

```bash
# Initialize dependencies
go mod tidy

# Run the controller
go run main.go
```

The controller will connect to your cluster using `~/.kube/config` and begin watching SecretProviderClass resources.

## Project Structure

```
.
├── main.go           # Controller implementation
├── go.mod            # Go module definition
├── go.sum            # Dependency checksums
└── README.md         # This file
```

## Related Documentation

See the parent `OPS2-984/` directory for detailed design documents:
- `Identity-Impersonation-Design.md` - Technical design of the impersonation approach
- `Planning-Notes.md` - Architecture decisions and risk analysis
- `Segment-1-Plan.md` - Implementation roadmap

## Development Status

**Next Steps:**
1. Add annotation support for configuration (`azure-keyvault-sync/enabled`)
2. Implement service account discovery from SecretProviderClass
3. Implement Kubernetes token acquisition via impersonation
4. Implement Azure AD token exchange
5. Implement Azure Key Vault secret listing
6. Implement SecretProviderClass updates

## License

MIT License - See LICENSE file for details
