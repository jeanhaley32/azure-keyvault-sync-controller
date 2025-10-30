package azure

import (
	"log/slog"
	"context"
	"fmt"
	
	"os"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	azureTokenRenewalThreshold = 0.8 // Renew at 80% of lifetime
	keyVaultScope              = "https://vault.azure.net/.default"
)

// AzureTokenCache manages Azure AD access tokens with automatic renewal
type AzureTokenCache struct {
	mu     sync.RWMutex
	tokens map[string]*CachedAzureToken
}

// CachedAzureToken represents an Azure AD token with metadata
type CachedAzureToken struct {
	Token          string
	ExpirationTime time.Time
	Namespace      string
	ServiceAccount string
	ClientID       string
	TenantID       string
}

// NewAzureTokenCache creates a new Azure token cache
func NewAzureTokenCache() *AzureTokenCache {
	return &AzureTokenCache{
		tokens: make(map[string]*CachedAzureToken),
	}
}

// GetToken returns a valid Azure AD token and its expiration time, acquiring a new one if necessary
func (ac *AzureTokenCache) GetToken(
	ctx context.Context,
	namespace string,
	serviceAccount string,
	k8sToken string,
	clientID string,
	tenantID string,
) (string, time.Time, error) {
	key := fmt.Sprintf("%s/%s", namespace, serviceAccount)

	// Check if we have a valid cached token
	if ac.IsTokenValid(namespace, serviceAccount) {
		ac.mu.RLock()
		token := ac.tokens[key].Token
		expiration := ac.tokens[key].ExpirationTime
		ac.mu.RUnlock()
		slog.Debug("Using cached Azure AD token", "key", key)
		return token, expiration, nil
	}

	// Need to acquire new token
	slog.Info("Acquiring Azure AD token",
		    "key", key, "clientID", clientID, "tenantID", tenantID)

	token, expiration, err := ac.exchangeToken(ctx, k8sToken, clientID, tenantID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to exchange token for %s: %w", key, err)
	}

	// Cache the new token
	ac.mu.Lock()
	ac.tokens[key] = &CachedAzureToken{
		Token:          token,
		ExpirationTime: expiration,
		Namespace:      namespace,
		ServiceAccount: serviceAccount,
		ClientID:       clientID,
		TenantID:       tenantID,
	}
	ac.mu.Unlock()

	slog.Info("Successfully cached Azure AD token",
		"key", key, "expiresAt", expiration.Format(time.RFC3339))

	return token, expiration, nil
}

// IsTokenValid checks if a cached token exists and is still valid
func (ac *AzureTokenCache) IsTokenValid(namespace, serviceAccount string) bool {
	key := fmt.Sprintf("%s/%s", namespace, serviceAccount)

	ac.mu.RLock()
	cached, exists := ac.tokens[key]
	ac.mu.RUnlock()

	if !exists {
		return false
	}

	// Check if token needs renewal (at 80% of lifetime)
	now := time.Now()
	// Calculate when we should renew (20% before expiration)
	renewalTime := cached.ExpirationTime.Add(-time.Duration(float64(time.Until(cached.ExpirationTime)) * (1 - azureTokenRenewalThreshold)))

	return now.Before(renewalTime)
}

// exchangeToken exchanges a Kubernetes JWT for an Azure AD access token
func (ac *AzureTokenCache) exchangeToken(
	ctx context.Context,
	k8sToken string,
	clientID string,
	tenantID string,
) (string, time.Time, error) {
	slog.Debug("Exchanging Kubernetes token for Azure AD token")

	// Write K8s token to temporary file
	tmpFile, err := os.CreateTemp("", "k8s-token-*.jwt")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create temporary token file: %w", err)
	}
    tmpFilePath := tmpFile.Name()
    // Best-effort cleanup of the temporary file; ignore removal errors
    defer func() { _ = os.Remove(tmpFilePath) }()

	// Set restrictive permissions (owner read/write only)
    if err := tmpFile.Chmod(0600); err != nil {
        if cerr := tmpFile.Close(); cerr != nil {
            slog.Debug("error closing temp token file after chmod failure", "error", cerr)
        }
		return "", time.Time{}, fmt.Errorf("failed to set token file permissions: %w", err)
	}

	// Write K8s token to file
    if _, err := tmpFile.WriteString(k8sToken); err != nil {
        if cerr := tmpFile.Close(); cerr != nil {
            slog.Debug("error closing temp token file after write failure", "error", cerr)
        }
		return "", time.Time{}, fmt.Errorf("failed to write token to file: %w", err)
	}
    if cerr := tmpFile.Close(); cerr != nil {
        slog.Debug("error closing temp token file after successful write", "error", cerr)
    }

	slog.Debug("Created temporary token file", "path", tmpFilePath)

	// Set environment variables for WorkloadIdentityCredential
    if err := os.Setenv("AZURE_FEDERATED_TOKEN_FILE", tmpFilePath); err != nil {
        return "", time.Time{}, fmt.Errorf("failed to set AZURE_FEDERATED_TOKEN_FILE: %w", err)
    }
    if err := os.Setenv("AZURE_CLIENT_ID", clientID); err != nil {
        return "", time.Time{}, fmt.Errorf("failed to set AZURE_CLIENT_ID: %w", err)
    }
    if err := os.Setenv("AZURE_TENANT_ID", tenantID); err != nil {
        return "", time.Time{}, fmt.Errorf("failed to set AZURE_TENANT_ID: %w", err)
    }

	slog.Debug("Set environment variables for Azure authentication")

	// Create WorkloadIdentityCredential
	cred, err := azidentity.NewWorkloadIdentityCredential(nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create WorkloadIdentityCredential: %w", err)
	}

	slog.Debug("Created WorkloadIdentityCredential")

	// Request Azure AD token with Key Vault scope
	tokenResponse, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{keyVaultScope},
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to get Azure AD token: %w", err)
	}

	slog.Info("Successfully obtained Azure AD token",
		"expiresAt", tokenResponse.ExpiresOn.Format(time.RFC3339))

	return tokenResponse.Token, tokenResponse.ExpiresOn, nil
}

// ExtractTenantID extracts the tenantId from a SecretProviderClass spec
func ExtractTenantID(obj *unstructured.Unstructured) (string, error) {
	tenantID, found, err := unstructured.NestedString(obj.Object, "spec", "parameters", "tenantId")
	if err != nil {
		return "", fmt.Errorf("error accessing spec.parameters.tenantId: %w", err)
	}
	if !found || tenantID == "" {
		return "", fmt.Errorf("tenantId not found in spec.parameters")
	}

	slog.Debug("Extracted tenantID",
		    "tenantID", tenantID, "namespace", obj.GetNamespace(), "name", obj.GetName())

	return tenantID, nil
}
