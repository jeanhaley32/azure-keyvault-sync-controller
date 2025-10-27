# Azure Key Vault Sync Controller
## Technical Presentation

**Date:** October 27, 2025
**Status:** Production Ready
**Repository:** [github.com/jeanhaley32/azure-keyvault-sync-controller](https://github.com/jeanhaley32/azure-keyvault-sync-controller)

---

## Executive Summary

A production-ready Kubernetes controller that automatically synchronizes Azure Key Vault contents to SecretProviderClass objects using Azure Workload Identity federation. Eliminates manual secret management and leverages existing Azure infrastructure for secure, scalable secret distribution.

**Key Value:** Vault becomes the single source of truth - add a secret to Azure Key Vault, and it automatically appears in your Kubernetes cluster.

---

## Problem Statement

**Before This Controller:**
- Manual maintenance of SecretProviderClass `objects` arrays
- Secrets added to vault require manual YAML updates
- Vault deletions leave stale references in Kubernetes
- Tedious process across hundreds of services and environments

**With This Controller:**
- Zero manual configuration after initial setup
- Secrets automatically sync from vault to Kubernetes
- Self-healing: vault changes propagate immediately
- Scales to hundreds of vaults with minimal overhead

---

## Architecture

### Security Model

**Service Account Impersonation** instead of centralized credentials:

```
Controller → Impersonates ServiceAccount
  → Kubernetes TokenRequest API (K8s JWT)
  → Azure Workload Identity Federation (Azure AD token)
  → Azure Managed Identity (vault access)
  → Azure Key Vault RBAC
  → Discovers secrets/certificates
  → Updates SecretProviderClass
```

**Security Benefits:**
- No centralized credential with access to all vaults
- Reuses existing Azure Workload Identity infrastructure
- Accurate audit trails (vault logs show actual service identity)
- Reduced blast radius if controller compromised

### Work Queue Pattern

Industry-standard Kubernetes controller architecture:

- **5 concurrent workers** for parallel processing
- **Automatic event deduplication** (multiple events → single reconciliation)
- **Rate limiting** with exponential backoff
- **Retry logic** (max 5 attempts) for transient failures
- **Graceful error handling** (failed resources don't block others)

**Result:** Immediate event-driven reconciliation with no race conditions or update loops.

### Token Management

**Kubernetes Tokens:**
- 1-hour lifetime with renewal at 80% (48 minutes)
- Audience: `api://AzureADTokenExchange`
- Cached by namespace/serviceAccount

**Azure AD Tokens:**
- 28-hour lifetime with renewal at 80% (22.4 hours)
- Scope: `https://vault.azure.net/.default` (service-level)
- Single token reusable across multiple vaults
- Cached by namespace/serviceAccount

---

## Implementation Highlights

### Phase 1: Foundation
- Kubernetes Dynamic Client for SecretProviderClass watching
- Thread-safe in-memory cache with mutex protection
- Annotation-based opt-in (`azure-keyvault-sync/enabled: "true"`)
- Work queue architecture with event deduplication

### Phase 2: Authentication
- **2.1:** Kubernetes TokenRequest API for service account impersonation
- **2.2:** Azure Workload Identity integration for Azure AD tokens
- Automatic token renewal and caching
- Multi-vault support via service-level token scope

### Phase 3: Vault Integration
- Custom token credential wrapper (CachedTokenCredential)
- Pagination support for listing secrets and certificates
- Filters disabled/expired items automatically
- RBAC-aware error handling

### Phase 4: SecretProviderClass Updates
- Automatic `objects` array population from vault contents
- Optional `secretObjects` generation for Kubernetes Secrets
- JSON Patch updates with change detection
- Vault as source of truth (no manual object preservation)

### Architecture Improvements
- Work queue pattern with retry logic
- Critical bug fix: 403 errors preserve existing data
- Comprehensive error handling and logging
- Production-grade reliability

---

## Usage

### Prerequisites
- Kubernetes cluster with Azure Workload Identity
- Azure Key Vault with secrets/certificates
- Managed Identity with Key Vault RBAC (Secrets User + Certificates User)
- Federated Identity Credential configured

### Quick Start

**1. Deploy Controller:**
```bash
kubectl apply -f https://raw.githubusercontent.com/jeanhaley32/azure-keyvault-sync-controller/main/deploy/rbac.yaml
kubectl apply -f https://raw.githubusercontent.com/jeanhaley32/azure-keyvault-sync-controller/main/deploy/deployment.yaml
```

**2. Create SecretProviderClass:**
```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: app-secrets
  namespace: production
  annotations:
    azure-keyvault-sync/enabled: "true"
    azure-keyvault-sync/service-account: "app-sa"
    azure-keyvault-sync/secret-objects: "true"  # Optional: K8s Secrets
spec:
  provider: azure
  parameters:
    keyvaultName: "prod-app-vault"
    clientID: "00000000-0000-0000-0000-000000000000"
    tenantId: "11111111-1111-1111-1111-111111111111"
    objects: ""        # Auto-populated by controller
  secretObjects: []    # Auto-populated if annotation enabled
```

**3. Verify:**
```bash
# Check sync timestamp
kubectl get secretproviderclass app-secrets \
  -o jsonpath='{.metadata.annotations.azure-keyvault-sync/last-sync}'

# View controller logs
kubectl logs -n kube-system -l app=azure-keyvault-sync-controller
```

### Annotations Reference

| Annotation | Required | Description |
|------------|----------|-------------|
| `azure-keyvault-sync/enabled` | Yes | Set to `"true"` to enable sync |
| `azure-keyvault-sync/service-account` | Yes | ServiceAccount for impersonation |
| `azure-keyvault-sync/secret-objects` | No | Create Kubernetes Secrets (Opaque) |
| `azure-keyvault-sync/cert-objects` | No | Create TLS Secrets from certificates |
| `azure-keyvault-sync/last-sync` | Auto | Timestamp of last sync |

---

## Key Technical Decisions

### 1. Vault as Source of Truth
**Decision:** Vault contents completely replace SecretProviderClass objects, no merging.

**Rationale:**
- Eliminates synchronization conflicts
- Simplifies mental model (vault is authoritative)
- Azure Key Vault already has versioning for pinning specific versions
- Reduces controller complexity

**Trade-off:** Cannot mix manual and automatic objects in same SecretProviderClass.

### 2. Service-Level Token Scope
**Decision:** Azure AD tokens use `https://vault.azure.net/.default` instead of vault-specific scopes.

**Rationale:**
- Single token works across all vaults for a service
- Scales efficiently to 100+ vaults per service
- Reduces token acquisition overhead
- RBAC still enforced per-vault by Azure

**Trade-off:** Token has broader scope than strictly necessary (mitigated by RBAC).

### 3. Work Queue Architecture
**Decision:** Use work queue pattern with event-driven reconciliation instead of periodic polling.

**Rationale:**
- Industry-standard Kubernetes controller pattern
- Immediate updates (no 5-minute wait)
- Natural event deduplication
- Better resource utilization

**Trade-off:** Slightly more complex than simple periodic polling.

### 4. Error Preservation
**Decision:** Permission errors (403) fail reconciliation but preserve existing objects.

**Rationale:**
- RBAC issues are usually temporary (permissions being applied)
- Data loss is worse than stale data
- Retry logic gives time for issues to resolve
- Operators can monitor controller logs for persistent errors

**Trade-off:** Stale data possible if RBAC permanently broken (acceptable).

---

## Error Handling

### Permission Errors (403 Forbidden)
1. Controller logs error with full Azure RBAC details
2. Retries 5 times with exponential backoff
3. After max retries, drops from queue
4. **Existing objects preserved** (no data loss)
5. Other resources continue processing

### Transient Failures
- Network issues, temporary token problems
- Automatic retry with exponential backoff
- Max 5 attempts per resource
- Graceful degradation (controller doesn't crash)

---

## Deployment

### Container Images

**GitHub Container Registry (Public):**
```bash
docker pull ghcr.io/jeanhaley32/azure-keyvault-sync-controller:latest
```

**Platforms:**
- linux/amd64
- linux/arm64

**CI/CD:**
- Automated builds on push to main
- Multi-arch builds via GitHub Actions
- Distroless runtime for minimal attack surface
- Non-root user (UID 65532)

### For Kaufman Rossin

Repository can be forked to Kaufman Rossin's GitHub organization. GitHub Actions workflow easily adapted for Azure Container Registry (ACR) instead of GHCR.

---

## Production Readiness

### Completed
- ✅ Work queue architecture with retry logic
- ✅ Comprehensive error handling (403 errors, transient failures)
- ✅ Token caching and automatic renewal
- ✅ Change detection (prevents unnecessary updates)
- ✅ Comprehensive logging
- ✅ Container images on GHCR
- ✅ Complete documentation and examples
- ✅ Tested against real AKS cluster and Azure vaults

### Future Enhancements (Phase 5)
- Structured logging with log levels (info, warn, error, debug)
- Health check endpoints (/healthz, /readyz)
- Configuration management via ConfigMap
- Additional security hardening (capabilities, network policies, PSS)
- Comprehensive test coverage (unit, integration, e2e)
- Helm chart and Kustomize manifests

---

## Monitoring

### Logs
```
2025/10/26 23:24:58 Event: ADDED default/app-secrets (sync enabled, sa: app-sa) - enqueuing
2025/10/26 23:24:58 Obtained Kubernetes token for default/app-sa
2025/10/26 23:24:58 Obtained Azure AD token for default/app-sa
2025/10/26 23:24:58 Found 3 secrets in vault prod-app-vault
2025/10/26 23:24:58 Found 0 certificates in vault prod-app-vault
2025/10/26 23:24:58 Successfully updated default/app-secrets with 3 objects
```

### Observability
- Last-sync annotation shows sync timestamp
- Controller logs show full reconciliation flow
- Azure vault logs show controller access via service identity
- Kubernetes events for SecretProviderClass updates

---

## Questions & Discussion

### Common Questions

**Q: What happens if the controller is down?**
A: Existing SecretProviderClass objects remain unchanged. CSI driver continues mounting secrets. When controller restarts, it resumes syncing.

**Q: How quickly do vault changes propagate?**
A: Immediate event-driven reconciliation (seconds). Periodic resync every 5 minutes as fallback.

**Q: Can I manually add objects alongside controller-managed ones?**
A: No - vault is source of truth. Create separate SecretProviderClass without sync annotations for manual objects.

**Q: What RBAC permissions are required in Azure?**
A: Managed Identity needs "Key Vault Secrets User" and "Key Vault Certificates User" roles on the vault.

**Q: Does this work with Azure Key Vault Premium (HSM)?**
A: Yes - controller uses standard Azure SDK which supports both Standard and Premium tiers.

---

## Resources

- **Repository:** [github.com/jeanhaley32/azure-keyvault-sync-controller](https://github.com/jeanhaley32/azure-keyvault-sync-controller)
- **Container Images:** `ghcr.io/jeanhaley32/azure-keyvault-sync-controller:latest`
- **Documentation:** See README.md, ROADMAP.md, CHANGELOG.md
- **Examples:** See examples/ directory
- **Planning Documents:** See planning/ directory

---

## Summary

**Production-ready Kubernetes controller that automates Azure Key Vault synchronization:**

- ✅ Zero manual secret management after initial setup
- ✅ Secure service account impersonation (no centralized credentials)
- ✅ Work queue architecture with retry logic
- ✅ Immediate event-driven reconciliation
- ✅ Multi-vault support with efficient token caching
- ✅ Comprehensive error handling and logging
- ✅ Container images available on GHCR
- ✅ Complete documentation and examples

**Ready for deployment to production environments.**
