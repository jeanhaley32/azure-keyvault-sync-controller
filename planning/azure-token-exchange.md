# Phase 2.2: Azure AD Token Exchange Planning

**Date:** 2025-10-25
**Branch:** azure-token-exchange
**Status:** Planning Complete, Ready for Implementation

## Overview

Exchange Kubernetes JWT tokens for Azure AD access tokens using Azure Workload Identity federation to enable Key Vault access.

## Research Summary

### Azure Workload Identity Mechanism

Azure AD trusts the AKS cluster as an OIDC issuer through federated identity credentials. The flow works as follows:

1. **Kubernetes as Token Issuer**: The Kubernetes cluster issues JWT tokens to service accounts
2. **OIDC Discovery**: Azure AD uses OpenID Connect to discover the cluster's public signing keys
3. **Token Validation**: Azure AD verifies the Kubernetes token's authenticity
4. **Token Exchange**: Azure AD issues its own access token in exchange for the validated K8s token
5. **No Stored Credentials**: Entire process uses federation - no secrets or keys stored

### Token Scope Architecture

**Key Finding**: Azure AD tokens are scoped to the **Key Vault service**, not individual vaults.

- **Scope**: `https://vault.azure.net/.default`
- **Service-level authentication**: One token can access ANY vault (subject to RBAC)
- **RBAC controls authorization**: Token validity ≠ vault access
- **Efficient caching**: One token per service account, reusable across all vaults

**Architectural Separation**:
- **Phase 2.2** (This Phase): **Authentication** - Get the Azure AD token
- **Phase 3** (Next Phase): **Authorization** - Use token to access specific vaults

### WorkloadIdentityCredential in Go

**Azure SDK Package**: `github.com/Azure/azure-sdk-for-go/sdk/azidentity`

**How It Works**:
```go
// Reads configuration from environment variables automatically
cred, err := azidentity.NewWorkloadIdentityCredential(nil)

// Request token for Key Vault service
token, err := cred.GetToken(
    ctx,
    policy.TokenRequestOptions{
        Scopes: []string{"https://vault.azure.net/.default"},
    },
)
```

**Required Environment Variables**:
- `AZURE_FEDERATED_TOKEN_FILE`: Path to file containing Kubernetes JWT
- `AZURE_CLIENT_ID`: Application/Managed Identity client ID
- `AZURE_TENANT_ID`: Azure tenant ID
- `AZURE_AUTHORITY_HOST`: (Optional) Microsoft Entra authority endpoint

**Token File Format**:
- Contains the raw Kubernetes JWT token as a string
- No special formatting - just the token itself
- Typically mounted at `/var/run/secrets/azure/tokens` in production
- We'll use temporary files for our impersonation approach

### SecretProviderClass Parameters

**All required information is available**:

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  annotations:
    azure-keyvault-sync/enabled: "true"
    azure-keyvault-sync/service-account: "aks-staging-flow"
spec:
  provider: azure
  parameters:
    clientID: "aac3d546-358f-4e74-94e5-bb4c472d7cc0"    # ✅ Already extracting
    tenantId: "your-tenant-id"                          # 📋 Need to extract
    keyvaultName: "staging-flow-vault"                  # 📋 Phase 3 only
```

**What we need for Phase 2.2**:
- ✅ `clientID` - Already extracted in Phase 2.1
- ✅ Kubernetes JWT - Already acquired in Phase 2.1
- 📋 `tenantId` - Need to add extraction (same pattern as clientID)

**What we need for Phase 3**:
- 📋 `keyvaultName` - Used to construct vault URL

## Technical Implementation Design

### Architecture

```
Controller
  ├── TokenCache (K8s JWT tokens)           [Phase 2.1 - COMPLETE]
  ├── AzureTokenCache (Azure AD tokens)     [Phase 2.2 - THIS PHASE]
  └── KeyVaultClient(s)                     [Phase 3 - FUTURE]
```

**Token Acquisition Flow**:
```
syncCache() iteration per SecretProviderClass:
  1. Get K8s JWT from TokenCache (cached, namespace/serviceAccount)
  2. Extract clientID from spec.parameters.clientID
  3. Extract tenantId from spec.parameters.tenantId
  4. Check AzureTokenCache for valid token (namespace/serviceAccount)
  5. If needed, exchange K8s JWT for Azure AD token:
     a. Write K8s JWT to temporary file
     b. Set environment variables (AZURE_*)
     c. Create WorkloadIdentityCredential
     d. Call GetToken with vault scope
     e. Cache Azure AD token
     f. Clean up temp file
  6. Have valid Azure AD token ready for Phase 3
