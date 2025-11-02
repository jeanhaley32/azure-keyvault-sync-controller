# Migration Guide: Opinionated Tag Philosophy

**Breaking Change:** As of version 2.0.0, the controller implements an opinionated approach where **Azure Key Vault tags are the single source of truth** for all synchronization behavior.

## What Changed

### 1. Sync Opt-In is Now Required

**Before (v1.x):**
- All secrets in the vault were synced by default
- Optional `respect-tags` annotation to enable filtering

**After (v2.0+):**
- Secrets/certificates MUST have `sync: "true"` tag to be synced
- The `respect-tags` annotation has been removed
- Tag-based behavior is always enforced

### 2. Tag Hierarchy

The controller now uses a two-level tag hierarchy:

```
sync: "true"              → Opt-in to sync (required for ALL secrets)
  ├─ secret-object: "true" → Also create K8s Secret (implies sync)
  └─ cert-object: "true"   → Also create K8s TLS Secret (implies sync)

service: "app-name"       → Multi-tenant filtering (optional)
environment: "prod"       → Multi-tenant filtering (optional)
```

**Key Rules:**
1. `sync: "true"` is required for a secret to be synced
2. `secret-object: "true"` automatically implies `sync: "true"`
3. `cert-object: "true"` automatically implies `sync: "true"`
4. Service/environment tags are only checked if the SPC has service/environment labels

## Migration Steps

### Step 1: Identify Your Secrets

List all secrets currently being synced:

```bash
# Get the current objects array from your SPC
kubectl get secretproviderclass <name> -o jsonpath='{.spec.parameters.objects}' | grep objectName
```

### Step 2: Add Sync Tags to Azure Key Vault

For each secret you want to keep syncing, add the `sync: "true"` tag:

```bash
# Single secret
az keyvault secret set-attributes \
  --vault-name <vault-name> \
  --name <secret-name> \
  --tags sync=true

# Secret that should also create K8s Secret
az keyvault secret set-attributes \
  --vault-name <vault-name> \
  --name <secret-name> \
  --tags sync=true secret-object=true

# Certificate that should create K8s TLS Secret
az keyvault certificate set-attributes \
  --vault-name <vault-name> \
  --name <cert-name> \
  --tags sync=true cert-object=true
```

**Batch Script Example:**

```bash
#!/bin/bash
VAULT_NAME="your-vault-name"

# List of secrets to sync
SECRETS=(
  "database-password"
  "api-key"
  "jwt-secret"
)

# Tag all secrets for sync
for secret in "${SECRETS[@]}"; do
  echo "Tagging $secret..."
  az keyvault secret set-attributes \
    --vault-name "$VAULT_NAME" \
    --name "$secret" \
    --tags sync=true
done
```

### Step 3: Remove Deprecated Annotations

If you were using `respect-tags` annotation, remove it:

```bash
# Before
kubectl annotate secretproviderclass <name> azure-keyvault-sync/respect-tags-

# The annotation is now ignored, but removing it keeps config clean
```

### Step 4: Verify After Upgrade

After upgrading the controller and adding tags:

```bash
# Check controller logs
kubectl logs -n kube-system -l app=azure-keyvault-sync-controller

# Look for log messages indicating sync behavior:
# - "secretsSynced=X" - Number of secrets with sync tag
# - "secretsNoSyncTag=Y" - Secrets without sync tag (filtered out)

# Verify your SPC was updated
kubectl get secretproviderclass <name> -o yaml
```

## Single-Tenant vs Multi-Tenant Mode

### Single-Tenant Mode (Simple Vaults)

If your vault is used by a single application/service, you don't need service/environment tags:

```bash
# Just tag for sync opt-in
az keyvault secret set-attributes \
  --vault-name single-app-vault \
  --name database-password \
  --tags sync=true
```

**SPC Configuration:**
```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: my-app
  namespace: default
  # No service/environment labels needed
  annotations:
    azure-keyvault-sync/service-account: "my-app"
```

### Multi-Tenant Mode (Shared Vaults)

If your vault is shared by multiple services, add service/environment tags AND labels:

