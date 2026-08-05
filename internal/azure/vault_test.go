package azure

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestCachedTokenCredential tests the CachedTokenCredential implementation
func TestCachedTokenCredential(t *testing.T) {
	expectedToken := "test-azure-token"
	expectedExpiration := time.Now().Add(1 * time.Hour)

	cred := &CachedTokenCredential{
		token:      expectedToken,
		expiration: expectedExpiration,
	}

	ctx := context.Background()
	opts := policy.TokenRequestOptions{
		Scopes: []string{keyVaultScope},
	}

	accessToken, err := cred.GetToken(ctx, opts)

	assert.NoError(t, err)
	assert.Equal(t, expectedToken, accessToken.Token)
	// Use WithinDuration for time comparison to allow for minor timing differences
	assert.WithinDuration(t, expectedExpiration, accessToken.ExpiresOn, time.Second)
}

// TestCachedTokenCredentialWithDifferentScopes tests that credential works with any scope
func TestCachedTokenCredentialWithDifferentScopes(t *testing.T) {
	cred := &CachedTokenCredential{
		token:      "test-token",
		expiration: time.Now().Add(1 * time.Hour),
	}

	ctx := context.Background()

	scopes := [][]string{
		{keyVaultScope},
		{"https://management.azure.com/.default"},
		{"https://graph.microsoft.com/.default"},
		{},
	}

	for _, scopeList := range scopes {
		opts := policy.TokenRequestOptions{
			Scopes: scopeList,
		}

		accessToken, err := cred.GetToken(ctx, opts)
		assert.NoError(t, err)
		assert.Equal(t, "test-token", accessToken.Token)
	}
}

// TestCachedTokenCredentialImplementsInterface tests that CachedTokenCredential implements azcore.TokenCredential
func TestCachedTokenCredentialImplementsInterface(t *testing.T) {
	cred := &CachedTokenCredential{
		token:      "test",
		expiration: time.Now().Add(1 * time.Hour),
	}

	// This will fail to compile if CachedTokenCredential doesn't implement azcore.TokenCredential
	var _ azcore.TokenCredential = cred
}

// TestVaultSecretStructure tests the VaultSecret structure
func TestVaultSecretStructure(t *testing.T) {
	tags := map[string]*string{
		"service":     stringPtr("web-api"),
		"environment": stringPtr("production"),
	}

	secret := VaultSecret{
		Name: "database-password",
		Tags: tags,
	}

	assert.Equal(t, "database-password", secret.Name)
	assert.NotNil(t, secret.Tags)
	assert.Equal(t, "web-api", *secret.Tags["service"])
	assert.Equal(t, "production", *secret.Tags["environment"])
}

// TestVaultCertificateStructure tests the VaultCertificate structure
func TestVaultCertificateStructure(t *testing.T) {
	tags := map[string]*string{
		"service": stringPtr("api-gateway"),
		"type":    stringPtr("tls"),
	}

	cert := VaultCertificate{
		Name: "api-tls-cert",
		Tags: tags,
	}

	assert.Equal(t, "api-tls-cert", cert.Name)
	assert.NotNil(t, cert.Tags)
	assert.Equal(t, "api-gateway", *cert.Tags["service"])
	assert.Equal(t, "tls", *cert.Tags["type"])
}

// TestExtractKeyvaultName tests keyvault name extraction from SecretProviderClass
func TestExtractKeyvaultName(t *testing.T) {
	tests := []struct {
		name          string
		obj           *unstructured.Unstructured
		expectError   bool
		expectedValue string
		errorContains string
	}{
		{
			name: "valid keyvaultName",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"keyvaultName": "my-key-vault",
						},
					},
				},
			},
			expectError:   false,
			expectedValue: "my-key-vault",
		},
		{
			name: "SSRF payload rejected (gh-68)",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"keyvaultName": "attacker.example.com/x",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid keyvaultName",
		},
		{
			name: "missing keyvaultName field",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"other": "value",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "keyvaultName not found",
		},
		{
			name: "empty keyvaultName",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"keyvaultName": "",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "keyvaultName not found",
		},
		{
			name: "missing parameters",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{},
				},
			},
			expectError:   true,
			errorContains: "keyvaultName not found",
		},
		{
			name: "missing spec",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
				},
			},
			expectError:   true,
			errorContains: "keyvaultName not found",
		},
		{
			name: "keyvaultName with hyphens",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"keyvaultName": "my-prod-vault-2024",
						},
					},
				},
			},
			expectError:   false,
			expectedValue: "my-prod-vault-2024",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaultName, err := ExtractKeyvaultName(tt.obj)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.Empty(t, vaultName)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedValue, vaultName)
			}
		})
	}
}

// TestVaultSecretWithNilTags tests VaultSecret with nil tags
func TestVaultSecretWithNilTags(t *testing.T) {
	secret := VaultSecret{
		Name: "secret-without-tags",
		Tags: nil,
	}

	assert.Equal(t, "secret-without-tags", secret.Name)
	assert.Nil(t, secret.Tags)
}

// TestVaultCertificateWithNilTags tests VaultCertificate with nil tags
func TestVaultCertificateWithNilTags(t *testing.T) {
	cert := VaultCertificate{
		Name: "cert-without-tags",
		Tags: nil,
	}

	assert.Equal(t, "cert-without-tags", cert.Name)
	assert.Nil(t, cert.Tags)
}

// TestVaultSecretWithEmptyTags tests VaultSecret with empty tags map
func TestVaultSecretWithEmptyTags(t *testing.T) {
	secret := VaultSecret{
		Name: "secret-with-empty-tags",
		Tags: map[string]*string{},
	}

	assert.Equal(t, "secret-with-empty-tags", secret.Name)
	assert.NotNil(t, secret.Tags)
	assert.Equal(t, 0, len(secret.Tags))
}

// TestCachedTokenCredentialExpiredToken tests credential with expired token
func TestCachedTokenCredentialExpiredToken(t *testing.T) {
	// Create credential with expired token
	cred := &CachedTokenCredential{
		token:      "expired-token",
		expiration: time.Now().Add(-1 * time.Hour), // Expired 1 hour ago
	}

	ctx := context.Background()
	opts := policy.TokenRequestOptions{
		Scopes: []string{keyVaultScope},
	}

	// GetToken still returns the token - it doesn't validate expiration
	// (that's the caller's responsibility)
	accessToken, err := cred.GetToken(ctx, opts)

	assert.NoError(t, err)
	assert.Equal(t, "expired-token", accessToken.Token)
	assert.True(t, accessToken.ExpiresOn.Before(time.Now()))
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}

// TestListSecretsIntegration is skipped because it requires actual Azure credentials
func TestListSecretsIntegration(t *testing.T) {
	t.Skip("ListSecrets requires actual Azure Key Vault access - integration test only")

	// NOTE: To test ListSecrets properly, you would need:
	// 1. Valid Azure credentials
	// 2. An actual Azure Key Vault
	// 3. Secrets in that vault
	//
	// This is better suited for integration tests rather than unit tests.
	// The function is relatively simple and primarily delegates to Azure SDK.
}

// TestListCertificatesIntegration is skipped because it requires actual Azure credentials
func TestListCertificatesIntegration(t *testing.T) {
	t.Skip("ListCertificates requires actual Azure Key Vault access - integration test only")

	// NOTE: To test ListCertificates properly, you would need:
	// 1. Valid Azure credentials
	// 2. An actual Azure Key Vault
	// 3. Certificates in that vault
	//
	// This is better suited for integration tests rather than unit tests.
	// The function is relatively simple and primarily delegates to Azure SDK.
}