```

### File Structure

**New File: `azure.go`**

```go
package main

import (
    "context"
    "fmt"
    "os"
    "sync"
    "time"

    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
    "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
    azureTokenRenewalThreshold = 0.8  // Renew at 80% of lifetime
    keyVaultScope             = "https://vault.azure.net/.default"
)

type AzureTokenCache struct {
    mu     sync.RWMutex
    tokens map[string]*CachedAzureToken
}

type CachedAzureToken struct {
    Token          string
    ExpirationTime time.Time
    Namespace      string
    ServiceAccount string
    ClientID       string
    TenantID       string
}

func NewAzureTokenCache() *AzureTokenCache {
    return &AzureTokenCache{
        tokens: make(map[string]*CachedAzureToken),
    }
}

// GetToken returns cached Azure AD token or acquires new one
func (ac *AzureTokenCache) GetToken(
    ctx context.Context,
    namespace string,
    serviceAccount string,
    k8sToken string,
    clientID string,
    tenantID string,
) (string, error) {
    // Check cache first
    // If valid, return cached token
    // Otherwise, exchange K8s token for Azure AD token
}

// exchangeToken performs the actual token exchange
func (ac *AzureTokenCache) exchangeToken(
    ctx context.Context,
    k8sToken string,
    clientID string,
    tenantID string,
) (string, time.Time, error) {
    // Write K8s token to temporary file
    // Set environment variables
    // Create WorkloadIdentityCredential
    // Call GetToken
    // Return token and expiration
    // Clean up temp file
}

// ExtractTenantID extracts tenantId from SecretProviderClass
func ExtractTenantID(obj *unstructured.Unstructured) (string, error) {
    // Similar to ExtractClientID
}
```

### Caching Strategy

**Cache Key**: `namespace/serviceAccount`

**Rationale**:
- One Azure AD token per service account identity
- Same token can access multiple vaults (if RBAC permits)
- More efficient than vault-specific tokens
- Aligns with service account impersonation model

**Cache Entry**:
```go
type CachedAzureToken struct {
    Token          string        // Azure AD JWT
    ExpirationTime time.Time     // When token expires
    Namespace      string        // For logging/debugging
    ServiceAccount string        // For logging/debugging
    ClientID       string        // For logging/debugging
    TenantID       string        // For logging/debugging
}
```

**Renewal Logic**:
- Check if `time.Now() >= (ExpirationTime - 20% of lifetime)`
- Azure AD tokens typically 1 hour lifetime
- Renew at 48 minutes (80% of 60 minutes)
- Consistent with K8s token renewal pattern

### Environment Variable Management

**Challenge**: WorkloadIdentityCredential reads from **process-level** environment variables

**Our Approach**:
1. Set env vars before creating credential
2. Create credential (reads env vars)
3. Get token
4. Unset env vars (optional cleanup)

**Alternative Considered**: Custom credential implementation
- **Rejected**: Too complex, reinventing the wheel
- **Decision**: Accept process-level env vars, document the limitation

**Code Pattern**:
```go
// Set environment variables
os.Setenv("AZURE_FEDERATED_TOKEN_FILE", tokenFilePath)
os.Setenv("AZURE_CLIENT_ID", clientID)
os.Setenv("AZURE_TENANT_ID", tenantID)

// Create credential (reads env vars)
cred, err := azidentity.NewWorkloadIdentityCredential(nil)

// Get token
token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
    Scopes: []string{keyVaultScope},
})

