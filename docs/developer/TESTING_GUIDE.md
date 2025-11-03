# Testing Guide: Azure SDK & Kubernetes API Calls

This guide explains how to test code that interacts with external services (Azure SDK, Kubernetes API) without requiring actual cloud infrastructure.

## Table of Contents
1. [Kubernetes API Testing (Already Implemented)](#kubernetes-api-testing)
2. [Azure SDK Testing (What It Would Take)](#azure-sdk-testing)
3. [Comparison of Approaches](#comparison-of-approaches)

---

## Kubernetes API Testing (Already Implemented)

### ✅ We're Already Doing This!

Our codebase already uses **fake Kubernetes clients** for testing. Here's how it works:

### Example: Testing PatchSecretProviderClass

The `PatchSecretProviderClass` function in `internal/update/update.go` makes real Kubernetes API calls:

```go
func PatchSecretProviderClass(
    ctx context.Context,
    client spcclient.Interface,  // ← This is the key!
    namespace string,
    name string,
    objectsYAML string,
    secretObjects interface{},
    timestamp string,
) error {
    // ... creates JSON patch ...

    // Makes actual Kubernetes API call
    _, err = client.SecretsstoreV1().SecretProviderClasses(namespace).Patch(
        ctx,
        name,
        types.JSONPatchType,
        patchBytes,
        metav1.PatchOptions{},
    )

    return err
}
```

**Key Design Decision**: The function accepts `spcclient.Interface` (an interface) rather than a concrete client type. This enables dependency injection.

### How to Test It

```go
package update_test

import (
    "context"
    "testing"

    "github.com/stretchr/testify/assert"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime"
    secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
    spcfake "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned/fake"

    "github.com/jeanhaley32/azure-keyvault-sync-controller/internal/update"
)

func TestPatchSecretProviderClass(t *testing.T) {
    // 1. Create a fake Kubernetes client
    scheme := runtime.NewScheme()
    _ = secretsstorev1.AddToScheme(scheme)
    fakeClient := spcfake.NewSimpleClientset()

    // 2. Create a test SecretProviderClass in the fake cluster
    spc := &secretsstorev1.SecretProviderClass{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-spc",
            Namespace: "default",
            Annotations: map[string]string{},
        },
        Spec: secretsstorev1.SecretProviderClassSpec{
            Provider: "azure",
            Parameters: map[string]string{
                "objects": "old-value",
            },
        },
    }

    // Add to fake cluster
    _, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Create(
        context.Background(),
        spc,
        metav1.CreateOptions{},
    )
    assert.NoError(t, err)

    // 3. Call the function being tested with the fake client
    err = update.PatchSecretProviderClass(
        context.Background(),
        fakeClient,  // ← Fake client, not real!
        "default",
        "test-spc",
        "new-objects-yaml",
        nil,
        "2024-01-01T00:00:00Z",
    )

    // 4. Verify the patch was applied
    assert.NoError(t, err)

    // 5. Retrieve the patched resource from fake cluster
    updated, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Get(
        context.Background(),
        "test-spc",
        metav1.GetOptions{},
    )

    // 6. Assert the changes were made
    assert.NoError(t, err)
    assert.Equal(t, "new-objects-yaml", updated.Spec.Parameters["objects"])
    assert.Equal(t, "2024-01-01T00:00:00Z", updated.Annotations["azure-keyvault-sync/last-sync"])
}
```

### What Makes This Work

1. **Interface-based design**: `spcclient.Interface` instead of concrete type
2. **Fake clients provided by Kubernetes**: `k8s.io/client-go/kubernetes/fake` and `sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned/fake`
3. **In-memory operations**: All CRUD operations happen in memory, no network calls
4. **Same API**: Fake clients implement the same interface as real clients

### Benefits
- ✅ Fast (no network)
- ✅ Deterministic (no flakiness)
- ✅ No infrastructure required
- ✅ Tests exactly how the code will behave in production

---

## Azure SDK Testing (What It Would Take)

Testing Azure SDK calls is more challenging because:
1. Azure SDK clients are **concrete types**, not interfaces
2. No official fake/mock clients from Microsoft
3. We need to create our own mocking layer

### Current Untested Code

`internal/azure/vault.go`:

```go
func ListSecrets(
    ctx context.Context,
    vaultName string,
    token string,
    expiration time.Time,
) ([]VaultSecret, error) {
    vaultURL := fmt.Sprintf("https://%s.vault.azure.net", vaultName)

    cred := &CachedTokenCredential{
        token:      token,
        expiration: expiration,
    }

    // Creates real Azure client - can't easily mock
    client, err := azsecrets.NewClient(vaultURL, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create secrets client: %w", err)
    }

    // Makes real Azure API calls
    pager := client.NewListSecretPropertiesPager(nil)
    for pager.More() {
        page, err := pager.NextPage(ctx)
        // ... process secrets ...
    }

    return secrets, nil
}
```

### Approach 1: Refactor with Interface (Like Kubernetes)

**Step 1**: Extract an interface for Azure operations

```go
// internal/azure/client.go
package azure

import (
    "context"
    "time"

    "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// VaultSecretsClient interface for listing secrets
type VaultSecretsClient interface {
    NewListSecretPropertiesPager(options *azsecrets.ListSecretPropertiesOptions) *azsecrets.ListSecretPropertiesPager
}

// RealVaultSecretsClient wraps the actual Azure SDK client
type RealVaultSecretsClient struct {
    client *azsecrets.Client
}

func NewRealVaultSecretsClient(vaultURL string, cred *CachedTokenCredential) (*RealVaultSecretsClient, error) {
    client, err := azsecrets.NewClient(vaultURL, cred, nil)
    if err != nil {
        return nil, err
    }
    return &RealVaultSecretsClient{client: client}, nil
}

func (r *RealVaultSecretsClient) NewListSecretPropertiesPager(options *azsecrets.ListSecretPropertiesOptions) *azsecrets.ListSecretPropertiesPager {
    return r.client.NewListSecretPropertiesPager(options)
}
```

**Step 2**: Refactor `ListSecrets` to accept interface

```go
// Updated ListSecrets signature
func ListSecretsWithClient(
    ctx context.Context,
    client VaultSecretsClient,  // ← Interface instead of creating client inside
) ([]VaultSecret, error) {
    pager := client.NewListSecretPropertiesPager(nil)

    var secrets []VaultSecret
    for pager.More() {
        page, err := pager.NextPage(ctx)
        if err != nil {
            return nil, err
        }
        // ... process secrets ...
    }

    return secrets, nil
}

// Keep original function as wrapper for backward compatibility
func ListSecrets(
    ctx context.Context,
    vaultName string,
    token string,
    expiration time.Time,
) ([]VaultSecret, error) {
    vaultURL := fmt.Sprintf("https://%s.vault.azure.net", vaultName)
    cred := &CachedTokenCredential{token: token, expiration: expiration}

    client, err := NewRealVaultSecretsClient(vaultURL, cred)
    if err != nil {
        return nil, err
    }

    return ListSecretsWithClient(ctx, client)
}
```

**Step 3**: Create mock for testing

```go
// internal/azure/client_test.go
package azure

import (
    "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// MockVaultSecretsClient for testing
type MockVaultSecretsClient struct {
    NewListSecretPropertiesPagerFunc func(options *azsecrets.ListSecretPropertiesOptions) *azsecrets.ListSecretPropertiesPager
}

func (m *MockVaultSecretsClient) NewListSecretPropertiesPager(options *azsecrets.ListSecretPropertiesOptions) *azsecrets.ListSecretPropertiesPager {
    if m.NewListSecretPropertiesPagerFunc != nil {
        return m.NewListSecretPropertiesPagerFunc(options)
    }
    // Return mock pager...
    panic("not implemented")
}
```

**Problem**: The `ListSecretPropertiesPager` is also a concrete type, making this approach very complex. We'd need to mock the pager, the page results, etc.

### Approach 2: HTTP Client Mocking (More Practical)

Instead of mocking the entire SDK, mock at the HTTP level:

```go
// internal/azure/vault_test.go
package azure_test

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
)

func TestListSecrets_HTTPMocking(t *testing.T) {
    // 1. Create a mock Azure Key Vault server
    mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Verify request
        assert.Equal(t, "/secrets", r.URL.Path)
        assert.Equal(t, "GET", r.Method)
        assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

        // Return mock response matching Azure API format
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{
            "value": [
                {
                    "id": "https://test-vault.vault.azure.net/secrets/secret1",
                    "attributes": {
                        "enabled": true
                    },
                    "tags": {
                        "environment": "production"
                    }
                }
            ],
            "nextLink": null
        }`))
    }))
    defer mockServer.Close()

    // 2. This would require modifying ListSecrets to accept custom HTTP client
    // or use environment variables to override the vault URL

    // 3. Call the function
    secrets, err := azure.ListSecrets(
        context.Background(),
        "test-vault",  // This would need to resolve to mockServer.URL
        "test-token",
        time.Now().Add(time.Hour),
    )

    // 4. Verify
    assert.NoError(t, err)
    assert.Len(t, secrets, 1)
    assert.Equal(t, "secret1", secrets[0].Name)
}
```

**Problem**: Azure SDK doesn't expose HTTP client configuration in a way that makes this easy.

### Approach 3: Testcontainers with Azurite (Most Realistic)

Use [Azurite](https://learn.microsoft.com/en-us/azure/storage/common/storage-use-azurite) (Azure Storage emulator) or similar:

```go
// +build integration

