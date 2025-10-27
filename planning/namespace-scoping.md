# Namespace Scoping Implementation Plan

**Date:** 2025-10-27
**Branch:** namespace-scoping
**Status:** Planning

---

## Objective

Implement namespace-scoped RBAC for the Azure Key Vault Sync Controller to reduce blast radius and improve security posture.

**Security Impact:** Reduces cluster-wide token creation permission to namespace-only, significantly limiting privilege escalation risks.

---

## Current State Analysis

### Current RBAC (Cluster-Wide)

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: azure-keyvault-sync-controller
rules:
  # SecretProviderClass permissions (ALL NAMESPACES)
  - apiGroups: ["secrets-store.csi.x-k8s.io"]
    resources: ["secretproviderclasses"]
    verbs: ["get", "list", "watch", "update", "patch"]

  # Token request permissions (ANY SERVICE ACCOUNT)
  - apiGroups: [""]
    resources: ["serviceaccounts/token"]
    verbs: ["create"]
```

**Security Issues:**
- ❌ Can impersonate ServiceAccounts in ANY namespace
- ❌ Can modify SecretProviderClass in ANY namespace
- ❌ Single point of compromise affects entire cluster

### Current Deployment

- **Namespace:** kube-system
- **Scope:** Cluster-wide watcher
- **Replicas:** 1 (single instance for entire cluster)

---

## Target State Design

### Namespace-Scoped RBAC

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role  # Changed from ClusterRole
metadata:
  name: azure-keyvault-sync-controller
  namespace: {{ NAMESPACE }}  # Deployed per-namespace
rules:
  # SecretProviderClass permissions (THIS NAMESPACE ONLY)
  - apiGroups: ["secrets-store.csi.x-k8s.io"]
    resources: ["secretproviderclasses"]
    verbs: ["get", "list", "watch", "update", "patch"]

  # Token request permissions (THIS NAMESPACE ONLY)
  - apiGroups: [""]
    resources: ["serviceaccounts/token"]
    verbs: ["create"]
```

**Security Improvements:**
- ✅ Can only impersonate ServiceAccounts in same namespace
- ✅ Can only modify SecretProviderClass in same namespace
- ✅ Compromise of one controller doesn't affect other namespaces
- ✅ Blast radius limited to single namespace

### Deployment Model

**Option A: Per-Namespace Deployment (Recommended)**
- One controller instance per namespace that needs sync
- Each controller only watches its own namespace
- Complete isolation between namespaces

**Option B: Configurable Scope (Flexible)**
- Support both cluster-wide and namespace-scoped via configuration
- Allow users to choose deployment model
- Backward compatible with existing deployments

**Recommendation:** Implement Option A with documentation for migration

---

## Implementation Approach

### Phase 1: RBAC Changes

**Files to Modify:**
- `deploy/rbac.yaml` - Convert ClusterRole → Role, ClusterRoleBinding → RoleBinding

**Changes:**
1. Create `deploy/rbac-namespaced.yaml` (new file for namespace-scoped)
2. Keep `deploy/rbac.yaml` for backward compatibility
3. Add template variables for namespace

**Example Structure:**
```yaml
# deploy/rbac-namespaced.yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: azure-keyvault-sync-controller
  namespace: ${NAMESPACE}  # Template variable
  labels:
    app: azure-keyvault-sync-controller

---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role  # Changed from ClusterRole
metadata:
  name: azure-keyvault-sync-controller
  namespace: ${NAMESPACE}  # Template variable
  labels:
    app: azure-keyvault-sync-controller
rules:
  - apiGroups: ["secrets-store.csi.x-k8s.io"]
    resources: ["secretproviderclasses"]
    verbs: ["get", "list", "watch", "update", "patch"]
  - apiGroups: [""]
    resources: ["serviceaccounts/token"]
    verbs: ["create"]

---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding  # Changed from ClusterRoleBinding
metadata:
  name: azure-keyvault-sync-controller
  namespace: ${NAMESPACE}  # Template variable
  labels:
    app: azure-keyvault-sync-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role  # Changed from ClusterRole
  name: azure-keyvault-sync-controller
subjects:
  - kind: ServiceAccount
    name: azure-keyvault-sync-controller
    namespace: ${NAMESPACE}  # Template variable
```

