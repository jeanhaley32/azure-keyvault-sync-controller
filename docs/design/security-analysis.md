# Security Analysis: Azure Key Vault Sync Controller

**Date:** 2025-10-27
**Status:** Production Security Review
**Reviewer:** Security Assessment

---

## Executive Summary

The Azure Key Vault Sync Controller implements a **service account impersonation model** that significantly reduces security risk compared to centralized credential approaches. The controller demonstrates strong security fundamentals but requires careful operational controls to mitigate specific attack vectors.

**Overall Security Posture:** Good with caveats requiring operational controls

---

## Architecture Overview

### Authentication Chain

```
Controller Pod
  ↓ [1] Watches SecretProviderClass resources (cluster-wide)
  ↓ [2] Impersonates target ServiceAccount via TokenRequest API
  ↓ [3] Exchanges K8s token for Azure AD token (Workload Identity)
  ↓ [4] Accesses Azure Key Vault using Azure AD token
  ↓ [5] Updates SecretProviderClass with vault contents
```

### Key Security Design Decisions

1. **Service Account Impersonation** - No centralized vault credential
2. **Token Caching** - Tokens cached in-memory (not persisted)
3. **Vault as Source of Truth** - Overwrites existing SecretProviderClass objects
4. **Cluster-Wide RBAC** - ClusterRole with broad permissions

---

## RBAC Permissions Analysis

### Granted Permissions

```yaml
# SecretProviderClass Management
- apiGroups: ["secrets-store.csi.x-k8s.io"]
  resources: ["secretproviderclasses"]
  verbs: ["get", "list", "watch", "update", "patch"]

# Token Acquisition
- apiGroups: [""]
  resources: ["serviceaccounts/token"]
  verbs: ["create"]
```

### Risk Assessment: MEDIUM-HIGH

**Concerns:**

1. **Cluster-Wide Scope**
   - ClusterRole grants access to ALL namespaces
   - No namespace-level isolation
   - Single point of compromise affects entire cluster

2. **ServiceAccount Token Creation (ANY ServiceAccount)**
   - `serviceaccounts/token` permission allows token creation for ANY service account in ANY namespace
   - This is the highest privilege escalation risk
   - Controller can impersonate ANY workload identity configured with Azure Workload Identity

3. **SecretProviderClass Modification**
   - Can modify ANY SecretProviderClass cluster-wide
   - Could redirect secret mounts to malicious vaults
   - Could expose secrets to unauthorized workloads

**What an attacker with controller access could do:**

1. Impersonate ANY ServiceAccount that has Azure Workload Identity
2. Access ANY Azure Key Vault that the impersonated identity can access
3. Modify SecretProviderClass objects to:
   - Redirect workloads to attacker-controlled vaults
   - Expose additional secrets
   - Modify secret mappings

---

## Authentication & Token Security

### Kubernetes Token Acquisition

**Method:** `serviceaccounts/token` subresource (TokenRequest API)

**Security Properties:**
- ✅ Token lifetime: 1 hour (good - short-lived)
- ✅ Token audience: `api://AzureADTokenExchange` (limited scope)
- ✅ Cached in memory only (not persisted to disk)
- ⚠️ Can impersonate ANY ServiceAccount (high privilege)

**Code Review (token.go:49-76):**
```go
// No validation that controller should impersonate this SA
// No audit logging of impersonation
// No rate limiting on token requests
```

### Azure AD Token Exchange

**Method:** Azure Workload Identity federation

**Security Properties:**
- ✅ Federated identity credential validation by Azure
- ✅ Token lifetime: ~28 hours (Azure-managed)
- ✅ Scope: `https://vault.azure.net/.default` (service-level)
- ✅ Cached in memory with automatic renewal
- ⚠️ Single token reusable across multiple vaults (efficiency vs. isolation)

### Token Storage

**Risk Assessment: LOW**
- Tokens stored in memory only (no disk persistence)
- Memory cleared on pod restart/crash
- No token exposure in logs (snippets only for debug)

---

## Attack Vectors

### 1. Controller Pod Compromise (CRITICAL)