func TestListSecrets_Integration(t *testing.T) {
    // 1. Start Azurite container
    ctx := context.Background()
    req := testcontainers.ContainerRequest{
        Image:        "mcr.microsoft.com/azure-storage/azurite",
        ExposedPorts: []string{"10000/tcp"},
    }

    azuriteContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: req,
        Started:          true,
    })
    require.NoError(t, err)
    defer azuriteContainer.Terminate(ctx)

    // 2. Get container endpoint
    endpoint, err := azuriteContainer.Endpoint(ctx, "")
    require.NoError(t, err)

    // 3. Run actual Azure SDK code against emulator
    secrets, err := azure.ListSecrets(ctx, endpoint, "test-token", time.Now().Add(time.Hour))

    // 4. Verify
    assert.NoError(t, err)
}
```

**Problem**: Azurite is for Storage, not Key Vault. No official Key Vault emulator exists.

---

## Comparison of Approaches

| Approach | Pros | Cons | Effort | Recommended |
|----------|------|------|--------|-------------|
| **Kubernetes Fake Clients** | ✅ Official support<br>✅ Easy to use<br>✅ Fast<br>✅ Deterministic | None | Low | ✅ Yes (already using) |
| **Azure Interface Extraction** | ✅ Clean design<br>✅ Unit testable | ❌ Complex (need to mock pagers, pages, etc.)<br>❌ Lots of boilerplate | Very High | ❌ No |
| **HTTP Mocking** | ✅ Tests HTTP layer<br>✅ Moderate effort | ❌ Azure SDK doesn't expose HTTP client easily<br>❌ Fragile (Azure API changes break tests) | High | ⚠️  Maybe |
| **Integration Tests** | ✅ Most realistic<br>✅ Tests actual SDK | ❌ No Key Vault emulator<br>❌ Slow<br>❌ Requires infrastructure | High | ⚠️  For CI/CD only |
| **Current Approach** | ✅ Very low effort<br>✅ Tests setup code | ❌ Doesn't test paging logic<br>❌ Doesn't test error handling | Very Low | ✅ Good enough |

---

## Recommendation

### For This Project

**Keep the current hybrid approach:**

1. ✅ **Use fake Kubernetes clients** - we're already doing this well
2. ✅ **Basic Azure tests** - test URL formatting, client creation (what we have now)
3. ✅ **Integration tests in CI/CD** - use real Azure (when needed)

### Why Not Full Azure Mocking?

The **cost-benefit ratio is poor**:
- **Benefit**: Test paging logic, error handling for Azure calls
- **Cost**:
  - Massive refactoring (extract interfaces for every Azure SDK type)
  - Extensive mock infrastructure
  - Maintenance burden (update mocks when Azure SDK changes)
  - Tests become more about mocks than actual logic

### Better Approach

**Test the business logic separately from Azure calls:**

```go
// Good: Testable business logic
func FilterEnabledSecrets(secrets []SecretProperty) []VaultSecret {
    var result []VaultSecret
    for _, secret := range secrets {
        if secret.Attributes != nil &&
           secret.Attributes.Enabled != nil &&
           *secret.Attributes.Enabled {
            result = append(result, VaultSecret{
                Name: secret.Name,
                Tags: secret.Tags,
            })
        }
    }
    return result
}

