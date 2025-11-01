# Testing Guide for Azure KeyVault Sync Controller

This guide covers how to test the controller on your staging cluster.

## What's Available to Test

### ✅ On Staging (Merged)
1. **Annotation Mode** - Original SPC management with service-account annotation
2. **CRD Mode** - AzureKeyVaultSync CRD with automatic SPC generation (PR #41)
3. **Tag Filtering** - Filter vault secrets by service/environment tags
4. **Metrics & Observability** - Prometheus metrics on port 9090 (PR #42)

### 🆕 On Feature Branch (Phase 6)
5. **Secret Annotation Sync** - Automatic metadata flow from vault tags to Secret annotations
   - Branch: `feature/phase-6-secret-annotation-sync`
   - Enables kubernetes-reflector integration

## Prerequisites

1. **Azure Key Vault** with workload identity configured
2. **Kubernetes ServiceAccount** with Azure workload identity annotations:
   ```yaml
   apiVersion: v1
   kind: ServiceAccount
   metadata:
     name: workload-identity-sa
     namespace: default
     annotations:
       azure.workload.identity/client-id: "<your-client-id>"
       azure.workload.identity/tenant-id: "<your-tenant-id>"
   ```
3. **Secrets Store CSI Driver** installed (required for SecretProviderClass support)

## Testing CRD Mode (Available Now on Staging)

### Step 1: Deploy the Controller

```bash
# Apply CRD definition
kubectl apply -f deploy/crd/azure-keyvault-sync.io_azurekeyvaultsyncs.yaml

# Apply RBAC (cluster-wide)
kubectl apply -f deploy/rbac.yaml

# Deploy controller
kubectl apply -f deploy/deployment.yaml

# Verify controller is running
kubectl get pods -n kube-system -l app=azure-keyvault-sync-controller
kubectl logs -f deployment/azure-keyvault-sync-controller -n kube-system
```

### Step 2: Create an AzureKeyVaultSync Resource

```bash
# Edit the example with your values
cat > my-vault-sync.yaml <<EOF
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: my-vault-sync
  namespace: default
spec:
  keyvaultName: your-keyvault-name
  tenantId: "your-tenant-id"
  clientID: "your-client-id"
  serviceAccount: workload-identity-sa
  # Optional: Filter by tags
  filters:
    service: "my-app"
    environment: "staging"
EOF

kubectl apply -f my-vault-sync.yaml
```

### Step 3: Verify CRD Mode Works

```bash
# Check the AzureKeyVaultSync status
kubectl get azurekeyvaultsync my-vault-sync -o yaml

# Look for status fields:
# - secretCount: Number of secrets found
# - secretObjectCount: Number with secret-object: "true" tag
# - generatedSPCName: Name of generated SPC
# - lastSyncTime: Last successful sync

# Check that SPC was automatically created
kubectl get secretproviderclass my-vault-sync -o yaml

# Verify secrets are listed in SPC
kubectl get secretproviderclass my-vault-sync -o jsonpath='{.spec.parameters.objects}' | head -50
```

### Step 4: Test Secret Object Generation

```bash
# In Azure, tag a vault secret with secret-object: "true"
az keyvault secret set-attribute \
  --vault-name your-vault \
  --name db-password \
  --tags "secret-object=true" "service=my-app" "environment=staging"

# Wait 30 seconds for next sync cycle

# Check that secretObjects field was added to SPC
kubectl get secretproviderclass my-vault-sync -o jsonpath='{.spec.secretObjects}' | jq

# Create a pod that uses the SPC
cat > test-pod.yaml <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
  namespace: default
spec:
  serviceAccountName: workload-identity-sa
  containers:
  - name: busybox
    image: busybox:latest
    command: ["sleep", "3600"]
    volumeMounts:
    - name: secrets-store
      mountPath: "/mnt/secrets"
      readOnly: true
  volumes:
  - name: secrets-store
    csi:
      driver: secrets-store.csi.k8s.io
      readOnly: true
      volumeAttributes:
        secretProviderClass: my-vault-sync
EOF

kubectl apply -f test-pod.yaml

# Verify Secret was created
kubectl get secret db-password
```

### Step 5: Test Tag Filtering

```bash
# Create AzureKeyVaultSync with filters
cat > filtered-sync.yaml <<EOF
apiVersion: azure-keyvault-sync.io/v1alpha1
kind: AzureKeyVaultSync
metadata:
  name: prod-app-sync
  namespace: default
spec:
  keyvaultName: your-vault
  tenantId: "your-tenant-id"
  clientID: "your-client-id"
  serviceAccount: workload-identity-sa
  filters:
    service: "my-app"
    environment: "production"
EOF

kubectl apply -f filtered-sync.yaml

# Only secrets with BOTH tags will be included
# Check status to see filtered count
kubectl get akvs prod-app-sync -o jsonpath='{.status.secretCount}'
```

## Testing Secret Annotation Sync (Phase 6)

> **Note:** This requires deploying from the `feature/phase-6-secret-annotation-sync` branch

### Step 1: Tag Azure Secrets with Annotations

```bash
# Add kubernetes-reflector annotations
az keyvault secret set-attribute \
  --vault-name your-vault \
  --name shared-secret \
  --tags \
    "secret-object=true" \
    "k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed=true" \
    "k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-auto-enabled=true" \
    "k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-auto-namespaces=app-*"

# Add custom metadata
az keyvault secret set-attribute \
  --vault-name your-vault \
  --name api-key \
  --tags \
    "secret-object=true" \
    "k8s-annotation.owner=platform-team" \
    "k8s-annotation.environment=production" \
    "k8s-annotation.cost-center=engineering"
```

### Step 2: Verify Annotations Flow to SPC

```bash
# Wait for next reconciliation (up to 30 seconds)

# Check SPC annotations
kubectl get secretproviderclass my-vault-sync -o yaml | grep "secret-metadata.azure-keyvault-sync.io"

# Expected format:
# secret-metadata.azure-keyvault-sync.io/shared-secret.reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
# secret-metadata.azure-keyvault-sync.io/api-key.owner: "platform-team"
```

### Step 3: Verify Annotations Applied to Secrets

```bash
# Create pod to trigger Secret creation (if not already exists)
kubectl apply -f test-pod.yaml

# Check Secret annotations (Secret watcher runs every 30s)
kubectl get secret shared-secret -o yaml | grep -A 5 "annotations:"

# Expected annotations (extracted from SPC):
# reflector.v1.k8s.emberstack.com/reflection-allowed: "true"
# reflector.v1.k8s.emberstack.com/reflection-auto-enabled: "true"
# reflector.v1.k8s.emberstack.com/reflection-auto-namespaces: "app-*"

kubectl get secret api-key -o jsonpath='{.metadata.annotations}' | jq
# Expected:
# {
#   "owner": "platform-team",
#   "environment": "production",
#   "cost-center": "engineering"
# }
```

### Step 4: Test with Kubernetes Reflector

If you have [kubernetes-reflector](https://github.com/emberstack/kubernetes-reflector) installed:

```bash
# The annotated Secret should automatically be replicated
kubectl get secrets -n app-prod | grep shared-secret
kubectl get secrets -n app-staging | grep shared-secret
kubectl get secrets -n app-dev | grep shared-secret
```

## Monitoring and Debugging

### Check Controller Logs

```bash
# General logs
kubectl logs -f deployment/azure-keyvault-sync-controller -n kube-system

# Filter for CRD mode
kubectl logs -f deployment/azure-keyvault-sync-controller -n kube-system | grep AzureKeyVaultSync

# Filter for annotation sync
kubectl logs -f deployment/azure-keyvault-sync-controller -n kube-system | grep -i annotation

# Filter for specific resource
kubectl logs -f deployment/azure-keyvault-sync-controller -n kube-system | grep my-vault-sync
```

### Check Metrics

```bash
# Port-forward metrics endpoint
kubectl port-forward deployment/azure-keyvault-sync-controller 9090:9090 -n kube-system

# View metrics in browser
open http://localhost:9090/metrics

# Or with curl
curl http://localhost:9090/metrics | grep controller
```

### Check Health

```bash
# Liveness probe
kubectl port-forward deployment/azure-keyvault-sync-controller 8080:8080 -n kube-system
curl http://localhost:8080/healthz

# Readiness probe
curl http://localhost:8080/readyz
```

## Common Issues

### Issue: CRD Not Found
```bash
# Error: no matches for kind "AzureKeyVaultSync"
# Solution: Apply CRD first
kubectl apply -f deploy/crd/azure-keyvault-sync.io_azurekeyvaultsyncs.yaml
```

### Issue: Controller Can't List Secrets
```bash
# Error: failed to list vault secrets
# Check:
1. ServiceAccount has correct Azure workload identity annotations
2. Azure identity has Key Vault Secrets User role
3. Token exchange is working (check controller logs)
```

### Issue: Annotations Not Appearing on Secrets
```bash
# Check:
1. Secret has label: secrets-store.csi.k8s.io/managed: "true"
2. Secret watcher is running (check logs for "Starting Secret watcher")
3. SPC has annotations with secret-metadata prefix
4. Wait up to 30 seconds for reconciliation cycle
```

### Issue: Tag Filtering Not Working
```bash
# Check:
1. All filter tags must match (AND logic, not OR)
2. Tag keys are exact match (case-sensitive)
3. Check controller logs for "rejected by tag filter"
```

## Cleanup

```bash
# Delete test resources
kubectl delete azurekeyvaultsync my-vault-sync
kubectl delete pod test-pod

# If deletePolicy is Cascade (default), SPC is automatically deleted
# If deletePolicy is Orphan, manually delete SPC:
kubectl delete secretproviderclass my-vault-sync

# Uninstall controller
kubectl delete -f deploy/deployment.yaml
kubectl delete -f deploy/rbac.yaml
kubectl delete -f deploy/crd/azure-keyvault-sync.io_azurekeyvaultsyncs.yaml
```

## Next Steps

1. **Integration Testing**: Test with your actual vault and workload identity
2. **Reflector Integration**: Install kubernetes-reflector and test automatic replication
3. **Production Deployment**: Deploy to production with appropriate RBAC
4. **Monitoring Setup**: Configure Prometheus to scrape controller metrics
