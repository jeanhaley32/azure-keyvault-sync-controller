# Token Acquisition - Stubbed Implementation Plan

**Branch:** `token-acquisition`
**Date:** 2025-10-25
**Status:** Ready to implement

## Overview
Create the complete token acquisition infrastructure with stubbed implementations that log what would happen instead of making real API calls.

## What is a Stub?

A **stub** is a stand-in for functionality that we are developing. It has the same structure and interfaces as the real implementation, but instead of doing the actual work, it:
- Logs what it would do
- Returns fake but realistic data
- Allows testing of structure and flow without full infrastructure

### Example: Real vs Stubbed

**REAL Implementation (future):**
```go
func (ctrl *Controller) requestToken(ctx context.Context, namespace, serviceAccountName string) (string, time.Time, error) {
    tokenRequest := &authenticationv1.TokenRequest{
        Spec: authenticationv1.TokenRequestSpec{
            Audiences:         []string{"api://AzureADTokenExchange"},
            ExpirationSeconds: pointer.Int64(3600),
        },
    }

    // Actually calls Kubernetes API
    result, err := ctrl.clientset.CoreV1().
        ServiceAccounts(namespace).
        CreateToken(ctx, serviceAccountName, tokenRequest, metav1.CreateOptions{})

    return result.Status.Token, result.Status.ExpirationTimestamp.Time, nil
}
```

**STUBBED Implementation (now):**
```go
func (ctrl *Controller) requestToken(ctx context.Context, namespace, serviceAccountName string) (string, time.Time, error) {
    // Log what we would do
    log.Printf("STUB: Would request token for serviceaccount %s/%s", namespace, serviceAccountName)
    log.Printf("STUB: TokenRequest would have audience=api://AzureADTokenExchange, expirationSeconds=3600")

    // Create fake token
    stubToken := fmt.Sprintf("stub-token-%s-%s-%d", namespace, serviceAccountName, time.Now().Unix())
    stubExpiration := time.Now().Add(3600 * time.Second)

    log.Printf("STUB: Generated fake token: %s", stubToken)
    log.Printf("STUB: Fake expiration: %s", stubExpiration.Format(time.RFC3339))

    // Return fake data
    return stubToken, stubExpiration, nil
}
```

**Key differences:**
- ❌ No actual Kubernetes API calls
- ✅ Logs show what would happen
- ✅ Returns fake but realistic data
- ✅ Same function signature (can swap with real later)
- ✅ Tests the structure and integration

## Implementation Steps

### Step 1: Create `token.go` with Token Cache and Stub Functions

**File:** `token.go`

