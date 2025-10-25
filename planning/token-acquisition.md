# Token Acquisition Implementation Plan

**Branch:** `token-acquisition`
**Date:** 2025-10-25
**Phase:** 2.1 - Kubernetes TokenRequest API (Stubbed)

## Objective

Implement the infrastructure for Kubernetes TokenRequest API integration with stubbed/logging implementation to test the flow before adding real token acquisition logic.

## Research Findings

### Industry Standards from Azure CSI Driver
- **Token Expiration**: 3600 seconds (1 hour) - Azure Workload Identity standard
- **Token Renewal**: Kubelet auto-renews at 80% of TTL (48 minutes for 1-hour tokens)
- **Audience**: `api://AzureADTokenExchange` (Azure Workload Identity standard)
- **Min Expiration**: 600 seconds (Kubernetes requirement)
- **Max Expiration**: 1 << 32 seconds (Kubernetes limit)

### Token Request Flow
1. Kubernetes API Server acts as OIDC issuer
2. Controller calls TokenRequest API for target ServiceAccount
3. Kubernetes issues signed JWT with:
   - `iss`: Cluster's OIDC issuer URL
   - `sub`: `system:serviceaccount:{namespace}:{name}`
   - `aud`: `api://AzureADTokenExchange`
   - `exp`: Current time + expirationSeconds
4. Token used for Azure AD exchange (future phase)

## Implementation Approach: Stubbed with Logging

This phase creates the complete infrastructure but **logs what would happen** instead of making real API calls. This allows us to:
- Test the structure and flow
- Verify client ID extraction
- Validate cache operations
- Ensure proper integration points
- Confirm RBAC requirements

## Files to Create

### 1. `token.go` - Token Acquisition Infrastructure

**Purpose:** Token cache, request logic, and renewal handling

**Contents:**
```go
// Constants
- tokenExpirationSeconds = 3600
- tokenAudience = "api://AzureADTokenExchange"
- tokenRenewalThreshold = 0.8

// Structs
- TokenCache (with sync.RWMutex)
- CachedToken (token, expiration, namespace, serviceAccount)

// Functions
- NewTokenCache() - Initialize cache
- RequestToken() - Stubbed token request with logging
- GetToken() - Get cached token or request new one
- IsTokenValid() - Check expiration and renewal threshold
- ExtractClientID() - Parse clientID from SecretProviderClass spec
```

**Stubbed Behavior:**
- Log: "Would request token for {namespace}/{serviceAccount}"
- Log: "TokenRequest: audience={audience}, expirationSeconds={seconds}"
- Create stub token in cache with fake expiration
- Return stub token string

### 2. `deploy/rbac.yaml` - RBAC Permissions

**Purpose:** Define controller ServiceAccount and permissions

**Contents:**
```yaml
# ServiceAccount
- name: azure-keyvault-sync-controller
- namespace: kube-system

# ClusterRole permissions
- secretproviderclasses: get, list, watch, update, patch
- serviceaccounts/token: create  # NEW for token acquisition

# ClusterRoleBinding
- Binds ServiceAccount to ClusterRole
```

**Notes:**
- Template provided for future Helm chart
- Will be applied manually for now
- Critical permission: `serviceaccounts/token` create

### 3. `deploy/deployment.yaml` - Controller Deployment

**Purpose:** Deployment manifest for controller

**Contents:**
```yaml
# Deployment
- serviceAccountName: azure-keyvault-sync-controller
- Single replica
- Resource limits/requests
- Image reference (TBD)
- Environment variables (if needed)
```

## Files to Modify

### 1. `controller.go`

**Changes:**
```go
// Add to Controller struct
type Controller struct {
    client    dynamic.Interface
    clientset kubernetes.Interface  // NEW - for TokenRequest API
    cache     *SecretProviderClassCache
    tokenCache *TokenCache           // NEW - token caching
    gvr       schema.GroupVersionResource
    ctx       context.Context
}

// Update NewController
func NewController(client dynamic.Interface, clientset kubernetes.Interface) *Controller {
    return &Controller{
        client:     client,
        clientset:  clientset,  // NEW
        cache:      NewCache(),
        tokenCache: NewTokenCache(),  // NEW
        gvr:        ...,
        ctx:        context.Background(),
    }
}

// Update syncCache() to add token acquisition
func (ctrl *Controller) syncCache() {
    // ... existing list logic ...

    for _, item := range result.Items {
        if valid, serviceAccount := isValidForSync(&item); valid {
            // NEW: Extract clientID from spec
            clientID, err := ExtractClientID(&item)
            if err != nil {
                log.Printf("Warning: %s/%s missing clientID in spec", ...)
                continue
            }

            // NEW: Request token (stubbed)
            token, err := ctrl.tokenCache.GetToken(
                ctrl.ctx,
                ctrl.clientset,
                item.GetNamespace(),
                serviceAccount,
            )
            if err != nil {
                log.Printf("Error getting token: %v", err)
                continue
            }

            // NEW: Log token acquisition (stubbed)
            log.Printf("Token acquired for %s/%s, would use for Azure auth with clientID: %s",
                item.GetNamespace(), serviceAccount, clientID)

            // Existing cache logic
            ctrl.cache.Set(...)
        }
    }
}
```