**Scenario:** Attacker gains exec access to controller pod

**Attack Path:**
```
1. kubectl exec into controller pod
2. Access in-memory token caches (if debugger/tools available)
3. Enumerate all cached ServiceAccount identities
4. Use cached Azure tokens to access vaults directly
```

**Impact:** Full access to all vaults that synced ServiceAccounts can access

**Mitigations:**
- ✅ Pod runs as non-root (UID 65534)
- ✅ Read-only root filesystem
- ✅ All capabilities dropped
- ✅ No privilege escalation allowed
- ⚠️ Memory still readable if attacker has exec access

**Recommendation:** Implement PodSecurityPolicy/Pod Security Standards enforcement

### 2. Privilege Escalation via Token Impersonation (HIGH)

**Scenario:** Attacker compromises controller and impersonates high-privilege ServiceAccounts

**Attack Path:**
```
1. Compromise controller pod
2. Identify ServiceAccounts with Azure Workload Identity
3. Request token for high-privilege ServiceAccount (no restrictions)
4. Exchange for Azure AD token
5. Access Azure Key Vaults beyond intended scope
```

**Impact:** Lateral movement to other workloads' vaults

**Current Controls:**
- ❌ No validation that controller should impersonate target SA
- ❌ No audit logging of impersonation attempts
- ❌ No rate limiting on token requests

**Recommendation:** Implement admission control or policy validation

### 3. SecretProviderClass Manipulation (HIGH)

**Scenario:** Attacker modifies SecretProviderClass to expose secrets

**Attack Path:**
```
1. Compromise controller OR gain update permission
2. Modify SecretProviderClass to point to attacker-controlled vault
3. OR add additional secret mappings
4. OR change secretObjects to create Secrets in attacker namespace
```

**Impact:** Secret exposure or redirection

**Current Controls:**
- ✅ Controller validates sync annotations before processing
- ⚠️ No integrity checking of SecretProviderClass modifications
- ⚠️ No alerting on unexpected changes

**Recommendation:** Implement admission webhooks for SecretProviderClass validation

### 4. Vault Content Manipulation (MEDIUM)

**Scenario:** Attacker with Azure vault access adds malicious secrets

**Attack Path:**
```
1. Gain access to Azure Key Vault (outside Kubernetes)
2. Add malicious secrets to vault
3. Controller automatically syncs malicious secrets to SecretProviderClass
4. CSI driver mounts malicious secrets to workload pods
```

**Impact:** Malicious secret injection via vault source of truth model

**Current Controls:**
- ⚠️ Vault is single source of truth (by design)
- ⚠️ No secret content validation
- ⚠️ No secret naming restrictions

**Recommendation:** Azure-side vault access controls and monitoring

### 5. Denial of Service (MEDIUM)

**Scenario:** Resource exhaustion or controller crash

**Attack Paths:**
- Create many SecretProviderClasses with sync enabled
- Target large vaults with hundreds of secrets
- Cause repeated authentication failures
- Exhaust work queue or rate limiter

**Current Controls:**
- ✅ Work queue with rate limiting
- ✅ Resource limits (128Mi memory, 200m CPU)
- ✅ Max retry attempts (5)
- ⚠️ No limit on number of sync-enabled resources

**Recommendation:** Consider resource quotas for sync-enabled SecretProviderClasses

### 6. Information Disclosure via Logs (LOW)

**Scenario:** Sensitive information leaked in logs

**Code Review:**
- ✅ Token snippets only (first 5 + last 5 chars)
- ✅ No full tokens logged
- ⚠️ ClientIDs, tenantIDs, vault names logged (non-secret but useful for recon)
- ⚠️ Service account names logged

**Impact:** Low - metadata exposure only

---

## Deployment Security Configuration

### Pod Security Context

**Review of deployment.yaml:**

```yaml
securityContext:
  allowPrivilegeEscalation: false    # ✅ Good
  readOnlyRootFilesystem: true       # ✅ Good
  runAsNonRoot: true                 # ✅ Good
  runAsUser: 65534                   # ✅ Good (nobody)
  capabilities:
    drop:
      - ALL                          # ✅ Good
  seccompProfile:
    type: RuntimeDefault             # ✅ Good
```

