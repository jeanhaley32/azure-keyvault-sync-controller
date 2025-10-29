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

## When to Use This Controller

This controller is designed for teams using the [Azure Key Vault Provider for Secrets Store CSI Driver](https://learn.microsoft.com/en-us/azure/aks/csi-secrets-store-driver) who want to eliminate manual configuration management.

### The Problem This Solves

When using the Secrets Store CSI Driver with Azure Key Vault, you must manually maintain the `objects` array in your SecretProviderClass:

```yaml
spec:
  parameters:
    objects: |
      array:
        - objectName: "database-password"    # Must manually list every secret
          objectType: "secret"
        - objectName: "api-key"              # Must manually list every secret
          objectType: "secret"
        - objectName: "tls-cert"             # Must manually list every certificate
          objectType: "cert"
```

**Challenges:**
- 🔄 Must update YAML every time secrets change in vault
- ⚠️ Easy to forget secrets, causing application failures
- 📝 Manual synchronization between vault and Kubernetes
- 🔁 Repetitive updates across multiple environments

### The Solution

This controller **automatically discovers** vault contents and keeps SecretProviderClass synchronized:

```yaml
spec:
  parameters:
    objects: ""  # Controller populates this automatically!
```

The controller:
1. Authenticates to Azure Key Vault using Workload Identity
2. Lists all enabled secrets and certificates
3. Automatically updates the `objects` array
4. Optionally generates `secretObjects` for Kubernetes Secrets

**Result:** Your vault becomes the single source of truth. Add/remove secrets in Azure, and the controller updates Kubernetes automatically.

### Prerequisites

You must have the Azure Secrets Store CSI Driver infrastructure already set up:

**Required Azure Documentation:**
- [Use the Azure Key Vault Provider for Secrets Store CSI Driver in AKS](https://learn.microsoft.com/en-us/azure/aks/csi-secrets-store-driver)
- [Use Azure Workload Identity with AKS](https://learn.microsoft.com/en-us/azure/aks/workload-identity-overview)
- [Provide an identity to access Azure Key Vault](https://learn.microsoft.com/en-us/azure/aks/csi-secrets-store-identity-access)

**What You Need:**
- ✅ Secrets Store CSI Driver installed in your cluster
- ✅ Azure Workload Identity enabled
- ✅ Managed Identity with Key Vault permissions (Get Secrets, List Secrets, Get Certificates, List Certificates)
- ✅ Federated Identity Credential linking Kubernetes ServiceAccount to Managed Identity

**What This Controller Adds:**
- 🤖 Automatic vault content discovery
- 🔄 Continuous synchronization of SecretProviderClass objects
- 🎯 Zero-touch configuration management

### Related Documentation

- **Azure Key Vault Provider:** https://azure.github.io/secrets-store-csi-driver-provider-azure/
- **Secrets Store CSI Driver:** https://secrets-store-csi-driver.sigs.k8s.io/
- **Azure Workload Identity:** https://azure.github.io/azure-workload-identity/

## Quick Start

**Prerequisites:**
- Kubernetes cluster with Azure Workload Identity installed
- Azure Key Vault with secrets/certificates
- Managed Identity with Key Vault RBAC permissions
- Federated Identity Credential configured

### Deployment Options

The controller supports two deployment models:

1. **Cluster-Wide** (Simple) - Single controller watches all namespaces
2. **Namespace-Scoped** (Secure) - Per-namespace controller with isolated RBAC

**Cluster-Wide Deployment:**
```bash
# Apply RBAC and controller (watches all namespaces)
kubectl apply -f https://raw.githubusercontent.com/jeanhaley32/azure-keyvault-sync-controller/main/deploy/rbac.yaml
kubectl apply -f https://raw.githubusercontent.com/jeanhaley32/azure-keyvault-sync-controller/main/deploy/deployment.yaml
```

**Namespace-Scoped Deployment (Recommended for Production):**
```bash
# Set target namespace
export NAMESPACE=production

# Deploy namespace-scoped RBAC and controller
kubectl apply -f - <<EOF
$(curl -s https://raw.githubusercontent.com/jeanhaley32/azure-keyvault-sync-controller/main/deploy/rbac-namespaced.yaml | sed "s/\${NAMESPACE}/$NAMESPACE/g")
EOF

kubectl apply -f - <<EOF
$(curl -s https://raw.githubusercontent.com/jeanhaley32/azure-keyvault-sync-controller/main/deploy/deployment-namespaced.yaml | sed "s/\${NAMESPACE}/$NAMESPACE/g")
EOF
```

See [Namespace-Scoped Examples](examples/namespace-scoped/) for detailed deployment instructions and security benefits.

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
    clientID: "00000000-0000-0000-0000-000000000000"
    tenantId: "11111111-1111-1111-1111-111111111111"
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
    clientID: "00000000-0000-0000-0000-000000000000"
    tenantId: "11111111-1111-1111-1111-111111111111"
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

> This design choice is based on my current usage: one vault per service, with separate accounts for each service.
> It may be possible to add annotations within Azure's Vault secrets to enable selective secrets
> syncing. As it stands this controller is intended to sync all of the secrets within a particular vault to
> the target SecretProviderClass


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

### Environment Variables

The controller can be configured via environment variables without requiring code changes:

| Variable | Default | Valid Values | Description |
|----------|---------|--------------|-------------|
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR` | Logging verbosity level |
| `LOG_FORMAT` | `text` | `text`, `json` | Log output format (text for dev, json for production) |
| `SYNC_INTERVAL` | `5m` | Duration ≥ 30s | How often to resync all SecretProviderClasses |
| `WORKER_COUNT` | `5` | 1-100 | Number of concurrent reconciliation workers |
| `HEALTH_CHECK_PORT` | `8080` | 1-65535 | Port for health check endpoints (/healthz, /readyz) |
| `KUBERNETES_QPS` | `10.0` | 0-100 | Kubernetes API queries per second limit |
| `KUBERNETES_BURST` | `20` | 1-200 | Kubernetes API burst allowance for short spikes |
| `AZURE_CIRCUIT_BREAKER_THRESHOLD` | `5` | 3-10 | Azure API failures before circuit opens |
| `AZURE_CIRCUIT_BREAKER_TIMEOUT` | `1m` | 30s-5m | Wait time before testing Azure API after circuit opens |

**Example deployment.yaml configuration:**

```yaml
env:
  - name: LOG_LEVEL
    value: "DEBUG"
  - name: SYNC_INTERVAL
    value: "10m"
  - name: WORKER_COUNT
    value: "3"
```

The controller validates all configuration values at startup and will exit immediately with a clear error message if any values are invalid.

### Rate Limiting

The controller implements multiple layers of rate limiting to protect both Kubernetes and Azure APIs:

**Kubernetes API Rate Limiting:**
- **QPS (Queries Per Second)**: Limits sustained API request rate
- **Burst**: Allows temporary spikes above QPS for short periods
- **Purpose**: Prevents controller from overwhelming Kubernetes API server
- **Default values**: 10 QPS with 20 burst provides good balance for most clusters

**Azure Circuit Breaker:**
- **Threshold**: Number of consecutive failures before circuit opens
- **Timeout**: How long to wait before testing if Azure API has recovered
- **Purpose**: Protects against cascading failures when Azure throttles requests (429 responses)
- **Behavior**: When open, all Azure calls fail fast to avoid wasting resources
- **States**:
  - *Closed*: Normal operation, requests pass through
  - *Open*: Failures exceeded threshold, all requests fail immediately
  - *Half-Open*: Testing if service recovered with single request

**Azure 429 Handling:**
- Automatically detects Azure throttling (429 Too Many Requests)
- Extracts Retry-After header from Azure response
- Waits for specified duration before retrying
- Logs throttling events for monitoring
- Azure Key Vault limit: 2000 requests per 10 seconds for secrets

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

**Platform:**
- linux/amd64

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

## Security

### Pod Security

The controller is **Pod Security Standards (PSS) Restricted-compliant**:
- Non-root user (UID 65534)
- Read-only root filesystem
- All capabilities dropped
- No privilege escalation
- Seccomp profile enabled (RuntimeDefault)

### Deployment Models

**Namespace-Scoped (Recommended for Production):**
- **Blast Radius:** Single namespace only
- **Token Creation:** Limited to same namespace ServiceAccounts
- **RBAC:** Role (namespace-only) instead of ClusterRole
- **Security:** 90%+ reduction in privilege escalation risk

**Cluster-Wide (Simple Deployment):**
- **Blast Radius:** Entire cluster
- **Token Creation:** Any ServiceAccount in any namespace
- **RBAC:** ClusterRole with cluster-wide permissions
- **Use Case:** Small clusters, single-tenant environments

**When to Use Namespace-Scoped:**
- Multi-tenant clusters
- Production environments
- Security-critical deployments
- Defense in depth required

See [docs/design/security-analysis.md](docs/design/security-analysis.md) for detailed security assessment and recommendations.

## Documentation

Complete documentation is available in the [docs/](docs/) directory:

### For Operators
- [Installation Guide](#installation) - Setup instructions
- [Configuration Reference](#configuration) - All environment variables
- [Examples](examples/README.md) - Sample configurations

### For Developers
- [Architecture Overview](docs/design/security-analysis.md) - System design
- [Testing Guide](#testing) - Running tests
- [Historical Planning](docs/archive/) - Archived planning documents (2024-2025)

### Additional Resources
- [CHANGELOG.md](CHANGELOG.md) - Version history
- [ROADMAP.md](ROADMAP.md) - Future plans
- [Rate Limiting Design](docs/design/rate-limiting.md) - Detailed rate limiting architecture

## Testing

The project includes comprehensive test coverage:

```bash
# Run all tests with race detector
make test

# Generate coverage report
make test-coverage

# Run tests in verbose mode
make test-verbose
```

**Test Coverage:**
- `internal/cache` - 100% coverage
- `internal/circuitbreaker` - 100% coverage
- `internal/config` - 98.5% coverage
- `internal/update` - 82.2% coverage

## Next Steps

**Phase 5: Production Enhancements** (Future)
- Integration and e2e tests
- GoDoc API reference documentation
- Release process documentation

## License

MIT License - See LICENSE file for details

## Contributing

Contributions are welcome! Please open an issue or pull request for any bugs, features, or improvements.
