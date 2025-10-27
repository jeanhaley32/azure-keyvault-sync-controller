# SecretProviderClass Examples

This directory contains example SecretProviderClass manifests demonstrating various configurations for the Azure Key Vault Sync Controller.

## Examples

### basic-sync.yaml
Minimal configuration that automatically populates the `objects` array from Azure Key Vault.

**Use this when:**
- You only need the CSI driver to mount secrets into pods
- You don't need automatic Kubernetes Secret creation

**What happens:**
- Controller discovers all enabled secrets and certificates in the vault
- `objects` array is automatically populated
- CSI driver can mount these as files in pod volumes

### with-secrets.yaml
Configuration that also generates Kubernetes Secrets automatically.

**Use this when:**
- You want automatic Kubernetes Secret creation
- You need to inject secrets as environment variables
- You want secrets available cluster-wide (not just in mounted volumes)

**What happens:**
- Controller populates both `objects` and `secretObjects` arrays
- CSI driver creates Kubernetes Secrets from vault contents
- Secrets available via `secretKeyRef` in pod specs

### full-example.yaml
Complete example with all features, annotations, and a sample pod configuration.

**Includes:**
- All available annotations documented
- Example ServiceAccount with Azure Workload Identity
- Example Pod using the SecretProviderClass
- Comments explaining each field

## Prerequisites

Before using these examples, ensure you have:

1. **Azure Infrastructure:**
   - Azure Key Vault with secrets/certificates
   - User-Assigned Managed Identity
   - Federated Identity Credential linking to Kubernetes ServiceAccount
   - RBAC roles: Key Vault Secrets User + Certificate User

2. **Kubernetes Infrastructure:**
   - Secrets Store CSI Driver installed
   - Azure Key Vault Provider for CSI Driver installed
   - Azure Workload Identity installed
   - Controller deployed (see main README)

3. **ServiceAccount Configuration:**
   - ServiceAccount with `azure.workload.identity/client-id` annotation
   - Federated identity configured in Azure

## Usage

1. Copy one of the examples
2. Update the following values:
   - `keyvaultName`: Your Azure Key Vault name
   - `clientID`: Your Azure Managed Identity client ID
   - `tenantId`: Your Azure AD tenant ID
   - `azure-keyvault-sync/service-account`: Your ServiceAccount name

3. Apply the manifest:
   ```bash
   kubectl apply -f basic-sync.yaml
   ```

4. Verify the controller synced the vault contents:
   ```bash
   kubectl get secretproviderclass example-basic-sync -o yaml
   ```

5. Check the last-sync annotation:
   ```bash
   kubectl get secretproviderclass example-basic-sync -o jsonpath='{.metadata.annotations.azure-keyvault-sync/last-sync}'
   ```

## Troubleshooting

### Objects array is empty
- Check controller logs: `kubectl logs -n kube-system -l app=azure-keyvault-sync-controller`
- Verify annotations are correct (especially `enabled: "true"`)
- Ensure ServiceAccount exists and has correct Azure annotation
- Check Azure RBAC permissions on the vault

### Permission errors (403 Forbidden)
- Verify Managed Identity has correct RBAC roles on vault
- Check Federated Identity Credential is configured correctly
- Ensure ServiceAccount annotation matches the clientID

### Secrets not created (with-secrets.yaml)
- Verify CSI driver is running: `kubectl get pods -n kube-system -l app=secrets-store-csi-driver`
- Check that pod is using the SecretProviderClass
- Review CSI driver logs for errors

## More Information

See the main [README.md](../README.md) for:
- Complete controller documentation
- Architecture overview
- Deployment instructions
- Additional troubleshooting