**Assessment:** Excellent pod-level security controls

**Missing:**
- ❌ No network policies defined
- ❌ No resource quotas
- ❌ No Pod Security Standards label enforcement

### Container Image Security

**Image:** `ghcr.io/jeanhaley32/azure-keyvault-sync-controller:latest`

**Concerns:**
- ⚠️ Uses `:latest` tag (not immutable)
- ⚠️ No image signature verification
- ⚠️ No SBOM or vulnerability scanning mentioned

**Recommendation:**
- Use specific version tags or SHA256 digests
- Implement image signing and verification
- Regular vulnerability scanning

### Health Checks

**Review:**
- ✅ Liveness probe on /healthz
- ✅ Readiness probe on /readyz
- ✅ Proper timeouts and failure thresholds

**Assessment:** Good operational health monitoring

---

## Audit & Observability

### What is Currently Logged

**Positive:**
- Token acquisition events
- Vault sync operations
- Error conditions and retries
- SecretProviderClass updates

**Gaps:**
- ❌ No audit trail of which ServiceAccounts were impersonated
- ❌ No tracking of vault access patterns
- ❌ No alerting on suspicious activity
- ❌ No metrics for security monitoring

### Recommendation: Security Instrumentation

Implement structured logging with:
- ServiceAccount impersonation events (who, when, why)
- Vault access attempts (success/failure)
- SecretProviderClass modification events
- Rate limiting hits
- Authentication failures

---

## Blast Radius Analysis

### If Controller is Compromised

**Direct Access:**
1. ✅ No centralized credential (good - no single key to all vaults)
2. ⚠️ Can impersonate ANY ServiceAccount with Azure Workload Identity
3. ⚠️ Can access ANY vault that synced workloads can access
4. ⚠️ Can modify ANY SecretProviderClass cluster-wide

**Blast Radius:** HIGH - Cluster-wide impact

**Comparison to Centralized Credential:**
- Better: No single credential to rotate if compromised
- Better: Audit trail shows actual service identity in Azure logs
- Worse: Broad Kubernetes RBAC permissions still create large blast radius

---

## Compliance & Regulatory Considerations

### Audit Trail

**Azure Side:**
- ✅ Key Vault access logs show impersonated identity (good attribution)
- ✅ Azure AD sign-in logs track token exchanges

**Kubernetes Side:**
- ⚠️ Limited audit trail for controller actions
- ⚠️ No record of why controller impersonated specific ServiceAccount

### Least Privilege

**Current State:** Violates least privilege principle
- Controller has cluster-wide token creation
- Controller can modify any SecretProviderClass
- No namespace isolation

**Recommendation:** Consider namespace-scoped deployments if possible

### Secret Access Patterns

**Concern:** Vault-as-source-of-truth means no secret-level access control
- All secrets in vault automatically synced
- Cannot selectively exclude secrets
- No approval workflow for secret additions

---

## Recommendations

### Priority 1: Critical (Implement Immediately)

1. **Namespace Isolation**
   - Consider deploying controller per-namespace with Role (not ClusterRole)
   - OR implement admission webhook to validate ServiceAccount impersonation
   - Limits blast radius significantly

2. **Security Monitoring**
   - Implement structured audit logging
   - Alert on:
     - Token creation for unexpected ServiceAccounts
     - SecretProviderClass modifications
     - Authentication failures
   - Export logs to SIEM

3. **Immutable Image Tags**
   - Stop using `:latest` tag
   - Use SHA256 digests: `ghcr.io/...:sha256-<hash>`
   - Implement image signing

4. **Admission Control**
   - Validating webhook for SecretProviderClass modifications
   - Enforce annotation patterns
   - Prevent unauthorized sync enablement

### Priority 2: High (Implement Soon)

5. **Network Policies**
   - Restrict controller egress to:
     - Kubernetes API server
     - Azure metadata endpoint (169.254.169.254)
     - Azure Key Vault endpoints (*.vault.azure.net)
   - Deny all other egress

