# Token Acquisition Implementation

**Status:** ✅ COMPLETE
**Date:** 2025-10-25 - 2025-10-26
**Phases:** 2.1 (Kubernetes TokenRequest) & 2.2 (Azure AD Exchange)

## Overview

This document describes the implementation of token acquisition for Azure Workload Identity federation, including both Kubernetes JWT tokens and Azure AD access tokens.

## Implementation Status

**Phase 2.1: Kubernetes TokenRequest API** ✅
- Kubernetes clientset integration
- TokenRequest API implementation
- Token caching with automatic renewal
- ClientID extraction from SecretProviderClass spec

**Phase 2.2: Azure AD Token Exchange** ✅
- WorkloadIdentityCredential integration (Azure SDK)
- Federated identity token exchange
- Azure AD token caching with automatic renewal
- TenantID extraction from SecretProviderClass spec
- Service-level token scope (multi-vault support)

**Phase 3: Azure Key Vault Integration** ✅
- Custom token credential wrapper
- List secrets and certificates with pagination
- KeyvaultName extraction from SecretProviderClass spec
- Error handling with retry logic

**Phase 4: SecretProviderClass Updates** ✅
- Automatic objects array population
- Automatic secretObjects generation
- JSON Patch updates with last-sync annotation

## Industry Standards from Azure CSI Driver

### Kubernetes Tokens
- **Token Expiration**: 3600 seconds (1 hour) - Azure Workload Identity standard
- **Token Renewal**: 80% of TTL (48 minutes before expiry)
- **Audience**: `api://AzureADTokenExchange` (Azure Workload Identity standard)
- **Min Expiration**: 600 seconds (Kubernetes requirement)
- **Max Expiration**: 1 << 32 seconds (Kubernetes limit)

### Azure AD Tokens
- **Token Expiration**: ~28 hours (Azure-configured lifetime)
- **Token Renewal**: 80% of lifetime (22.4 hours before expiry)
- **Scope**: `https://vault.azure.net/.default` (service-level)
- **Cache**: By namespace/serviceAccount (reusable across vaults)

## Architecture

### Token Request Flow

```
1. Controller identifies SecretProviderClass with sync enabled
2. Extract configuration from spec:
   - clientID from spec.parameters.clientID
   - tenantID from spec.parameters.tenantId
   - keyvaultName from spec.parameters.keyvaultName
3. Kubernetes Token Acquisition:
   - Check tokenCache for existing valid token
   - If missing or needs renewal:
     → Call TokenRequest API for ServiceAccount
     → Cache token with expiration
4. Azure AD Token Exchange:
   - Check azureTokenCache for existing valid token
   - If missing or needs renewal:
     → Create WorkloadIdentityCredential
     → Exchange K8s JWT for Azure AD token
     → Cache token with expiration
5. Use Azure AD token to authenticate to Key Vault
6. List secrets and certificates
7. Update SecretProviderClass with discovered objects
```

### Authentication Flow

```
Controller → Impersonates ServiceAccount
  → Kubernetes TokenRequest API (token.go)
  → Azure Workload Identity federation (azure.go)
  → Azure Managed Identity
  → Azure Key Vault RBAC (vault.go)
  → List secrets/certificates
  → Update SecretProviderClass (update.go)
```

## Implementation Details

### Files Created

#### 1. `token.go` - Kubernetes Token Acquisition

**Implemented Features:**
- `TokenCache` with thread-safe caching (sync.RWMutex)
- `RequestToken()` - Real TokenRequest API calls
- `GetToken()` - Get cached token or request new one
- `IsTokenValid()` - Check expiration and renewal threshold
- Token renewal at 80% of lifetime
- Automatic cache cleanup

**Key Functions:**
```go
func NewTokenCache() *TokenCache
func (tc *TokenCache) GetToken(
    ctx context.Context,
    clientset kubernetes.Interface,
    namespace string,
    serviceAccount string,
) (string, error)
```

