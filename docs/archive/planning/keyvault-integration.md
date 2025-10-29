# Phase 3: Azure Key Vault Integration

## Overview

Phase 3 implements the integration with Azure Key Vault to list secrets and certificates using the Azure AD tokens acquired in Phase 2.2. This phase completes the read-only data acquisition pipeline before Phase 4 implements SecretProviderClass updates.

## Research Findings

### Azure Key Vault SDK for Go

**Packages:**
- `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets` - Secrets operations
- `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates` - Certificate operations

**Client Creation:**
```go
import (
    "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
    "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// Vault URL format
vaultURL := fmt.Sprintf("https://%s.vault.azure.net", keyvaultName)

// Create clients with credential
secretsClient, err := azsecrets.NewClient(vaultURL, credential, nil)
certsClient, err := azcertificates.NewClient(vaultURL, credential, nil)
```

**Credential Interface:**
Both clients accept `azcore.TokenCredential` interface:
```go
type TokenCredential interface {
    GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error)
}
```

### Listing Secrets

**Method:** `NewListSecretPropertiesPager(options *ListSecretPropertiesOptions) *runtime.Pager[ListSecretPropertiesResponse]`

**Pattern:**
```go
pager := secretsClient.NewListSecretPropertiesPager(nil)
for pager.More() {
    page, err := pager.NextPage(ctx)
    if err != nil {
        // Handle error
    }
    for _, secret := range page.Value {
        // secret.ID contains the full identifier
        // secret.ID.Name() gives the secret name
        // secret.Attributes.Enabled indicates if secret is active
    }
}
```

**Important Notes:**
- List operations return only metadata (names, IDs, attributes)
- Secret values are NOT included in list responses
- We only need names for SecretProviderClass objects array
- Individual secret retrieval would require `GetSecret()` but not needed for our use case

### Listing Certificates

**Method:** `NewListCertificatesPager(options *ListCertificatesOptions) *runtime.Pager[ListCertificatesResponse]`

**Pattern:**
```go
pager := certsClient.NewListCertificatesPager(nil)
for pager.More() {
    page, err := pager.NextPage(ctx)
    if err != nil {
        // Handle error
    }
    for _, cert := range page.Value {
        // cert.ID contains the full identifier
        // cert.ID.Name() gives the certificate name
        // cert.Attributes.Enabled indicates if certificate is active
    }
}
```

**Important Notes:**
- Similar to secrets, only metadata is returned
- Certificate values/content not needed for our use case
- We only need names for SecretProviderClass objects array

## Architecture Decisions

### Custom Token Credential Wrapper

**Challenge:** Azure SDK clients expect `azcore.TokenCredential` interface, but we already have Azure AD tokens from Phase 2.2.

**Solution:** Create a custom credential wrapper that implements the interface:

```go
type CachedTokenCredential struct {
    token      string
    expiration time.Time
}

func (c *CachedTokenCredential) GetToken(
    ctx context.Context,
    opts policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
    // Return our existing token
    return azcore.AccessToken{
        Token:     c.token,
        ExpiresOn: c.expiration,
    }, nil
}
```

**Benefits:**
- Reuses existing token acquisition logic from Phase 2.2
- No redundant token requests
- Simple wrapper with minimal overhead
- Integrates cleanly with Azure SDK

### Vault Name Extraction

**Source:** `spec.parameters.keyvaultName` in SecretProviderClass

**Example from real SecretProviderClass:**
```yaml
spec:
  parameters:
    keyvaultName: "staging-myservice-vault"
    clientID: "aac3d546-358f-4e74-94e5-bb4c472d7cc0"
    tenantId: "12345678-1234-1234-1234-123456789012"
```

**Implementation:**
```go
func ExtractKeyvaultName(obj *unstructured.Unstructured) (string, error) {
    keyvaultName, found, err := unstructured.NestedString(obj.Object, "spec", "parameters", "keyvaultName")
    if err != nil {
        return "", fmt.Errorf("error accessing spec.parameters.keyvaultName: %w", err)
    }
    if !found || keyvaultName == "" {
        return "", fmt.Errorf("keyvaultName not found in spec.parameters")
    }
    return keyvaultName, nil
}
```

### Error Handling Strategy

**RBAC Permission Errors (403 Forbidden):**
- Expected scenario: Service may not have permissions to vault
- Log warning but don't fail entire sync
- Continue processing other vaults
- Allow operator to fix RBAC separately

**Network Errors:**
- Vault unreachable or DNS failure
- Retry with exponential backoff (consider for future enhancement)
- Log error and continue with other vaults

**Pagination Errors:**
- Handle gracefully mid-pagination
- Log partial results if any were obtained
- Don't block entire sync process

**Philosophy:** Best-effort approach - one vault failure shouldn't break entire controller sync.

### Result Storage

**For Phase 3:** Log discovered secrets and certificates for verification.