### Phase 2: Deployment Changes

**Files to Modify:**
- `deploy/deployment.yaml` - Add namespace configuration

**Changes:**
1. Add `WATCH_NAMESPACE` environment variable
2. Update namespace from kube-system to template variable
3. Add documentation for per-namespace deployment

**Example:**
```yaml
env:
  - name: WATCH_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace  # Auto-populate from pod namespace
```

### Phase 3: Controller Code Changes

**Files to Modify:**
- `controller.go` - Add namespace filtering for watch operations
- `main.go` - Read WATCH_NAMESPACE from environment

**Key Changes:**

1. **main.go - Configuration**
```go
// Read namespace from environment (empty = cluster-wide for backward compat)
watchNamespace := os.Getenv("WATCH_NAMESPACE")

// Pass to controller
ctrl := NewController(client, clientset, config, watchNamespace)
```

2. **controller.go - Namespace-Scoped Watch**
```go
type Controller struct {
    client          dynamic.Interface
    clientset       kubernetes.Interface
    cache           *SecretProviderClassCache
    tokenCache      *TokenCache
    azureTokenCache *AzureTokenCache
    queue           workqueue.TypedRateLimitingInterface[QueueKey]
    gvr             schema.GroupVersionResource
    ctx             context.Context
    healthChecker   *HealthChecker
    config          *Config
    watchNamespace  string  // NEW: namespace to watch (empty = all)
}

func (c *Controller) Run(ctx context.Context) error {
    // ... existing setup ...

    // Create namespace-scoped watch
    var watcher watch.Interface
    var err error

    if c.watchNamespace != "" {
        // Namespace-scoped watch
        slog.Info("Starting namespace-scoped watcher", "namespace", c.watchNamespace)
        watcher, err = c.client.Resource(c.gvr).
            Namespace(c.watchNamespace).  // NEW: scope to namespace
            Watch(ctx, metav1.ListOptions{})
    } else {
        // Cluster-wide watch (backward compatibility)
        slog.Info("Starting cluster-wide watcher")
        watcher, err = c.client.Resource(c.gvr).
            Watch(ctx, metav1.ListOptions{})
    }

    // ... rest of existing code ...
}
```

3. **controller.go - Namespace-Scoped List**
```go
func (c *Controller) resync(ctx context.Context) {
    var list *unstructured.UnstructuredList
    var err error

    if c.watchNamespace != "" {
        // List only in watched namespace
        slog.Info("Performing namespace resync", "namespace", c.watchNamespace)
        list, err = c.client.Resource(c.gvr).
            Namespace(c.watchNamespace).
            List(ctx, metav1.ListOptions{})
    } else {
        // List cluster-wide
        slog.Info("Performing cluster-wide resync")
        list, err = c.client.Resource(c.gvr).
            List(ctx, metav1.ListOptions{})
    }

    // ... rest of existing code ...
}
```

### Phase 4: Documentation Updates

**Files to Update:**
- `README.md` - Document namespace-scoped deployment
- `SECURITY-ANALYSIS.md` - Update with improved security posture
- `examples/` - Add namespace-scoped examples

**Key Documentation Points:**
1. How to deploy per-namespace
2. Migration guide from cluster-wide to namespace-scoped
3. Security benefits of namespace scoping
4. Multi-namespace deployment strategy

---

## Deployment Examples

### Example 1: Single Namespace Deployment

```bash
# Set target namespace
NAMESPACE=production

# Deploy RBAC
kubectl apply -f - <<EOF
$(sed "s/\${NAMESPACE}/$NAMESPACE/g" deploy/rbac-namespaced.yaml)
EOF

# Deploy controller
kubectl apply -f - <<EOF
$(sed "s/\${NAMESPACE}/$NAMESPACE/g" deploy/deployment-namespaced.yaml)
EOF
```

### Example 2: Multi-Namespace Deployment

```bash
# Deploy to multiple namespaces
for ns in production staging development; do
  echo "Deploying to namespace: $ns"
  kubectl apply -f - <<EOF
$(sed "s/\${NAMESPACE}/$ns/g" deploy/rbac-namespaced.yaml)
EOF
  kubectl apply -f - <<EOF
$(sed "s/\${NAMESPACE}/$ns/g" deploy/deployment-namespaced.yaml)
EOF
done
```