```bash
# Tag with service and environment
az keyvault secret set-attributes \
  --vault-name shared-vault \
  --name web-api-db-password \
  --tags sync=true service=web-api environment=production
```

**SPC Configuration:**
```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: web-api-prod
  namespace: production
  labels:
    service: web-api
    environment: production
  annotations:
    azure-keyvault-sync/service-account: "web-api"
```

## Common Migration Scenarios

### Scenario 1: Simple Vault (No Filtering)

**Before:**
```yaml
# All secrets synced automatically
metadata:
  annotations:
    azure-keyvault-sync/service-account: "my-app"
```

**After:**
```bash
# Add sync tag to all secrets you want
az keyvault secret set-attributes --vault-name my-vault --name secret1 --tags sync=true
az keyvault secret set-attributes --vault-name my-vault --name secret2 --tags sync=true
```

### Scenario 2: Using respect-tags

**Before:**
```yaml
metadata:
  labels:
    service: web-api
  annotations:
    azure-keyvault-sync/service-account: "web-api"
    azure-keyvault-sync/respect-tags: "true"  # This annotation is removed
```

**After:**
```bash
# Add sync + service tags
az keyvault secret set-attributes \
  --vault-name shared-vault \
  --name api-secret \
  --tags sync=true service=web-api
```

```yaml
metadata:
  labels:
    service: web-api
  annotations:
    azure-keyvault-sync/service-account: "web-api"
    # respect-tags annotation removed - behavior is now mandatory
```

### Scenario 3: Using secret-object Tags

**Before:**
```bash
# secret-object alone was enough
az keyvault secret set-attributes --vault-name vault --name secret --tags secret-object=true
```

**After:**
```bash
# Still works! secret-object implies sync
az keyvault secret set-attributes --vault-name vault --name secret --tags secret-object=true

# Or be explicit
az keyvault secret set-attributes --vault-name vault --name secret --tags sync=true secret-object=true
```

## Rollback Plan

If you need to rollback to v1.x:

1. **Keep the tags** - They're ignored by v1.x, so they don't break anything
2. **Re-add respect-tags annotation** if you were using filtering
3. **Downgrade the controller** to previous version

The tags you added won't affect v1.x behavior, so migration is safe and reversible.

## Validation Checklist

- [ ] All required secrets have `sync: "true"` tag in Azure Key Vault
- [ ] Secrets that need K8s Secrets have `secret-object: "true"` or `cert-object: "true"` tags
- [ ] Multi-tenant vaults have service/environment tags on secrets
- [ ] Multi-tenant SPCs have service/environment labels
- [ ] Removed `respect-tags` annotations from SPCs (optional, but recommended for cleanliness)
- [ ] Tested that the correct secrets are synced after upgrade
- [ ] Verified K8s Secrets are created for tagged vault secrets

## Troubleshooting

### No Secrets Syncing After Upgrade

**Problem:** Controller logs show `secretsSynced=0`

**Solution:** Add `sync: "true"` tag to your vault secrets:

```bash
az keyvault secret set-attributes --vault-name <vault> --name <secret> --tags sync=true
```

### Service/Environment Filtering Not Working

**Problem:** All secrets syncing even though SPC has service label

**Solution:** Check that your SPC has BOTH the service label AND the vault secrets have service tags:

```bash
# SPC must have label
kubectl get secretproviderclass <name> -o jsonpath='{.metadata.labels.service}'

# Vault secrets must have matching tag
az keyvault secret show --vault-name <vault> --name <secret> --query tags.service
```

### Secrets Rejected by Filter

**Problem:** Controller logs show `secretsServiceEnvRejected=X`

**Solution:** This is expected in multi-tenant mode. Ensure:
- SPC service label matches vault secret service tag (case-insensitive)
- If vault secret has environment tag, SPC must have matching environment label

## Support

For questions or issues during migration:
- Check controller logs: `kubectl logs -n kube-system -l app=azure-keyvault-sync-controller`
- Review the [Tag Filtering Decision Tree](docs/design/tag-filtering-decision-tree.md)
- Open an issue: https://github.com/jeanhaley32/azure-keyvault-sync-controller/issues