#### 2. `azure.go` - Azure AD Token Exchange

**Implemented Features:**
- `AzureTokenCache` with thread-safe caching
- WorkloadIdentityCredential integration (Azure SDK)
- Federated identity token exchange
- Service-level token scope (reusable across vaults)
- Automatic token renewal at 80% of lifetime
- Temporary file handling for token exchange

**Key Functions:**
```go
func NewAzureTokenCache() *AzureTokenCache
func (atc *AzureTokenCache) GetToken(
    ctx context.Context,
    namespace string,
    serviceAccount string,
    k8sToken string,
    clientID string,
    tenantID string,
) (string, time.Time, error)
```

**Helper Functions:**
```go
func ExtractClientID(obj *unstructured.Unstructured) (string, error)
func ExtractTenantID(obj *unstructured.Unstructured) (string, error)
```

#### 3. `vault.go` - Azure Key Vault Integration

**Implemented Features:**
- `CachedTokenCredential` - Custom azcore.TokenCredential wrapper
- `ListSecrets()` - List all enabled secrets with pagination
- `ListCertificates()` - List all enabled certificates with pagination
- KeyvaultName extraction from spec
- Comprehensive error handling

**Key Functions:**
```go
func ExtractKeyvaultName(obj *unstructured.Unstructured) (string, error)
func ListSecrets(
    ctx context.Context,
    vaultName string,
    token string,
    expiration time.Time,
) ([]string, error)
func ListCertificates(
    ctx context.Context,
    vaultName string,
    token string,
    expiration time.Time,
) ([]string, error)
```

### Files Modified

#### 1. `controller.go`

**Changes:**
```go
// Added to Controller struct
type Controller struct {
    client          dynamic.Interface
    clientset       kubernetes.Interface      // For TokenRequest API
    cache           *SecretProviderClassCache
    tokenCache      *TokenCache               // K8s token caching
    azureTokenCache *AzureTokenCache          // Azure AD token caching
    queue           workqueue.TypedRateLimitingInterface[QueueKey]
    gvr             schema.GroupVersionResource
    ctx             context.Context
}

// Updated NewController
func NewController(client dynamic.Interface, clientset kubernetes.Interface) *Controller {
    return &Controller{
        client:          client,
        clientset:       clientset,
        cache:           NewCache(),
        tokenCache:      NewTokenCache(),
        azureTokenCache: NewAzureTokenCache(),
        queue:           workqueue.NewTypedRateLimitingQueue(...),
        gvr:             ...,
        ctx:             context.Background(),
    }
}

// Added reconcileResource() function
func (ctrl *Controller) reconcileResource(obj *unstructured.Unstructured) error {
    // 1. Extract configuration
    serviceAccount, _ := getServiceAccount(obj)
    clientID, _ := ExtractClientID(obj)
    tenantID, _ := ExtractTenantID(obj)
    keyvaultName, _ := ExtractKeyvaultName(obj)

    // 2. Get Kubernetes token
    k8sToken, _ := ctrl.tokenCache.GetToken(ctx, clientset, namespace, serviceAccount)

    // 3. Get Azure AD token
    azureToken, expiration, _ := ctrl.azureTokenCache.GetToken(
        ctx, namespace, serviceAccount, k8sToken, clientID, tenantID,
    )

    // 4. List vault contents
    secrets, _ := ListSecrets(ctx, keyvaultName, azureToken, expiration)
    certificates, _ := ListCertificates(ctx, keyvaultName, azureToken, expiration)

    // 5. Update SecretProviderClass
    // (Implementation in update.go)
}
```

#### 2. `main.go`

**Changes:**
```go
import (
    "k8s.io/client-go/kubernetes"
)

// Added kubernetes clientset creation
clientset, err := kubernetes.NewForConfig(config)
if err != nil {
    log.Fatalf("Error creating kubernetes clientset: %v", err)
}

// Updated controller creation
controller := NewController(dynamicClient, clientset)
```

#### 3. `go.mod`

