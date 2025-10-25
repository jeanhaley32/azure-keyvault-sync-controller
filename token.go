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
	tokenExpirationSeconds = 3600                        // 1 hour
	tokenAudience          = "api://AzureADTokenExchange"
	tokenRenewalThreshold  = 0.8                         // Renew at 80% of lifetime
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

// tokenCacheKey generates a unique key for namespace/serviceAccount
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