**For Phase 4:** Store results for SecretProviderClass updates:
```go
type VaultContents struct {
    Secrets      []string
    Certificates []string
    VaultName    string
    Namespace    string
    LastSynced   time.Time
}
```

## Implementation Plan

### Step 1: Create vault.go

**New file:** `vault.go`

**Components:**
1. `CachedTokenCredential` struct and implementation
2. `VaultClient` wrapper struct (optional, for organization)
3. `ListSecrets(ctx, vaultName, token, expiration)` function
4. `ListCertificates(ctx, vaultName, token, expiration)` function
5. `ExtractKeyvaultName(obj)` helper function

**Example Structure:**
```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
    "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
    "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type CachedTokenCredential struct {
    token      string
    expiration time.Time
}

func (c *CachedTokenCredential) GetToken(
    ctx context.Context,
    opts policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
    return azcore.AccessToken{
        Token:     c.token,
        ExpiresOn: c.expiration,
    }, nil
}

func ListSecrets(
    ctx context.Context,
    vaultName string,
    token string,
    expiration time.Time,
) ([]string, error) {
    // Implementation
}

func ListCertificates(
    ctx context.Context,
    vaultName string,
    token string,
    expiration time.Time,
) ([]string, error) {
    // Implementation
}

func ExtractKeyvaultName(obj *unstructured.Unstructured) (string, error) {
    // Implementation
}
```

### Step 2: Integrate into controller.go

**Location:** In `syncCache()` function, after Azure AD token acquisition.

**Flow:**
```go
// ... existing code to get Azure AD token ...

// Extract vault name
keyvaultName, err := ExtractKeyvaultName(&item)
if err != nil {
    log.Printf("Warning: %s/%s missing keyvaultName: %v", item.GetNamespace(), item.GetName(), err)
    continue
}

// List secrets from vault
secrets, err := ListSecrets(ctrl.ctx, keyvaultName, azureToken, azureTokenExpiration)
if err != nil {
    log.Printf("Error listing secrets from vault %s for %s/%s: %v",
        keyvaultName, item.GetNamespace(), item.GetName(), err)
    // Continue processing - don't fail entire sync
} else {
    log.Printf("Found %d secrets in vault %s for %s/%s",
        len(secrets), keyvaultName, item.GetNamespace(), item.GetName())
    for _, secret := range secrets {
        log.Printf("  - Secret: %s", secret)
    }
}

// List certificates from vault
certificates, err := ListCertificates(ctrl.ctx, keyvaultName, azureToken, azureTokenExpiration)
if err != nil {
    log.Printf("Error listing certificates from vault %s for %s/%s: %v",
        keyvaultName, item.GetNamespace(), item.GetName(), err)
    // Continue processing - don't fail entire sync
} else {
    log.Printf("Found %d certificates in vault %s for %s/%s",
        len(certificates), keyvaultName, item.GetNamespace(), item.GetName())
    for _, cert := range certificates {
        log.Printf("  - Certificate: %s", cert)
    }
}
```

### Step 3: Add Dependencies

**Method:** Import packages in `vault.go`, then run `go mod tidy`.

**Packages to add:**
- `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets`
- `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates`

**Command:**
```bash
# Add imports to vault.go first
go mod tidy
```

### Step 4: Test Against Real Vault

**Prerequisites:**
- Service account with Azure Workload Identity configured
- Managed identity with RBAC permissions:
  - Key Vault Secrets User
  - Key Vault Certificates User
- Azure Key Vault with test secrets and certificates

**Test Scenarios:**
1. **Happy Path:** Vault with secrets and certificates
   - Verify all secrets listed
   - Verify all certificates listed
   - Check logs for correct counts

2. **Empty Vault:** Vault with no contents
   - Should succeed with 0 secrets/certificates
   - No errors logged

3. **Insufficient Permissions:** Vault without RBAC role assignment
   - Should log 403 error
   - Should continue processing other vaults
   - Controller should not crash

4. **Invalid Vault Name:** Non-existent vault
   - Should log DNS/network error
   - Should continue processing
   - Controller should not crash

## Testing Strategy

### Unit Tests (Future Enhancement)

**Mock Token Credential:**
```go
type MockTokenCredential struct {
    token string
}

func (m *MockTokenCredential) GetToken(...) (azcore.AccessToken, error) {
    return azcore.AccessToken{Token: m.token, ExpiresOn: time.Now().Add(1 * time.Hour)}, nil
}
```

**Test Cases:**
- Token credential wrapper returns correct token
- Vault name extraction from SecretProviderClass
- Error handling for missing keyvaultName
- Pagination through multiple pages of secrets/certificates

### Integration Tests

**Real AKS Cluster Tests:**
1. Deploy controller to staging cluster
2. Create test SecretProviderClass with annotations
3. Verify controller logs show discovered secrets/certificates
4. Verify no crashes or errors in controller pod