### Example 3: Kustomize-Based Deployment

```yaml
# kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: production  # Target namespace

resources:
  - rbac-namespaced.yaml
  - deployment-namespaced.yaml

# Automatically replaces ${NAMESPACE} with namespace value
```

---

## Testing Strategy

### Test Cases

1. **Single Namespace Isolation**
   - Deploy controller in namespace A
   - Create SecretProviderClass in namespace A (should sync)
   - Create SecretProviderClass in namespace B (should NOT sync)
   - Verify controller cannot access namespace B resources

2. **ServiceAccount Token Scoping**
   - Deploy controller in namespace A
   - Attempt to request token for ServiceAccount in namespace A (should succeed)
   - Attempt to request token for ServiceAccount in namespace B (should fail with RBAC error)

3. **Multi-Namespace Deployment**
   - Deploy controller in namespace A
   - Deploy controller in namespace B
   - Verify both controllers work independently
   - Verify no interference between controllers

4. **Backward Compatibility**
   - Deploy with cluster-wide RBAC (no WATCH_NAMESPACE set)
   - Verify cluster-wide behavior still works
   - Verify no breaking changes

### Test Environment

**Setup:**
```bash
# Create test namespaces
kubectl create namespace test-namespace-a
kubectl create namespace test-namespace-b

# Create test SecretProviderClasses
kubectl apply -f examples/basic-sync.yaml -n test-namespace-a
kubectl apply -f examples/basic-sync.yaml -n test-namespace-b

# Deploy namespace-scoped controller to namespace A only
NAMESPACE=test-namespace-a make deploy-namespaced

# Verify isolation
kubectl logs -n test-namespace-a -l app=azure-keyvault-sync-controller
```

---

## Migration Guide (for Existing Deployments)

### Step 1: Assess Current State

```bash
# Check current deployment
kubectl get deployment azure-keyvault-sync-controller -n kube-system

# List all SecretProviderClasses with sync enabled
kubectl get secretproviderclass -A \
  -o jsonpath='{range .items[?(@.metadata.annotations.azure-keyvault-sync/enabled=="true")]}{.metadata.namespace}{"\t"}{.metadata.name}{"\n"}{end}'
```

### Step 2: Plan Namespace Deployments

Identify which namespaces need the controller:
- List of namespaces with sync-enabled SecretProviderClasses
- ServiceAccount → Namespace mapping
- Resource requirements per namespace

### Step 3: Deploy Namespace-Scoped Controllers

```bash
# For each identified namespace
for ns in $(kubectl get secretproviderclass -A \
  -o jsonpath='{.items[?(@.metadata.annotations.azure-keyvault-sync/enabled=="true")].metadata.namespace}' | uniq); do
  echo "Deploying to $ns"
  # Deploy namespace-scoped RBAC and controller
  sed "s/\${NAMESPACE}/$ns/g" deploy/rbac-namespaced.yaml | kubectl apply -f -
  sed "s/\${NAMESPACE}/$ns/g" deploy/deployment-namespaced.yaml | kubectl apply -f -
done
```

### Step 4: Verify Namespace-Scoped Controllers

```bash
# Check all controller deployments
kubectl get deployment -A -l app=azure-keyvault-sync-controller

# Verify sync working in each namespace
for ns in production staging development; do
  echo "Checking $ns:"
  kubectl get secretproviderclass -n $ns \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.annotations.azure-keyvault-sync/last-sync}{"\n"}{end}'
done
```

### Step 5: Remove Cluster-Wide Deployment

```bash
# Once namespace-scoped controllers are verified
kubectl delete deployment azure-keyvault-sync-controller -n kube-system
kubectl delete clusterrolebinding azure-keyvault-sync-controller
kubectl delete clusterrole azure-keyvault-sync-controller
kubectl delete serviceaccount azure-keyvault-sync-controller -n kube-system
```

---

## Security Improvements

### Before (Cluster-Wide)

**Blast Radius:** Entire cluster
- Compromise of controller = access to all namespaces
- Can impersonate any ServiceAccount
- Can modify any SecretProviderClass