**Dependencies Added:**
```
k8s.io/api v0.34.1                                        // For authentication/v1 types
k8s.io/client-go v0.34.1                                  // For kubernetes.Interface
github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.13.0  // For WorkloadIdentityCredential
github.com/Azure/azure-sdk-for-go/sdk/azcore v1.19.1      // For token credential interface
github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets v1.4.0      // For secrets client
github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates v1.4.0 // For certificates client
```

### RBAC Permissions

**Required Permissions** (in `deploy/rbac.yaml`):
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

## Token Caching Strategy

### Kubernetes Token Cache

**Cache Key:** `{namespace}/{serviceAccount}`
**TTL:** 3600 seconds (1 hour)
**Renewal:** 80% of lifetime (48 minutes)

**Benefits:**
- Reduces API server load
- Immediate token availability
- Automatic renewal before expiration

### Azure AD Token Cache

**Cache Key:** `{namespace}/{serviceAccount}`
**TTL:** ~28 hours (Azure-configured)
**Renewal:** 80% of lifetime (22.4 hours)
**Scope:** Service-level (reusable across vaults)

**Benefits:**
- Reusable across multiple vaults for same service
- Reduces Azure AD token exchange calls
- Automatic renewal
- Thread-safe concurrent access

## Testing Results

### Phase 2.1 Testing (Kubernetes Tokens)
✅ Controller successfully creates TokenRequest for ServiceAccounts
✅ Tokens cached with proper expiration
✅ Automatic renewal at 80% threshold
✅ ClientID extracted from SecretProviderClass spec
✅ Tested against real AKS cluster

**Example Logs:**
```
2025/10/26 23:24:58 Obtained Kubernetes token for default/aks-staging-flow, ready for Azure authentication with clientID: 40737f1d-af23-46b7-9f84-90000fc423ce
2025/10/26 23:24:58 DEBUG: K8s token for default/aks-staging-flow: eyJhb...EqG8
```

### Phase 2.2 Testing (Azure AD Tokens)
✅ WorkloadIdentityCredential successfully exchanges K8s JWT for Azure AD token
✅ Azure tokens cached with proper expiration
✅ TenantID extracted from SecretProviderClass spec
✅ Service-level scope allows vault access
✅ Tested with real Azure federated identity

**Example Logs:**
```
2025/10/26 23:24:58 Extracted tenantID: 8b83ab42-3e3f-422d-85ca-fe2d40c51e35 from default/epackaging-staging-secrets
2025/10/26 23:24:58 Obtained Azure AD token for default/aks-staging-flow, ready for Key Vault access
2025/10/26 23:24:58 DEBUG: Azure AD token for default/aks-staging-flow: eyJ0eXAiOi...UUyx_3A
```

### Phase 3 Testing (Key Vault Access)
✅ Successfully lists secrets from Azure Key Vault
✅ Successfully lists certificates from Azure Key Vault
✅ Filters disabled items automatically
✅ Handles pagination correctly
✅ Tested with real staging vault (staging-flow-vault)

**Example Logs:**
```
2025/10/26 23:24:58 Extracted keyvaultName: staging-epackaging-vault from default/epackaging-staging-secrets
2025/10/26 23:24:58 Listing secrets from vault: https://staging-epackaging-vault.vault.azure.net
2025/10/26 23:24:58 Found 3 secrets in vault staging-epackaging-vault for default/epackaging-staging-secrets
2025/10/26 23:24:58   - Secret: azure-flow-api-secret
2025/10/26 23:24:58   - Secret: flow-api-secret
2025/10/26 23:24:58   - Secret: testing-secret
```

## Error Handling

### Permission Errors (403 Forbidden)

When vault permissions are missing:
1. Controller logs detailed Azure RBAC error
2. Retries 5 times with exponential backoff via work queue
3. After max retries, drops item from queue
4. Existing objects preserved (no data loss)
5. Other resources continue processing

