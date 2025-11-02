package controller

import (
	"context"
	"time"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/token"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/update"
	"k8s.io/client-go/kubernetes"
	spcclient "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned"
)

// RealTokenProvider implements TokenProvider using the real token caches
type RealTokenProvider struct {
	tokenCache      *token.TokenCache
	azureTokenCache *azure.AzureTokenCache
}

// NewRealTokenProvider creates a new RealTokenProvider
func NewRealTokenProvider(tokenCache *token.TokenCache, azureTokenCache *azure.AzureTokenCache) *RealTokenProvider {
	return &RealTokenProvider{
		tokenCache:      tokenCache,
		azureTokenCache: azureTokenCache,
	}
}

// GetK8sToken retrieves a Kubernetes service account token
func (p *RealTokenProvider) GetK8sToken(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error) {
	return p.tokenCache.GetToken(ctx, clientset, namespace, serviceAccount)
}

// GetAzureToken exchanges a Kubernetes token for an Azure AD token
func (p *RealTokenProvider) GetAzureToken(ctx context.Context, namespace, serviceAccount, k8sToken, clientID, tenantID string) (string, time.Time, error) {
	return p.azureTokenCache.GetToken(ctx, namespace, serviceAccount, k8sToken, clientID, tenantID)
}

// RealVaultClient implements VaultClient using the real Azure SDK
type RealVaultClient struct{}

// NewRealVaultClient creates a new RealVaultClient
func NewRealVaultClient() *RealVaultClient {
	return &RealVaultClient{}
}

// ListSecrets lists all secrets in the specified Azure Key Vault
func (c *RealVaultClient) ListSecrets(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
	return azure.ListSecrets(ctx, vaultName, token, expiration)
}

// ListCertificates lists all certificates in the specified Azure Key Vault
func (c *RealVaultClient) ListCertificates(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
	return azure.ListCertificates(ctx, vaultName, token, expiration)
}

// RealPatchClient implements PatchClient using the real update package
type RealPatchClient struct {
	client spcclient.Interface
}

// NewRealPatchClient creates a new RealPatchClient
func NewRealPatchClient(client spcclient.Interface) *RealPatchClient {
	return &RealPatchClient{
		client: client,
	}
}

// PatchSecretProviderClass updates a SecretProviderClass with new objects, secretObjects, and annotations
func (c *RealPatchClient) PatchSecretProviderClass(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, annotations map[string]string, timestamp string) error {
	return update.PatchSecretProviderClass(ctx, c.client, namespace, name, objectsYAML, secretObjects, annotations, timestamp)
}
