# Namespace-Scoped Deployment Examples

This directory contains examples for deploying the Azure Key Vault Sync Controller in namespace-scoped mode for improved security.

## Security Benefits

Namespace-scoped deployment provides:
- **90%+ reduction in blast radius** - Controller compromise affects only single namespace
- **Limited token creation** - Can only impersonate ServiceAccounts in same namespace
- **Namespace isolation** - Cannot access resources in other namespaces
- **Least privilege** - Role instead of ClusterRole permissions

## Deployment Options

### Option 1: Single Namespace Deployment

Deploy controller to one specific namespace:

```bash
# Set your target namespace
export NAMESPACE=production

# Deploy RBAC and controller
kubectl apply -f - <<EOF
$(sed "s/\${NAMESPACE}/$NAMESPACE/g" ../../deploy/rbac-namespaced.yaml)
EOF

kubectl apply -f - <<EOF
$(sed "s/\${NAMESPACE}/$NAMESPACE/g" ../../deploy/deployment-namespaced.yaml)
EOF
```

### Option 2: Multi-Namespace Deployment

Deploy controller to multiple namespaces:

```bash
# Deploy to each namespace that needs secret sync
for ns in production staging development; do
  echo "Deploying to namespace: $ns"

  kubectl apply -f - <<EOF
$(sed "s/\${NAMESPACE}/$ns/g" ../../deploy/rbac-namespaced.yaml)
EOF

  kubectl apply -f - <<EOF
$(sed "s/\${NAMESPACE}/$ns/g" ../../deploy/deployment-namespaced.yaml)
EOF
done
```

### Option 3: Kustomize-Based Deployment

Use Kustomize for managing namespace-specific configurations:

```yaml
# kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: production  # Target namespace

resources:
  - ../../deploy/rbac-namespaced.yaml
  - ../../deploy/deployment-namespaced.yaml

# Replaces ${NAMESPACE} with namespace value
replacements:
  - source:
      kind: Kustomization
      fieldPath: metadata.namespace
    targets:
      - select:
          kind: ServiceAccount
        fieldPaths:
          - metadata.namespace
      - select:
          kind: Role
        fieldPaths:
          - metadata.namespace
      - select:
          kind: RoleBinding
        fieldPaths:
          - metadata.namespace
          - subjects.[kind=ServiceAccount].namespace
      - select:
          kind: Deployment
        fieldPaths:
          - metadata.namespace
```

## Verification

### Check Controller Status

```bash
# Check controller deployment in target namespace
kubectl get deployment -n production azure-keyvault-sync-controller

# Check controller logs
kubectl logs -n production -l app=azure-keyvault-sync-controller --tail=50
```

### Verify Namespace Isolation

```bash
# Controller should only sync resources in its own namespace
kubectl get secretproviderclass -n production \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.annotations.azure-keyvault-sync/last-sync}{"\n"}{end}'

# Resources in other namespaces should NOT be synced by this controller
kubectl get secretproviderclass -n staging \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.annotations.azure-keyvault-sync/last-sync}{"\n"}{end}'
```

### Test RBAC Isolation

```bash
# Verify controller cannot access other namespaces
kubectl auth can-i get secretproviderclasses \
  --namespace=production \
  --as=system:serviceaccount:production:azure-keyvault-sync-controller
# Should return: yes

kubectl auth can-i get secretproviderclasses \
  --namespace=staging \
  --as=system:serviceaccount:production:azure-keyvault-sync-controller
# Should return: no
```

## Migration from Cluster-Wide

If you're migrating from cluster-wide deployment:

1. **Identify namespaces** with sync-enabled SecretProviderClasses
2. **Deploy namespace-scoped** controllers to each namespace
3. **Verify** namespace-scoped controllers are working
4. **Remove** cluster-wide deployment:

```bash
kubectl delete deployment azure-keyvault-sync-controller -n kube-system
kubectl delete clusterrolebinding azure-keyvault-sync-controller
kubectl delete clusterrole azure-keyvault-sync-controller
kubectl delete serviceaccount azure-keyvault-sync-controller -n kube-system
```

## Resource Requirements

Per namespace:
- **Memory:** 64Mi request, 128Mi limit
- **CPU:** 100m request, 200m limit

For 10 namespaces:
- **Total Memory:** 640Mi request, 1280Mi limit
- **Total CPU:** 1000m (1 core) request, 2000m (2 cores) limit

## When to Use Namespace-Scoped

Use namespace-scoped deployment when:
- Multi-tenant cluster
- Many namespaces (> 5)
- Security is critical priority
- Defense in depth required
- Namespace-level isolation needed

Use cluster-wide deployment when:
- Small number of namespaces (< 5)
- Single-tenant cluster
- Simple deployment preferred
- Resource constraints

## Troubleshooting

### Controller Not Syncing

Check if WATCH_NAMESPACE is set correctly:

```bash
kubectl get deployment azure-keyvault-sync-controller -n production \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="WATCH_NAMESPACE")].valueFrom.fieldRef.fieldPath}'
```

Should output: `metadata.namespace`

### Cannot Access Other Namespaces

This is expected and correct behavior. Each controller is isolated to its own namespace for security.

### Multiple Controllers Interfering

This should not happen. Each controller only watches its own namespace and has no knowledge of other controllers.

## Security Considerations

1. **Separate ServiceAccounts** - Each namespace has its own ServiceAccount
2. **Role-based permissions** - No cluster-wide access
3. **Token scoping** - Can only impersonate SAs in same namespace
4. **Blast radius** - Compromise limited to single namespace
5. **Defense in depth** - Namespace boundaries as security layer

## See Also

- [Security Analysis](../../SECURITY-ANALYSIS.md)
- [Planning Document](../../planning/namespace-scoping.md)
- [Deployment Manifests](../../deploy/)
