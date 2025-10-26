package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

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

// GetToken returns a valid Azure AD token, acquiring a new one if necessary
func (ac *AzureTokenCache) GetToken(
	ctx context.Context,
	namespace string,
	serviceAccount string,
	k8sToken string,
	clientID string,
	tenantID string,
) (string, error) {
	key := fmt.Sprintf("%s/%s", namespace, serviceAccount)

	// Check if we have a valid cached token
	if ac.IsTokenValid(namespace, serviceAccount) {
		ac.mu.RLock()
		token := ac.tokens[key].Token
		ac.mu.RUnlock()
		log.Printf("Using cached Azure AD token for %s", key)
		return token, nil
	}

	// Need to acquire new token
	log.Printf("Acquiring Azure AD token for %s (clientID: %s, tenantID: %s)",
		key, clientID, tenantID)

	token, expiration, err := ac.exchangeToken(ctx, k8sToken, clientID, tenantID)
	if err != nil {
		return "", fmt.Errorf("failed to exchange token for %s: %w", key, err)
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

	log.Printf("Successfully cached Azure AD token for %s, expires at %s",
		key, expiration.Format(time.RFC3339))

	return token, nil
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
// STUB: This is a placeholder implementation for testing
func (ac *AzureTokenCache) exchangeToken(
	ctx context.Context,
	k8sToken string,
	clientID string,
	tenantID string,
) (string, time.Time, error) {
	log.Printf("STUB: Exchanging Kubernetes token for Azure AD token")
	log.Printf("STUB:   clientID: %s", clientID)
	log.Printf("STUB:   tenantID: %s", tenantID)
	log.Printf("STUB:   k8sToken: %s...%s", k8sToken[:5], k8sToken[len(k8sToken)-5:])
	log.Printf("STUB:   scope: %s", keyVaultScope)

	// Generate fake Azure AD token
	fakeToken := fmt.Sprintf("eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9.azure-ad-token-%d.signature-%d",
		time.Now().Unix(), time.Now().Unix())

	// Azure AD tokens typically have 1 hour lifetime
	expiration := time.Now().Add(1 * time.Hour)

	log.Printf("STUB: Would write K8s token to temporary file")
	log.Printf("STUB: Would set environment variables:")
	log.Printf("STUB:   AZURE_FEDERATED_TOKEN_FILE=/tmp/k8s-token-*.jwt")
	log.Printf("STUB:   AZURE_CLIENT_ID=%s", clientID)
	log.Printf("STUB:   AZURE_TENANT_ID=%s", tenantID)
	log.Printf("STUB: Would create WorkloadIdentityCredential")
	log.Printf("STUB: Would call GetToken with scope: %s", keyVaultScope)
	log.Printf("STUB: Would receive Azure AD token: %s...%s",
		fakeToken[:10], fakeToken[len(fakeToken)-10:])
	log.Printf("STUB: Token expires at: %s", expiration.Format(time.RFC3339))
	log.Printf("STUB: Would clean up temporary file")

	return fakeToken, expiration, nil
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

	log.Printf("Extracted tenantID: %s from %s/%s",
		tenantID, obj.GetNamespace(), obj.GetName())

	return tenantID, nil
}