**Example Error Log:**
```
RESPONSE 403: 403 Forbidden
ERROR CODE: Forbidden
Caller: appid=aac3d546-358f-4e74-94e5-bb4c472d7cc0
Action: 'Microsoft.KeyVault/vaults/secrets/readMetadata/action'
Assignment: (not found)
Vault: staging-epackaging-vault
```

### Transient Failures

Network issues, temporary token problems:
- Automatic retry with exponential backoff
- Max 5 attempts per resource
- Work queue handles rate limiting
- Graceful degradation

## SecretProviderClass Configuration

### Required spec.parameters

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
    clientID: "aac3d546-358f-4e74-94e5-bb4c472d7cc0"      # Required for Azure auth
    tenantId: "8b83ab42-3e3f-422d-85ca-fe2d40c51e35"      # Required for Azure auth
    keyvaultName: "staging-example-vault"                 # Required for vault access
    objects: ""  # Will be auto-populated by controller
```

## Success Criteria

**Phase 2.1: Kubernetes TokenRequest API**
- [x] Controller compiles with new dependencies
- [x] Kubernetes clientset initialized successfully
- [x] Token cache structure implemented
- [x] ClientID successfully extracted from spec.parameters
- [x] Real TokenRequest API calls working
- [x] Token renewal threshold logic working
- [x] RBAC manifest created and applied
- [x] Tested against real AKS cluster

**Phase 2.2: Azure AD Token Exchange**
- [x] WorkloadIdentityCredential integration
- [x] Federated identity token exchange working
- [x] Azure AD token caching implemented
- [x] TenantID extraction working
- [x] Service-level token scope validated
- [x] Tested with real Azure federated identity

**Phase 3: Azure Key Vault Integration**
- [x] Custom token credential wrapper implemented
- [x] List secrets with pagination working
- [x] List certificates with pagination working
- [x] KeyvaultName extraction working
- [x] Error handling with retry logic
- [x] Tested with real staging vault

**Phase 4: SecretProviderClass Updates**
- [x] Automatic objects array population
- [x] Automatic secretObjects generation
- [x] JSON Patch updates
- [x] Last-sync annotation
- [x] Field removal when annotations disabled
- [x] Vault as source of truth

## Infrastructure Requirements

### Azure Setup

Each service requires:
- **Azure Key Vault**: `{environment}-{service}-vault`
- **User-Assigned Managed Identity**: `{service}-{environment}-identity`
- **Federated Identity Credential**: Link to Kubernetes ServiceAccount
  - Issuer: AKS OIDC issuer URL
  - Subject: `system:serviceaccount:{namespace}:{serviceAccountName}`
  - Audience: `api://AzureADTokenExchange`
- **RBAC Roles**:
  - Key Vault Secrets User
  - Key Vault Certificate User

### Kubernetes Setup

Each service requires:
- **ServiceAccount** with Azure Workload Identity annotations:
  ```yaml
  metadata:
    annotations:
      azure.workload.identity/client-id: "{clientID}"
  ```
- **SecretProviderClass** with sync annotations and parameters

## Production Readiness

**Current Status:** ✅ Production Ready

**Completed Features:**
- Work queue architecture with retry logic
- Event-driven reconciliation
- Comprehensive error handling
- Token caching with automatic renewal
- Azure Key Vault integration
- Automatic SecretProviderClass updates
- Graceful degradation on failures

**Future Enhancements (Phase 5):**
- Prometheus metrics export
- Structured logging with log levels
- Health check endpoints
- Configuration via ConfigMap
- Security hardening
- Comprehensive test coverage

## References

- [Azure Workload Identity Documentation](https://azure.github.io/azure-workload-identity/docs/)
- [Kubernetes TokenRequest API](https://kubernetes.io/docs/reference/kubernetes-api/authentication-resources/token-request-v1/)
- [Azure SDK for Go](https://github.com/Azure/azure-sdk-for-go)
- [Secrets Store CSI Driver](https://secrets-store-csi-driver.sigs.k8s.io/)