**Contents:**
```go
package main

import (
    "context"
    "fmt"
    "log"
    "sync"
    "time"

    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/client-go/kubernetes"
)

const (
    tokenExpirationSeconds = 3600                      // 1 hour
    tokenAudience         = "api://AzureADTokenExchange"
    tokenRenewalThreshold = 0.8                        // Renew at 80% of lifetime
)

// TokenCache manages cached service account tokens
type TokenCache struct {
    mu     sync.RWMutex
    tokens map[string]*CachedToken
}

// CachedToken represents a cached token with metadata
type CachedToken struct {
    Token          string
    ExpirationTime time.Time
    Namespace      string
    ServiceAccount string
}

// NewTokenCache creates a new token cache
func NewTokenCache() *TokenCache {
    return &TokenCache{
        tokens: make(map[string]*CachedToken),
    }
}

// cacheKey generates a unique key for namespace/serviceAccount
func tokenCacheKey(namespace, serviceAccount string) string {
    return fmt.Sprintf("%s/%s", namespace, serviceAccount)
}

// requestToken is a STUBBED function that logs token request behavior
func (tc *TokenCache) requestToken(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, time.Time, error) {
    log.Printf("STUB: Would request token for serviceaccount %s/%s", namespace, serviceAccount)
    log.Printf("STUB: TokenRequest would have audience=%s, expirationSeconds=%d", tokenAudience, tokenExpirationSeconds)

    // Create fake token
    stubToken := fmt.Sprintf("stub-token-%s-%s-%d", namespace, serviceAccount, time.Now().Unix())
    stubExpiration := time.Now().Add(time.Duration(tokenExpirationSeconds) * time.Second)

    log.Printf("STUB: Generated fake token: %s", stubToken)
    log.Printf("STUB: Fake expiration: %s", stubExpiration.Format(time.RFC3339))

    return stubToken, stubExpiration, nil
}

// IsTokenValid checks if token exists and hasn't reached renewal threshold
func (tc *TokenCache) IsTokenValid(namespace, serviceAccount string) bool {
    tc.mu.RLock()
    defer tc.mu.RUnlock()

    cached, exists := tc.tokens[tokenCacheKey(namespace, serviceAccount)]
    if !exists {
        return false
    }

    // Calculate renewal time (80% of token lifetime)
    now := time.Now()
    renewalTime := cached.ExpirationTime.Add(-time.Duration(float64(tokenExpirationSeconds) * (1 - tokenRenewalThreshold)) * time.Second)

    // Token is valid if we haven't reached renewal threshold
    return now.Before(renewalTime)
}

// GetToken retrieves a cached token or requests a new one
func (tc *TokenCache) GetToken(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error) {
    // Check if we have a valid cached token
    if tc.IsTokenValid(namespace, serviceAccount) {
        tc.mu.RLock()
        token := tc.tokens[tokenCacheKey(namespace, serviceAccount)].Token
        tc.mu.RUnlock()
        log.Printf("Using cached token for %s/%s", namespace, serviceAccount)
        return token, nil
    }

    // Request new token (stubbed)
    token, expiration, err := tc.requestToken(ctx, clientset, namespace, serviceAccount)
    if err != nil {
        return "", err
    }

    // Cache the token
    tc.mu.Lock()
    tc.tokens[tokenCacheKey(namespace, serviceAccount)] = &CachedToken{
        Token:          token,
        ExpirationTime: expiration,
        Namespace:      namespace,
        ServiceAccount: serviceAccount,
    }
    tc.mu.Unlock()

    return token, nil
}

// ExtractClientID extracts the clientID from SecretProviderClass spec.parameters
func ExtractClientID(obj *unstructured.Unstructured) (string, error) {
    clientID, found, err := unstructured.NestedString(obj.Object, "spec", "parameters", "clientID")
    if err != nil {
        return "", fmt.Errorf("error accessing spec.parameters.clientID: %w", err)
    }
    if !found || clientID == "" {
        return "", fmt.Errorf("clientID not found in spec.parameters")
    }

    log.Printf("Extracted clientID: %s from %s/%s", clientID, obj.GetNamespace(), obj.GetName())
    return clientID, nil
}
```

**What this does:**
- Creates token cache with thread-safe operations
- Implements stubbed token request that logs instead of calling API
- Checks token validity and renewal thresholds
- Extracts clientID from SecretProviderClass spec
- Returns fake tokens for testing

### Step 2: Update `controller.go` - Add Fields

**Modify Controller struct:**
```go
type Controller struct {
    client     dynamic.Interface
    clientset  kubernetes.Interface      // NEW - for TokenRequest API
    cache      *SecretProviderClassCache
    tokenCache *TokenCache               // NEW - token caching
    gvr        schema.GroupVersionResource
    ctx        context.Context
}
```

**Update NewController:**
```go
func NewController(client dynamic.Interface, clientset kubernetes.Interface) *Controller {
    return &Controller{
        client:     client,
        clientset:  clientset,              // NEW
        cache:      NewCache(),
        tokenCache: NewTokenCache(),        // NEW
        gvr: schema.GroupVersionResource{
            Group:    "secrets-store.csi.x-k8s.io",
            Version:  "v1",
            Resource: "secretproviderclasses",
        },
        ctx: context.Background(),
    }
}
```

### Step 3: Update `controller.go` - Integrate Token Acquisition

**Modify syncCache() function:**

Add this logic after the existing validation check:

