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

## Quick Start

**Prerequisites:**
- Kubernetes cluster with Azure Workload Identity installed
- Azure Key Vault with secrets/certificates
- Managed Identity with Key Vault RBAC permissions
- Federated Identity Credential configured

**Deploy Controller:**
```bash
# Apply RBAC and controller
kubectl apply -f https://raw.githubusercontent.com/jeanhaley32/azure-keyvault-sync-controller/main/deploy/rbac.yaml
kubectl apply -f https://raw.githubusercontent.com/jeanhaley32/azure-keyvault-sync-controller/main/deploy/deployment.yaml
```

**Create SecretProviderClass:**
```bash
# See examples/ directory for complete examples
kubectl apply -f examples/basic-sync.yaml  # Customize with your vault details
```

**Verify:**
```bash
# Check controller logs
kubectl logs -n kube-system -l app=azure-keyvault-sync-controller

# Verify sync completed
kubectl get secretproviderclass <name> -o jsonpath='{.metadata.annotations.azure-keyvault-sync/last-sync}'
```

## Features

**Production Ready** ✅

**Core Capabilities:**
- **Automatic Vault Sync** - Discovers and syncs all enabled secrets/certificates from Azure Key Vault
- **Kubernetes Secret Generation** - Optionally creates Kubernetes Secrets (Opaque and TLS types)
- **Service Account Impersonation** - Uses existing Azure Workload Identity, no centralized credentials
- **Event-Driven Reconciliation** - Immediate updates via work queue (no waiting for periodic sync)
- **Robust Error Handling** - Retry logic with exponential backoff, preserves data on permission errors
- **Zero Configuration** - Just add annotations, vault is the source of truth

**Technical Highlights:**
- Work queue pattern with 5 concurrent workers and rate limiting
- Token caching with automatic renewal (K8s + Azure AD tokens)
- JSON Patch updates with change detection
- Comprehensive logging and observability
- Multi-vault support per service account

**Implementation Phases:**
- ✅ Phase 1: Foundation (watching, caching, work queue)
- ✅ Phase 2: Token acquisition (K8s + Azure AD via Workload Identity)
- ✅ Phase 3: Azure Key Vault integration (secrets + certificates)
- ✅ Phase 4: SecretProviderClass updates (objects + secretObjects)

See [ROADMAP.md](ROADMAP.md) for detailed implementation history.

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
├── examples/                                 # Usage examples
│   ├── basic-sync.yaml                       # Minimal configuration
│   ├── with-secrets.yaml                     # With Kubernetes Secret generation
│   ├── full-example.yaml                     # Complete example with Pod
│   └── README.md                             # Examples documentation
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
├── Dockerfile                                # Container image build
├── Makefile                                  # Build automation
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

## Container Images

### GitHub Container Registry (Public)

Pre-built container images are automatically built and published to GitHub Container Registry:

```bash
# Pull latest image
docker pull ghcr.io/jeanhaley32/azure-keyvault-sync-controller:latest

# Pull specific commit
docker pull ghcr.io/jeanhaley32/azure-keyvault-sync-controller:main-<sha>
```

**Available tags:**
- `latest` - Latest successful build from main branch
- `main-<sha>` - Specific commit builds
- Semantic version tags (e.g., `v1.0.0`) will be created when releases are tagged

**Platforms:**
- linux/amd64
- linux/arm64

### For Kaufman Rossin

This repository can be forked to Kaufman Rossin's GitHub organization. The included GitHub Actions workflow can be adapted to push images to Azure Container Registry (ACR) instead of GHCR.

## Development

### Testing Locally

```bash
# Run against your current kubeconfig context
go run .

# Test with specific SecretProviderClass
kubectl apply -f examples/basic-sync.yaml
```

### Building

```bash
# Using Makefile (recommended)
make build                # Build binary
make docker-build         # Build container image
make docker-push          # Push to registry
make deploy               # Deploy to cluster

# Manual build
go build -o azure-keyvault-sync-controller .

# Manual container build
docker build -t azure-keyvault-sync-controller:latest .
```

### CI/CD

The repository includes a GitHub Actions workflow (`.github/workflows/build-and-push.yaml`) that automatically:
- Builds multi-arch container images (amd64, arm64)
- Pushes to GitHub Container Registry
- Creates version tags from git tags
- Runs on every push to main and on version tags

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
