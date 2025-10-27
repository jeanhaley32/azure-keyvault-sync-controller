# Azure Key Vault Sync Controller

A production-ready Kubernetes controller that automatically synchronizes Azure Key Vault contents to SecretProviderClass objects using Azure Workload Identity federation.

## Overview

This controller watches SecretProviderClass resources and automatically:
- Discovers secrets and certificates in Azure Key Vault
- Updates the `objects` array to match vault contents
- Optionally generates `secretObjects` for automatic Kubernetes Secret creation
- Handles permission errors gracefully with retry logic
- Provides immediate event-driven reconciliation

**Key Feature:** Vault is the single source of truth - no manual object management required.

## Current State

**Status:** Phase 4 Complete - Production Ready ✅

### Implemented Features

**Phase 1: Foundation** ✅
- Watch SecretProviderClass resources across all namespaces
- Annotation-based opt-in filtering (`azure-keyvault-sync/enabled: "true"`)
- Service account discovery via annotations
- Thread-safe in-memory cache with mutex protection
- Automatic watch reconnection on failures
- Work queue architecture with event deduplication

**Phase 2.1: Kubernetes Token Acquisition** ✅
- Service account impersonation via TokenRequest API
- Real Kubernetes JWT token acquisition
- Token caching with automatic renewal (80% of 1-hour lifetime)
- ClientID extraction from SecretProviderClass spec.parameters
- Tested and verified against real AKS cluster

**Phase 2.2: Azure AD Token Exchange** ✅
- WorkloadIdentityCredential integration (Azure SDK)
- Exchange K8s JWT for Azure AD access tokens via federated identity
- Azure AD token caching with automatic renewal (80% of lifetime)
- TenantID extraction from SecretProviderClass spec.parameters
- Secure temporary file handling for token exchange
- Service-level token scope (multi-vault support)
- Tested with real AKS cluster and Azure federated identity

**Phase 3: Azure Key Vault Integration** ✅
- Custom token credential wrapper (CachedTokenCredential)
- List secrets from Azure Key Vault with pagination
- List certificates from Azure Key Vault with pagination
- KeyvaultName extraction from SecretProviderClass spec.parameters
- Filter disabled secrets and certificates automatically
- Comprehensive logging of discovered vault contents
- Error handling with retry logic (5 attempts with exponential backoff)

**Phase 4: SecretProviderClass Updates** ✅
- Automatic `objects` array population from vault contents
- Automatic `secretObjects` generation for Kubernetes Secrets
- JSON Patch updates with last-sync timestamp annotation
- Change detection to avoid unnecessary updates
- Field removal when annotations disabled
- Vault as source of truth (no merge logic)
- Immediate event-driven reconciliation via work queue