// Thin wrapper (hard to test, but simple enough to trust)
func ListSecrets(...) ([]VaultSecret, error) {
    client, err := azsecrets.NewClient(vaultURL, cred, nil)
    if err != nil {
        return nil, err
    }

    pager := client.NewListSecretPropertiesPager(nil)
    var allSecrets []SecretProperty
    for pager.More() {
        page, err := pager.NextPage(ctx)
        if err != nil {
            return nil, err
        }
        allSecrets = append(allSecrets, page.Value...)
    }

    return FilterEnabledSecrets(allSecrets), nil
}
```

Now you can easily test `FilterEnabledSecrets` with any test data, and the thin `ListSecrets` wrapper is simple enough that bugs are unlikely.

---

## Summary

**What we're doing now:**
- ✅ Full mocking of Kubernetes API (via fake clients)
- ✅ Basic Azure SDK testing (setup code)
- ✅ Good separation of business logic from SDK calls

**What it would take to fully mock Azure:**
- Extract interfaces for all Azure SDK types
- Create comprehensive mock infrastructure
- Maintain mocks as SDK evolves
- **Estimated effort**: 40-80 hours
- **Benefit**: Marginal (most bugs are in business logic, not SDK usage)

**Recommendation**:
Keep current approach. If you need higher confidence in Azure integration, add integration tests that run against real Azure in CI/CD (using Azure credentials as secrets).