```go
func (ctrl *Controller) syncCache() {
    log.Println("Performing full resync")
    result, err := ctrl.client.Resource(ctrl.gvr).Namespace("").List(ctrl.ctx, metav1.ListOptions{})
    if err != nil {
        log.Printf("Error listing SecretProviderClasses: %v", err)
        return
    }

    enabledCount := 0
    validCount := 0
    for _, item := range result.Items {
        if isSyncEnabled(&item) {
            enabledCount++
        }
        if valid, serviceAccount := isValidForSync(&item); valid {
            // NEW: Extract clientID from spec
            clientID, err := ExtractClientID(&item)
            if err != nil {
                log.Printf("Warning: %s/%s missing clientID: %v", item.GetNamespace(), item.GetName(), err)
                continue
            }

            // NEW: Get token (stubbed)
            token, err := ctrl.tokenCache.GetToken(
                ctrl.ctx,
                ctrl.clientset,
                item.GetNamespace(),
                serviceAccount,
            )
            if err != nil {
                log.Printf("Error getting token for %s/%s: %v", item.GetNamespace(), item.GetName(), err)
                continue
            }

            // NEW: Log what we would do with the token
            log.Printf("STUB: Would authenticate to Azure with clientID: %s using token: %s", clientID, token[:20]+"...")

            // Existing cache logic
            ctrl.cache.Set(item.GetNamespace(), item.GetName(), item.DeepCopy())
            validCount++
        } else if isSyncEnabled(&item) {
            log.Printf("Warning: %s/%s has sync enabled but missing service-account annotation", item.GetNamespace(), item.GetName())
        }
    }

    log.Printf("Resync complete: %d objects in cache (%d total, %d enabled, %d valid)", validCount, len(result.Items), enabledCount, validCount)
}
```

### Step 4: Update `main.go` - Add Kubernetes Clientset

**Add import:**
```go
import (
    "log"
    "path/filepath"

    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/kubernetes"  // NEW
    "k8s.io/client-go/tools/clientcmd"
    "k8s.io/client-go/util/homedir"
)
```

**Add clientset creation after config:**
```go
func main() {
    log.Println("Starting SecretProviderClass watcher")

    var kubeconfig string
    if home := homedir.HomeDir(); home != "" {
        kubeconfig = filepath.Join(home, ".kube", "config")
    } else {
        log.Fatal("Unable to find home directory")
    }

    config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
    if err != nil {
        log.Fatalf("Error building kubeconfig: %v", err)
    }

    dynamicClient, err := dynamic.NewForConfig(config)
    if err != nil {
        log.Fatalf("Error creating dynamic client: %v", err)
    }

    // NEW: Create kubernetes clientset
    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        log.Fatalf("Error creating kubernetes clientset: %v", err)
    }

    // NEW: Pass clientset to controller
    controller := NewController(dynamicClient, clientset)
    controller.Run()
}
```

### Step 5: Update `go.mod` Dependencies

**Verify these dependencies exist:**
```
k8s.io/api v0.34.1              // For authentication/v1 types
k8s.io/apimachinery v0.34.1     // Already present
k8s.io/client-go v0.34.1        // Already present
```

**Run:**
```bash
go mod tidy
```

### Step 6: Create `deploy/rbac.yaml`

**File:** `deploy/rbac.yaml`

```yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: azure-keyvault-sync-controller
  namespace: kube-system
  labels:
    app: azure-keyvault-sync-controller

---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: azure-keyvault-sync-controller
  labels:
    app: azure-keyvault-sync-controller
rules:
  # SecretProviderClass permissions
  - apiGroups: ["secrets-store.csi.x-k8s.io"]
    resources: ["secretproviderclasses"]
    verbs: ["get", "list", "watch", "update", "patch"]

  # Token request permissions (for service account impersonation)
  - apiGroups: [""]
    resources: ["serviceaccounts/token"]
    verbs: ["create"]

---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: azure-keyvault-sync-controller
  labels:
    app: azure-keyvault-sync-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: azure-keyvault-sync-controller
subjects:
  - kind: ServiceAccount
    name: azure-keyvault-sync-controller
    namespace: kube-system
```

**Note:** This won't be applied yet - manual application later when needed.

### Step 7: Create `deploy/deployment.yaml`

**File:** `deploy/deployment.yaml`

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: azure-keyvault-sync-controller
  namespace: kube-system
  labels:
    app: azure-keyvault-sync-controller
spec:
  replicas: 1
  selector:
    matchLabels:
      app: azure-keyvault-sync-controller
  template:
    metadata:
      labels:
        app: azure-keyvault-sync-controller
    spec:
      serviceAccountName: azure-keyvault-sync-controller
      containers:
        - name: controller
          image: <registry>/azure-keyvault-sync-controller:latest  # TBD
          imagePullPolicy: IfNotPresent
          resources:
            requests:
              memory: "64Mi"
              cpu: "100m"
            limits:
              memory: "128Mi"
              cpu: "200m"
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            runAsUser: 65534
            capabilities:
              drop:
                - ALL