**Test Vault Setup:**
```bash
# Create test secrets
az keyvault secret set --vault-name staging-test-vault --name "test-secret-1" --value "value1"
az keyvault secret set --vault-name staging-test-vault --name "test-secret-2" --value "value2"

# Create test certificate
az keyvault certificate create --vault-name staging-test-vault --name "test-cert-1" --policy @policy.json
```

## Security Considerations

### Token Reuse from Phase 2.2

**Benefit:** No additional token requests or credential storage.

**Security:** Tokens are already cached with proper lifecycle management from Phase 2.2.

### RBAC Principle of Least Privilege

**Required Roles:**
- Key Vault Secrets User (read-only, no write/delete)
- Key Vault Certificates User (read-only, no write/delete)

**NOT Required:**
- Key Vault Administrator (too broad)
- Key Vault Secrets Officer (has write permissions)

### Audit Trail

**Vault Logs:** All list operations will appear in Azure Monitor with correct service identity attribution (no shared credential).

### Error Message Sanitization

**Avoid:** Leaking vault names or sensitive details in error messages that might end up in cluster logs accessible to developers.

**Approach:** Log vault names only in controller logs, not in user-facing events (Phase 4 consideration).

## Success Criteria

**Phase 3 Complete When:**
1. ✅ Vault name extraction working from SecretProviderClass
2. ✅ Custom token credential wrapper implemented
3. ✅ Secret listing functional with real vault
4. ✅ Certificate listing functional with real vault
5. ✅ Error handling for RBAC failures (403)
6. ✅ Error handling for network failures
7. ✅ Controller continues processing after individual vault failures
8. ✅ Comprehensive logging of discovered contents
9. ✅ No controller crashes during sync
10. ✅ Tested against staging cluster with real vault

## Dependencies on Previous Phases

**Phase 2.1 (Kubernetes Token Acquisition):**
- Used to establish service account identity

**Phase 2.2 (Azure AD Token Exchange):**
- Azure AD token is required input for vault clients
- Token expiration used in custom credential wrapper
- Service account scope ensures correct RBAC evaluation

## Looking Ahead to Phase 4

**What Phase 3 Provides:**
- List of all secrets in vault
- List of all certificates in vault
- Error handling patterns for vault operations

**What Phase 4 Needs:**
- Convert lists to SecretProviderClass objects array format
- Patch SecretProviderClass resources with discovered contents
- Handle merge strategy for existing manually-defined objects
- Add last-sync timestamp annotation

**Example Output Format for Phase 4:**
```yaml
objects:
  - objectName: "test-secret-1"
    objectType: "secret"
  - objectName: "test-secret-2"
    objectType: "secret"
  - objectName: "test-cert-1"
    objectType: "cert"
```

## Risk Analysis

**Risk: RBAC Configuration Errors**
- **Probability:** High (common misconfiguration)
- **Impact:** Medium (vault contents not discoverable)
- **Mitigation:** Clear error logging, documentation of required roles

**Risk: Vault Network Connectivity**
- **Probability:** Low (Azure infrastructure reliable)
- **Impact:** Medium (temporary sync failures)
- **Mitigation:** Retry logic, graceful degradation

**Risk: Large Vault Pagination**
- **Probability:** Low (most vaults have < 100 secrets)
- **Impact:** Low (SDK handles pagination automatically)
- **Mitigation:** Pager pattern handles this transparently

**Risk: Token Expiration During Vault Operation**
- **Probability:** Very Low (28-hour token lifetime)
- **Impact:** Low (next sync will succeed)
- **Mitigation:** Phase 2.2 token renewal ensures fresh tokens

## Implementation Checklist

- [ ] Create `vault.go` with CachedTokenCredential
- [ ] Implement ListSecrets() function
- [ ] Implement ListCertificates() function
- [ ] Implement ExtractKeyvaultName() helper
- [ ] Add vault operations to controller syncCache()
- [ ] Add comprehensive error handling
- [ ] Add logging for discovered contents
- [ ] Run `go mod tidy` to add dependencies
- [ ] Build and test locally
- [ ] Test against staging vault with RBAC permissions
- [ ] Test error handling (403, network errors)
- [ ] Verify pagination works with large vaults
- [ ] Verify controller continues after vault failures
- [ ] Commit implementation
- [ ] Create PR to main
- [ ] Update CHANGELOG.md
- [ ] Update ROADMAP.md
- [ ] Update README.md

## Estimated Complexity

**Development Time:** 2-3 hours
- Custom credential wrapper: 30 minutes
- Vault operations implementation: 1 hour
- Controller integration: 30 minutes
- Testing and debugging: 1 hour

**Testing Time:** 1-2 hours
- Local testing: 30 minutes
- Staging cluster deployment: 30 minutes
- Real vault testing: 30 minutes
- Error scenario testing: 30 minutes

**Total:** 3-5 hours for complete Phase 3 implementation and testing.