### 2. `main.go`

**Changes:**
```go
// Add import
import (
    "k8s.io/client-go/kubernetes"
)

// After dynamic client creation, add:
clientset, err := kubernetes.NewForConfig(config)
if err != nil {
    log.Fatalf("Error creating kubernetes clientset: %v", err)
}

// Update controller creation:
controller := NewController(dynamicClient, clientset)
```

### 3. `go.mod`

**Verify Dependencies:**
```
k8s.io/api v0.34.1              // For authentication/v1 types
k8s.io/client-go v0.34.1        // For kubernetes.Interface
k8s.io/apimachinery v0.34.1     // Already present
```

## Implementation Flow

### Token Request Flow (Stubbed)

```
1. syncCache() iterates SecretProviderClass objects
2. For each valid object:
   a. Extract clientID from spec.parameters.clientID
   b. Check tokenCache for existing valid token
   c. If token missing or needs renewal:
      - Log: "Would request token for {namespace}/{serviceAccount}"
      - Log: "TokenRequest params: audience=api://AzureADTokenExchange, expirationSeconds=3600"
      - Create stub token entry: "stub-token-{timestamp}"
      - Set fake expiration: now + 3600 seconds
      - Store in cache
   d. Log: "Would authenticate to Azure with clientID: {clientID}"
   e. Log: "Token valid until: {expiration}"
3. Continue with existing cache operations
```

### Client ID Extraction

Parse SecretProviderClass spec:
```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: example-app-secrets
  namespace: default
  annotations:
    azure-keyvault-sync/enabled: "true"
    azure-keyvault-sync/service-account: "example-app"
spec:
  provider: azure
  parameters:
    clientID: "00000000-0000-0000-0000-000000000000"  # Extract this
    keyvaultName: "prod-example-vault"
    tenantId: "11111111-1111-1111-1111-111111111111"
```

Use unstructured access:
```go
clientID, found, err := unstructured.NestedString(obj.Object, "spec", "parameters", "clientID")
```

## Testing Approach

### 1. Local Testing
```bash
# Build controller
go build

# Run against test cluster
./azure-keyvault-sync-controller
```

### 2. Create Test SecretProviderClass
```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: test-token-acquisition
  namespace: default
  annotations:
    azure-keyvault-sync/enabled: "true"
    azure-keyvault-sync/service-account: "test-app"
spec:
  provider: azure
  parameters:
    clientID: "12345678-1234-1234-1234-123456789012"
    keyvaultName: "test-vault"
    tenantId: "87654321-4321-4321-4321-210987654321"
```

### 3. Expected Log Output
```
2025/10/25 12:00:00 Performing full resync
2025/10/25 12:00:00 Would request token for default/test-app
2025/10/25 12:00:00 TokenRequest: audience=api://AzureADTokenExchange, expirationSeconds=3600
2025/10/25 12:00:00 Token acquired for default/test-app, would use for Azure auth with clientID: 12345678-1234-1234-1234-123456789012
2025/10/25 12:00:00 Token valid until: 2025-10-25T13:00:00Z
2025/10/25 12:00:00 Resync complete: 1 objects in cache (1 total, 1 enabled, 1 valid)
```

## Success Criteria

- [x] Planning directory created
- [x] Plan document saved to planning/token-acquisition.md
- [ ] Controller compiles with new dependencies
- [ ] Kubernetes clientset initialized successfully
- [ ] Token cache structure implemented
- [ ] ClientID successfully extracted from spec.parameters
- [ ] Logs show complete token acquisition flow
- [ ] Token renewal threshold logic working (even with stub data)
- [ ] RBAC manifest created and valid
- [ ] Deployment manifest created
- [ ] No runtime errors during testing
- [ ] Clean, informative log output

## Future Steps (Not This Phase)

### Phase 2.2: Real Token Acquisition
- Replace stub with actual TokenRequest API call
- Implement real token validation
- Add proper error handling for auth failures

### Phase 2.3: Azure AD Token Exchange
- Use Kubernetes token to get Azure AD token
- Integrate Azure Identity SDK
- Handle federated credential validation

### Phase 3: Azure Key Vault Integration
- Use Azure AD token to authenticate to Key Vault
- List secrets and certificates
- Handle vault permissions and errors

## Notes

- This phase focuses on **structure and flow** validation
- All Azure interactions are stubbed/logged
- Real implementation swapped in incrementally
- RBAC template provided but applied manually
- Token cache ready for production use
- Follows Azure Workload Identity best practices from CSI driver research

## Questions Resolved

1. **Token Expiration**: 3600 seconds (matching Azure standard)
2. **Renewal Strategy**: 80% of TTL (matching Kubelet behavior)
3. **Client ID Source**: spec.parameters.clientID
4. **Implementation Approach**: Stubbed with logging
5. **RBAC Template**: Yes, in deploy/rbac.yaml