// Optional: Clean up env vars
os.Unsetenv("AZURE_FEDERATED_TOKEN_FILE")
os.Unsetenv("AZURE_CLIENT_ID")
os.Unsetenv("AZURE_TENANT_ID")
```

### Temporary File Handling

**File Creation**:
```go
tmpFile, err := os.CreateTemp("", "k8s-token-*.jwt")
tmpFile.Chmod(0600)  // Restrict to owner only
tmpFile.WriteString(k8sToken)
tmpFile.Close()
```

**File Cleanup**:
```go
defer os.Remove(tmpFile.Name())
```

**Security Considerations**:
- Use `os.CreateTemp()` for secure file creation
- Set permissions to 0600 (owner read/write only)
- Delete immediately after use
- No sensitive data persists on disk

## Error Handling

### Expected Errors

1. **Missing Tenant ID**
   - Error: tenantId not found in spec.parameters
   - Handling: Log warning, skip this SecretProviderClass
   - Recovery: User must add tenantId to spec

2. **Federated Identity Not Configured**
   - Error: Azure AD returns "AADSTS700016: Application not found"
   - Handling: Log detailed error with clientID/tenantID
   - Recovery: User must configure federated identity credential in Azure

3. **Invalid Token File**
   - Error: Unable to create or write temporary file
   - Handling: Log error, retry on next sync
   - Recovery: Check filesystem permissions

4. **Token Exchange Failure**
   - Error: Network error, Azure AD unavailable
   - Handling: Log error, keep using cached token if available
   - Recovery: Automatic retry on next sync (5 minutes)

5. **Expired Kubernetes Token**
   - Error: Azure AD rejects expired K8s token
   - Handling: Force K8s token renewal, retry exchange
   - Recovery: Automatic via token renewal logic

### Error Messages

**Format**: Clear, actionable, includes all relevant identifiers

```go
log.Printf("Error exchanging token for %s/%s (clientID: %s, tenantID: %s): %v",
    namespace, serviceAccount, clientID, tenantID, err)