```

**Note:** Template for future use - image path TBD.

### Step 8: Test Compilation

**Commands:**
```bash
# Build the controller
go build

# Check for errors
echo $?  # Should be 0
```

### Step 9: Test Runtime Locally

**Create test SecretProviderClass:**

Save as `test-spc.yaml`:
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

**Apply and run:**
```bash
# Apply test object
kubectl apply -f test-spc.yaml

# Run controller
./azure-keyvault-sync-controller
```

### Step 10: Verify Log Output

**Expected logs:**
```
2025/10/25 12:00:00 Starting SecretProviderClass watcher
2025/10/25 12:00:00 Performing full resync
2025/10/25 12:00:00 Extracted clientID: 12345678-1234-1234-1234-123456789012 from default/test-token-acquisition
2025/10/25 12:00:00 STUB: Would request token for serviceaccount default/test-app
2025/10/25 12:00:00 STUB: TokenRequest would have audience=api://AzureADTokenExchange, expirationSeconds=3600
2025/10/25 12:00:00 STUB: Generated fake token: stub-token-default-test-app-1729876543
2025/10/25 12:00:00 STUB: Fake expiration: 2025-10-25T13:00:00Z
2025/10/25 12:00:00 STUB: Would authenticate to Azure with clientID: 12345678-1234-1234-1234-123456789012 using token: stub-token-default-t...
2025/10/25 12:00:00 Resync complete: 1 objects in cache (1 total, 1 enabled, 1 valid)

--- Current SecretProviderClass objects: 1 ---
  default/test-token-acquisition
---
2025/10/25 12:00:00 Watching for events...
```

**What the logs tell us:**
- ✅ ClientID extraction working
- ✅ Token request logic executing (stubbed)
- ✅ Fake tokens generated with correct format
- ✅ Cache operations working
- ✅ Integration with controller working

## Success Criteria Checklist

- [ ] `token.go` created with complete stub implementation
- [ ] `controller.go` updated with clientset and tokenCache fields
- [ ] `controller.go` syncCache() integrated with token acquisition
- [ ] `main.go` updated with clientset initialization
- [ ] `go.mod` dependencies verified and tidy
- [ ] `deploy/rbac.yaml` created
- [ ] `deploy/deployment.yaml` created
- [ ] Code compiles without errors
- [ ] Runtime produces expected stub log output
- [ ] ClientID successfully extracted from spec.parameters
- [ ] Token cache operations working correctly
- [ ] No runtime errors during testing

## Next Steps (Future Phases)

### Replace Stubs with Real Implementation
When ready to implement real token acquisition:

1. **Modify `requestToken()` in token.go:**
   - Remove stub logging
   - Add real TokenRequest API call
   - Return actual JWT from Kubernetes

2. **Add error handling:**
   - Handle authentication failures
   - Handle missing RBAC permissions
   - Retry logic for transient errors

3. **Test with real cluster:**
   - Apply RBAC manifests
   - Verify token request permissions
   - Test token validation

### Phase 2.2: Azure AD Token Exchange
- Use Kubernetes token to get Azure AD token
- Integrate Azure Identity SDK
- Handle federated credential validation

### Phase 3: Azure Key Vault Integration
- Use Azure AD token for vault access
- List secrets and certificates
- Update SecretProviderClass objects array

## Benefits of Stubbing First

1. **Fast Iteration**: Test structure without infrastructure setup
2. **Clear Separation**: Know exactly what's real vs stubbed
3. **Easy Testing**: Verify logic without external dependencies
4. **Incremental**: Swap stubs for real implementation one at a time
5. **Debugging**: Logs clearly show intended behavior
6. **Structure Validation**: Ensure design is correct before committing to implementation

## Notes

- All stub functions clearly labeled with "STUB:" prefix in logs
- Fake tokens use predictable format for debugging
- Cache structure is production-ready (only requestToken() is stubbed)
- RBAC permissions defined for when we implement real token requests
- Can run and test without any cluster permissions (stubs don't make API calls)