6. **Pod Security Standards**
   - Enforce `restricted` profile on kube-system namespace
   - Verify controller deployment passes PSS validation

7. **Resource Quotas**
   - Limit number of sync-enabled SecretProviderClasses
   - Prevent DoS via resource exhaustion

8. **Image Scanning**
   - Implement CI/CD vulnerability scanning
   - Generate and publish SBOM
   - Regular security updates

### Priority 3: Medium (Consider)

9. **Service Account Allowlist**
   - Add annotation to controller deployment listing allowed ServiceAccounts
   - Reject impersonation attempts for non-allowlisted SAs
   - Reduces lateral movement risk

10. **Secret Content Validation**
    - Implement webhook to validate secret naming conventions
    - Enforce vault organization policies
    - Prevent accidental exposure via naming

11. **Rate Limiting**
    - Add per-ServiceAccount rate limits for token requests
    - Alert on excessive token acquisition attempts

12. **Regular Security Audits**
    - Quarterly review of:
      - Sync-enabled resources
      - ServiceAccount permissions
      - Vault access patterns
      - Controller logs

---

## Operational Security Checklist

### Before Deployment

- [ ] Review and approve all ServiceAccounts that will use sync
- [ ] Verify Azure Workload Identity federated credentials are correctly scoped
- [ ] Verify Azure Key Vault RBAC permissions follow least privilege
- [ ] Configure network policies for controller pod
- [ ] Enable audit logging in Kubernetes
- [ ] Enable Azure Key Vault diagnostic logging
- [ ] Document incident response procedures

### Ongoing Operations

- [ ] Regular review of sync-enabled SecretProviderClasses
- [ ] Monitor controller logs for anomalies
- [ ] Review Azure Key Vault access logs
- [ ] Track which ServiceAccounts have been impersonated
- [ ] Regular vulnerability scanning of controller image
- [ ] Rotate ServiceAccount credentials periodically (if possible)

### Incident Response

**If controller compromise suspected:**
1. Scale controller deployment to 0 replicas immediately
2. Review Kubernetes audit logs for suspicious token requests
3. Review Azure Key Vault access logs
4. Identify which ServiceAccounts were impersonated
5. Assess if any vault contents were modified
6. Rotate federated identity credentials for affected ServiceAccounts
7. Review all SecretProviderClass modifications
8. Deploy patched/validated controller version

---

## Risk Matrix

| Risk | Likelihood | Impact | Severity | Mitigation Priority |
|------|-----------|---------|----------|-------------------|
| Controller pod compromise | Medium | High | HIGH | P1 |
| Privilege escalation via token impersonation | Medium | High | HIGH | P1 |
| SecretProviderClass manipulation | Low | High | MEDIUM | P2 |
| Vault content manipulation | Medium | Medium | MEDIUM | P3 (Azure-side) |
| DoS via resource exhaustion | Low | Medium | LOW | P2 |
| Information disclosure via logs | Low | Low | LOW | P3 |

---

## Conclusion

The Azure Key Vault Sync Controller implements a **more secure model** than centralized credential approaches, but the broad RBAC permissions create significant privilege escalation risks if the controller is compromised.

**Key Strengths:**
- Service account impersonation (no centralized secret)
- Strong pod security context
- Short-lived tokens
- In-memory token storage only

**Key Weaknesses:**
- Cluster-wide token creation permission
- No validation of impersonation legitimacy
- Limited audit trail
- Large blast radius if compromised

**Recommendation:** Deploy with enhanced monitoring, admission control, and namespace isolation where possible. The controller is suitable for production use with operational controls in place.

---

## References

- Kubernetes TokenRequest API: https://kubernetes.io/docs/reference/kubernetes-api/authentication-resources/token-request-v1/
- Azure Workload Identity: https://azure.github.io/azure-workload-identity/
- Pod Security Standards: https://kubernetes.io/docs/concepts/security/pod-security-standards/
- Kubernetes RBAC: https://kubernetes.io/docs/reference/access-authn-authz/rbac/
- Azure Key Vault Security: https://learn.microsoft.com/en-us/azure/key-vault/general/security-features