**Work Queue Architecture** ✅
- Industry-standard Kubernetes controller pattern
- 5 concurrent workers with rate limiting
- Automatic event deduplication
- Retry logic with exponential backoff (max 5 attempts)
- Graceful error handling (one failed resource doesn't block others)
- No race conditions or reconciliation loops

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

### Work Queue Pattern

```
┌─────────────────────────────────────────────────────────┐
│                  Work Queue Architecture                 │
├─────────────────────────────────────────────────────────┤
│  ┌─────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │ Events  │───▶│ Work Queue   │───▶│ Worker Pool  │  │
│  │         │    │              │    │ (5 workers)  │  │
│  └─────────┘    │ - Dedupes    │    └──────────────┘  │
│                 │ - Retries    │           │           │
│  ┌─────────┐    │ - Rate limit │           │           │
│  │Periodic │───▶│              │           ▼           │
│  │ Resync  │    └──────────────┘    ┌──────────────┐  │
│  │(5 min)  │                        │ Reconcile    │  │
│  └─────────┘                        │ Resource     │  │
│                                     └──────────────┘  │
└─────────────────────────────────────────────────────────┘
```

**Benefits:**
- Immediate event-driven reconciliation (no 5-minute wait)
- Automatic deduplication (multiple events → single reconciliation)
- Rate limiting (max 5 concurrent reconciliations)
- Retry logic (transient failures retry with backoff)
- Graceful degradation (permission errors don't block other resources)

## Usage

### Required Annotations

#### Basic Sync (objects only)

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
    keyvaultName: "staging-example-vault"
    clientID: "aac3d546-358f-4e74-94e5-bb4c472d7cc0"
    tenantId: "8b83ab42-3e3f-422d-85ca-fe2d40c51e35"
    objects: ""  # Will be auto-populated by controller
```

**Result:** Controller automatically populates `objects` array with all enabled secrets and certificates from vault.

#### With Kubernetes Secret Generation

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: example-secrets
  namespace: default
  annotations:
    azure-keyvault-sync/enabled: "true"
    azure-keyvault-sync/service-account: "workload-service-account"
    azure-keyvault-sync/secret-objects: "true"     # Enable secret sync
    azure-keyvault-sync/cert-objects: "true"       # Enable certificate sync
spec:
  provider: azure
  parameters:
    keyvaultName: "staging-example-vault"
    clientID: "aac3d546-358f-4e74-94e5-bb4c472d7cc0"
    tenantId: "8b83ab42-3e3f-422d-85ca-fe2d40c51e35"
    objects: ""        # Will be auto-populated
  secretObjects: []    # Will be auto-populated
```

**Result:**
- Controller populates both `objects` and `secretObjects` arrays
- CSI driver automatically creates Kubernetes Secrets (type: Opaque for secrets, kubernetes.io/tls for certificates)

### Annotations Reference

| Annotation | Required | Description |
|------------|----------|-------------|
| `azure-keyvault-sync/enabled` | Yes | Set to `"true"` to enable sync |
| `azure-keyvault-sync/service-account` | Yes | ServiceAccount name for impersonation |
| `azure-keyvault-sync/secret-objects` | No | Set to `"true"` to create Kubernetes Secrets from vault secrets |
| `azure-keyvault-sync/cert-objects` | No | Set to `"true"` to create Kubernetes TLS Secrets from vault certificates |
| `azure-keyvault-sync/last-sync` | Auto | Timestamp of last successful sync (set by controller) |

### Vault as Source of Truth

**Important:** The controller uses vault contents as the single source of truth. This means:

✅ **Supported:**
- Vault secrets automatically appear in `objects` array
- Vault deletions automatically removed from `objects` array
- Use Azure Key Vault versioning for pinning specific versions

❌ **Not Supported:**
- Manual object definitions in SecretProviderClass (they will be overwritten)
- Mixing manual and automatic objects

**Migration Path:**
If you need custom object configurations:
1. **Option A:** Move objects to vault and use vault versioning
2. **Option B:** Create separate SecretProviderClass without sync annotations
3. **Option C:** Disable sync for specific resources (remove annotations)

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

## Configuration

### Controller Constants

Current defaults (configurable in code):

| Constant | Value | Description |
|----------|-------|-------------|
| `resyncInterval` | 5 minutes | Periodic resync frequency |
| `numWorkers` | 5 | Number of concurrent workers |
| `maxRetries` | 5 | Maximum retry attempts before dropping |

### Token Configuration

**Kubernetes Tokens:**
- **Expiration:** 3600 seconds (1 hour - Azure standard)
- **Renewal:** 80% of lifetime (48 minutes before expiry)
- **Audience:** `api://AzureADTokenExchange`
- **Format:** JWT with claims for Azure Workload Identity federation

**Azure AD Tokens:**
- **Expiration:** 28 hours (Azure-configured lifetime)
- **Renewal:** 80% of lifetime (22.4 hours before expiry)
- **Scope:** `https://vault.azure.net/.default` (service-level)
- **Format:** JWT with claims for Azure Key Vault access
- **Cache:** By namespace/serviceAccount (reusable across vaults)

## Error Handling

### Permission Errors (403 Forbidden)

When vault permissions are missing:
1. Controller logs error with full Azure RBAC details
2. Retries 5 times with exponential backoff
3. After max retries, drops item from queue
4. **Existing objects preserved** (no data loss)
5. Other resources continue processing normally

### Transient Failures

Network issues, temporary token problems, etc.:
1. Automatic retry with exponential backoff
2. Max 5 attempts per resource
3. Work queue handles rate limiting
4. Graceful degradation (doesn't crash controller)

## Project Structure

```
.
├── deploy/                                   # Deployment manifests
│   ├── deployment.yaml                       # Controller deployment
│   └── rbac.yaml                             # RBAC permissions
├── planning/                                 # Implementation plans
│   ├── architecture-improvements.md          # Work queue implementation
│   ├── secretproviderclass-updates.md        # Phase 4 implementation
│   ├── workflow-blindspot-fixes.md           # Vault as source of truth
│   ├── azure-token-exchange.md               # Azure AD token design
│   ├── keyvault-integration.md               # Vault integration design
│   └── token-acquisition-implementation.md   # Token acquisition guide
├── cache.go                                  # SecretProviderClass cache
├── controller.go                             # Main controller logic + work queue
├── main.go                                   # Application entry point
├── token.go                                  # Token acquisition and caching
├── azure.go                                  # Azure AD token exchange
├── vault.go                                  # Azure Key Vault integration
├── update.go                                 # SecretProviderClass patching
├── CHANGELOG.md                              # Development history
├── LICENSE                                   # MIT License
├── README.md                                 # This file
├── ROADMAP.md                                # Development roadmap
├── go.mod                                    # Go module definition
└── go.sum                                    # Dependency checksums
```

## RBAC Permissions

The controller requires these Kubernetes permissions:

```yaml
# SecretProviderClass management
- apiGroups: ["secrets-store.csi.x-k8s.io"]
  resources: ["secretproviderclasses"]
  verbs: ["get", "list", "watch", "update", "patch"]

# Token acquisition
- apiGroups: [""]
  resources: ["serviceaccounts/token"]
  verbs: ["create"]
```

## Monitoring and Observability

### Logs

The controller provides comprehensive logging:

```
2025/10/26 23:24:58 Event: ADDED default/example-secrets (sync enabled, service-account: app-sa) - enqueuing
2025/10/26 23:24:58 Obtained Kubernetes token for default/app-sa, ready for Azure authentication
2025/10/26 23:24:58 Obtained Azure AD token for default/app-sa, ready for Key Vault access
2025/10/26 23:24:58 Found 3 secrets in vault staging-example-vault
2025/10/26 23:24:58 Found 0 certificates in vault staging-example-vault
2025/10/26 23:24:58 Successfully updated default/example-secrets with 3 objects (3 secrets, 0 certs)
```

### Last-Sync Annotation

Check when a resource was last synced:

```bash
kubectl get secretproviderclass example-secrets -o jsonpath='{.metadata.annotations.azure-keyvault-sync/last-sync}'
# Output: 2025-10-26T23:24:58Z
```

## Troubleshooting

### Resource not syncing

Check annotations:
```bash
kubectl get secretproviderclass example-secrets -o yaml | grep -A 5 annotations
```

Ensure:
- `azure-keyvault-sync/enabled: "true"` is present
- `azure-keyvault-sync/service-account` points to existing ServiceAccount
- `last-sync` annotation exists and is recent

### Permission errors

Check controller logs for Azure RBAC details:
```bash
kubectl logs -l app=azure-keyvault-sync-controller -n kube-system
```

Look for:
- `403 Forbidden` errors with Azure resource details
- Missing RBAC assignments (`Assignment: (not found)`)
- Verify ServiceAccount has federated identity credential
- Verify Managed Identity has Key Vault RBAC roles

### Secrets not created

If `secretObjects` annotation is enabled but no Secrets created:
1. Check controller logs for errors
2. Verify CSI driver is running: `kubectl get pods -n kube-system -l app=secrets-store-csi-driver`
3. Check pod using SecretProviderClass has volume mount
4. Review CSI driver logs: `kubectl logs -n kube-system -l app=secrets-store-csi-driver`

## Development

### Testing Locally

```bash
# Run against your current kubeconfig context
go run .

# Test with specific SecretProviderClass
kubectl apply -f examples/secretproviderclass.yaml
```

### Building

```bash
# Build binary
go build -o azure-keyvault-sync-controller .

# Build container
docker build -t azure-keyvault-sync-controller:latest .
```

## Next Steps

**Phase 5: Production Enhancements** (Future)
- Prometheus metrics export (sync counts, error rates, queue depth)
- Structured logging with log levels (info, warn, error, debug)
- Health check endpoints (/healthz, /readyz)
- Configuration management via ConfigMap
- Security hardening (non-root, read-only filesystem, seccomp)
- Comprehensive test coverage (unit, integration, e2e)

## License

MIT License - See LICENSE file for details

## Contributing

This is an internal tool for Kaufman Rossin. For questions or contributions, contact the DevOps team.
