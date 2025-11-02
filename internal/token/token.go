package token

import (
	"log/slog"
	"context"
	"fmt"
	
	"sync"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// requestToken requests a token from Kubernetes for the specified service account
func (tc *TokenCache) requestToken(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, time.Time, error) {
	slog.Debug("Requesting token for serviceaccount", "namespace", namespace, "serviceAccount", serviceAccount)

	// Create TokenRequest
	expirationSeconds := int64(tokenExpirationSeconds)
	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences:         []string{tokenAudience},
			ExpirationSeconds: &expirationSeconds,
		},
	}

	// Call Kubernetes API
	result, err := clientset.CoreV1().
		ServiceAccounts(namespace).
		CreateToken(ctx, serviceAccount, tokenRequest, metav1.CreateOptions{})

	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to request token: %w", err)
	}

    slog.Info("Successfully obtained Kubernetes token",
        "namespace", namespace, "serviceAccount", serviceAccount,
        "expiresAt", result.Status.ExpirationTimestamp.Format(time.RFC3339))

    return result.Status.Token, result.Status.ExpirationTimestamp.Time, nil
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

	// Token already expired
	if now.After(cached.ExpirationTime) {
		return false
	}

	// Calculate remaining lifetime
	remainingLifetime := cached.ExpirationTime.Sub(now)

	// Calculate renewal threshold based on original token lifetime
	// For 1-hour tokens (3600s), threshold of 0.8 means renew when ≤ 720s (20%) remains
	renewalThresholdDuration := time.Duration(float64(tokenExpirationSeconds) * (1 - tokenRenewalThreshold)) * time.Second

	// Token is valid if remaining lifetime is more than the renewal threshold
	return remainingLifetime > renewalThresholdDuration
}

// GetToken retrieves a cached token or requests a new one
func (tc *TokenCache) GetToken(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error) {
	// Check if we have a valid cached token
	if tc.IsTokenValid(namespace, serviceAccount) {
		tc.mu.RLock()
		token := tc.tokens[tokenCacheKey(namespace, serviceAccount)].Token
		tc.mu.RUnlock()
		slog.Debug("Using cached Kubernetes token", "namespace", namespace, "serviceAccount", serviceAccount)
		return token, nil
	}

	// Request new token
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

	slog.Debug("Extracted clientID", "clientID", clientID, "namespace", obj.GetNamespace(), "name", obj.GetName())
	return clientID, nil
}