```

## Testing Strategy

### Phase 1: Stubbed Implementation

**Purpose**: Test structure and integration without Azure infrastructure

**Stub Implementation**:
```go
func (ac *AzureTokenCache) exchangeToken(
    ctx context.Context,
    k8sToken string,
    clientID string,
    tenantID string,
) (string, time.Time, error) {
    log.Printf("STUB: Would exchange K8s token for Azure AD token")
    log.Printf("STUB:   clientID: %s", clientID)
    log.Printf("STUB:   tenantID: %s", tenantID)
    log.Printf("STUB:   k8sToken: %s...%s", k8sToken[:5], k8sToken[len(k8sToken)-5:])

    // Return fake Azure AD token
    fakeToken := fmt.Sprintf("azure-ad-token-%d", time.Now().Unix())
    expiration := time.Now().Add(1 * time.Hour)

    log.Printf("STUB: Would receive Azure AD token: %s...%s",
        fakeToken[:5], fakeToken[len(fakeToken)-5:])
    log.Printf("STUB: Token expires at: %s", expiration.Format(time.RFC3339))

    return fakeToken, expiration, nil
}
```

**Test Cases**:
- ✅ tenantID extraction from SecretProviderClass
- ✅ Cache key generation (namespace/serviceAccount)
- ✅ Cache hit returns existing token
- ✅ Cache miss triggers exchange
- ✅ Renewal logic triggers at 80% lifetime
- ✅ Logging shows all parameters

### Phase 2: Real Implementation

**Purpose**: Verify actual token exchange with Azure AD

**Test Environment**:
- Staging AKS cluster with Workload Identity enabled
- Federated identity credential configured
- Real service account with Azure identity mapping

**Verification Steps**:
1. Run controller locally (uses ~/.kube/config)
2. Observe K8s token acquisition (Phase 2.1)
3. Observe tenantID extraction
4. Observe Azure AD token exchange
5. Decode Azure AD token to verify claims
6. Verify token expiration time
7. Wait for renewal trigger (or adjust time threshold)
8. Verify token renewal works

**Expected Azure AD Token Format**:
```json
{
  "aud": "https://vault.azure.net",
  "iss": "https://login.microsoftonline.com/{tenant}/v2.0",
  "iat": 1729900000,
  "exp": 1729903600,
  "sub": "{client-id}",
  "appid": "{client-id}",
  "tid": "{tenant-id}"
}
```

**Success Criteria**:
- Real Azure AD token obtained
- Token has correct audience (https://vault.azure.net)
- Token expiration is ~1 hour from issuance
- Token can be decoded as valid JWT
- Renewal logic triggers before expiration

## Security Considerations

### Token Security

1. **Kubernetes JWT Tokens** (Phase 2.1)
   - Logged as snippets only: `eyJhb...IzGr0`
   - Cached in memory only (not persisted)
   - Renewed proactively before expiration

2. **Azure AD Tokens** (Phase 2.2)
   - Logged as snippets only: `eyJ0e...Kd82z`
   - Cached in memory only (not persisted)
   - Temporary files deleted immediately after use
   - File permissions restricted to owner (0600)

3. **Environment Variables**
   - Process-level scope (controller process only)
   - Overwritten on each exchange (not cumulative)
   - No risk in single-process controller model

### Audit Trail

**Maintains service account attribution**:
- Azure AD logs show the actual managed identity (via clientID)
- Not the controller's identity
- Preserves audit attribution for compliance

**Example Azure AD Audit Log**:
```
User: aks-staging-flow (managed identity)
Application: aac3d546-358f-4e74-94e5-bb4c472d7cc0
Action: Sign-in
Status: Success
```

### Blast Radius

**Compromised controller scenario**:
- Attacker gains access to controller process
- Can see Azure AD tokens in memory
- **But**: Each token only grants access to vaults the service account has RBAC for
- **Not**: A single credential with access to all vaults
- Service-level impersonation limits damage scope

## Dependencies

### Go Modules to Add

```
github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.x
github.com/Azure/azure-sdk-for-go/sdk/azcore v1.x
```

**Version Selection**:
- Use latest v1.x releases
- Azure SDK follows semantic versioning
- v1.x is stable and production-ready

**Installation**:
```bash
go get github.com/Azure/azure-sdk-for-go/sdk/azidentity
go get github.com/Azure/azure-sdk-for-go/sdk/azcore
go mod tidy
```

## Implementation Checklist

- [ ] Create `planning/azure-token-exchange.md` (this document)
- [ ] Create `azure.go` with stubbed implementation
  - [ ] AzureTokenCache struct
  - [ ] CachedAzureToken struct
  - [ ] NewAzureTokenCache() constructor
  - [ ] GetToken() method (with cache logic)
  - [ ] exchangeToken() method (stubbed)
  - [ ] IsTokenValid() helper
  - [ ] ExtractTenantID() helper
- [ ] Update `controller.go`
  - [ ] Add azureTokenCache field to Controller
  - [ ] Initialize AzureTokenCache in NewController()
  - [ ] Integrate Azure token acquisition in syncCache()
  - [ ] Add logging for Azure token acquisition
- [ ] Add dependencies to `go.mod`
  - [ ] Add azidentity package
  - [ ] Add azcore package
  - [ ] Run go mod tidy
- [ ] Test stubbed implementation
  - [ ] Verify tenant ID extraction
  - [ ] Verify cache operations
  - [ ] Verify logging output
- [ ] Replace stub with real implementation
  - [ ] Implement real exchangeToken()
  - [ ] Add temporary file handling
  - [ ] Add environment variable management
  - [ ] Add error handling
- [ ] Test real implementation
  - [ ] Test against staging AKS cluster
  - [ ] Verify real Azure AD token obtained
  - [ ] Decode and verify token claims
  - [ ] Test token renewal logic
- [ ] Security review
  - [ ] Add token snippet logging
  - [ ] Verify temp file cleanup
  - [ ] Verify file permissions
  - [ ] Review error messages (no token leakage)
- [ ] Documentation
  - [ ] Update session transcript
  - [ ] Update CHANGELOG.md
  - [ ] Update ROADMAP.md
- [ ] Git workflow
  - [ ] Commit stub implementation
  - [ ] Commit real implementation
  - [ ] Push to azure-token-exchange branch
  - [ ] Create pull request
  - [ ] Merge to main

## Next Phase Preview

**Phase 3: Azure Key Vault Integration**

Once we have Azure AD tokens, Phase 3 will:
1. Extract `keyvaultName` from `spec.parameters.keyvaultName`
2. Construct vault URL: `https://{keyvaultName}.vault.azure.net`
3. Create Azure Key Vault client with URL + Azure AD token
4. List secrets from the vault
5. List certificates from the vault
6. Handle RBAC permission errors (403 Forbidden)
7. Handle vault-not-found errors (404)
8. Cache vault contents with TTL

**Key Vault Client Preview**:
```go
import "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

// Create client
client, err := azsecrets.NewClient(
    "https://staging-flow-vault.vault.azure.net",
    azureToken,  // From Phase 2.2
    nil,
)

// List secrets
pager := client.NewListSecretsPager(nil)
for pager.More() {
    page, err := pager.NextPage(ctx)
    // Process secrets
}
```

## References

- [Azure Workload Identity Documentation](https://azure.github.io/azure-workload-identity/docs/introduction.html)
- [Azure SDK for Go - azidentity](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azidentity)
- [Azure Key Vault Authentication](https://learn.microsoft.com/en-us/azure/key-vault/general/authentication-requests-and-responses)
- [Federated Identity Credentials](https://azure.github.io/azure-workload-identity/docs/topics/federated-identity-credential.html)
- [AKS Workload Identity Guide](https://learn.microsoft.com/en-us/azure/aks/workload-identity-overview)