**RBAC Scope:**
- ClusterRole with cluster-wide permissions
- Single ServiceAccount with broad access

### After (Namespace-Scoped)

**Blast Radius:** Single namespace
- Compromise of controller = access to one namespace only
- Can impersonate ServiceAccounts in same namespace only
- Can modify SecretProviderClass in same namespace only

**RBAC Scope:**
- Role with namespace-only permissions
- Per-namespace ServiceAccounts with limited access

**Risk Reduction:**
- ✅ 90%+ reduction in blast radius (depends on namespace count)
- ✅ Privilege escalation limited to single namespace
- ✅ Defense in depth (namespace boundaries as security layer)
- ✅ Better alignment with least privilege principle

---

## Trade-offs

### Advantages

1. **Security**
   - Significantly reduced blast radius
   - Namespace-level isolation
   - Limited token creation scope
   - Cannot affect other namespaces

2. **Compliance**
   - Better alignment with least privilege
   - Easier to audit (per-namespace permissions)
   - Clearer security boundaries

3. **Multi-tenancy**
   - Suitable for multi-tenant clusters
   - Namespace owners can deploy own controller
   - No cluster-admin required for deployment

### Disadvantages

1. **Operational Complexity**
   - More controller instances to manage
   - More deployments to maintain
   - Higher resource usage (multiple pods)

2. **Resource Overhead**
   - Each namespace needs dedicated controller pod
   - Memory: 64Mi per namespace (default request)
   - CPU: 100m per namespace (default request)

3. **Initial Setup**
   - More complex deployment (per-namespace)
   - Migration effort for existing deployments
   - Need deployment automation (Helm, Kustomize, etc.)

### When to Use Which Model

**Use Cluster-Wide (Original):**
- Small number of namespaces (< 5)
- Single-tenant cluster
- Trust all workloads in cluster
- Want simple deployment

**Use Namespace-Scoped (New):**
- Multi-tenant cluster
- Many namespaces (> 5)
- Security is critical priority
- Want defense in depth
- Namespace-level isolation required

---

## Implementation Tasks

### Phase 1: Core Implementation
- [ ] Create `deploy/rbac-namespaced.yaml`
- [ ] Create `deploy/deployment-namespaced.yaml`
- [ ] Update `controller.go` with namespace filtering
- [ ] Update `main.go` to read WATCH_NAMESPACE
- [ ] Add environment variable documentation

### Phase 2: Testing
- [ ] Test single namespace deployment
- [ ] Test multi-namespace deployment
- [ ] Test namespace isolation (cannot access other namespaces)
- [ ] Test ServiceAccount token scoping
- [ ] Test backward compatibility (cluster-wide still works)

### Phase 3: Documentation
- [ ] Update README.md with namespace-scoped deployment
- [ ] Update SECURITY-ANALYSIS.md with improved security posture
- [ ] Create migration guide
- [ ] Add namespace-scoped examples
- [ ] Document resource requirements

### Phase 4: Automation
- [ ] Create Kustomize overlays for namespace deployment
- [ ] (Future) Create Helm chart
- [ ] Add Makefile targets for namespace deployment
- [ ] Create deployment scripts

---

## Success Criteria

1. ✅ Controller can be deployed per-namespace with Role (not ClusterRole)
2. ✅ Namespace-scoped controller only watches target namespace
3. ✅ Cannot access resources in other namespaces (verified with RBAC test)
4. ✅ Multiple controllers can run independently in different namespaces
5. ✅ Backward compatibility maintained (cluster-wide deployment still works)
6. ✅ Documentation complete with migration guide
7. ✅ Security analysis updated with improved posture

---

## Next Steps

1. Implement Phase 1 (RBAC changes)
2. Implement controller code changes
3. Test in staging environment
4. Update documentation
5. Create migration guide
6. Merge to main after validation

---

## References

- Kubernetes RBAC: https://kubernetes.io/docs/reference/access-authn-authz/rbac/
- Namespace Scoped Resources: https://kubernetes.io/docs/concepts/overview/working-with-objects/namespaces/
- Security Best Practices: https://kubernetes.io/docs/concepts/security/rbac-good-practices/
