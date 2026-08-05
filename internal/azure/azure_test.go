package azure

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestNewAzureTokenCache tests the Azure token cache constructor
func TestNewAzureTokenCache(t *testing.T) {
	ac := NewAzureTokenCache()

	assert.NotNil(t, ac)
	assert.NotNil(t, ac.tokens)
	assert.Equal(t, 0, len(ac.tokens))
}

// TestIsTokenValid tests Azure token validity checking
func TestIsTokenValid(t *testing.T) {
	tests := []struct {
		name           string
		setupCache     func(*AzureTokenCache)
		namespace      string
		serviceAccount string
		expectValid    bool
	}{
		{
			name: "token not in cache",
			setupCache: func(ac *AzureTokenCache) {
				// Empty cache
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    false,
		},
		{
			name: "token valid - freshly cached",
			setupCache: func(ac *AzureTokenCache) {
				key := "default/test-sa"
				ac.tokens[key] = &CachedAzureToken{
					Token:          "valid-azure-token",
					ExpirationTime: time.Now().Add(1 * time.Hour),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "test-sa",
					ClientID:       "test-client-id",
					TenantID:       "test-tenant-id",
				}
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    true,
		},
		{
			name: "token expired - past expiration time",
			setupCache: func(ac *AzureTokenCache) {
				key := "default/test-sa"
				ac.tokens[key] = &CachedAzureToken{
					Token:          "expired-token",
					ExpirationTime: time.Now().Add(-1 * time.Hour),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "test-sa",
					ClientID:       "test-client-id",
					TenantID:       "test-tenant-id",
				}
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    false,
		},
		{
			name: "token near expiration - still valid with dynamic calc",
			setupCache: func(ac *AzureTokenCache) {
				key := "default/test-sa"
				// Token expiring in 30 seconds - Azure uses dynamic renewal calc
				// renewalTime = now + 30s - (0.2 * 30s) = now + 24s
				// So now < now + 24s = true (still valid)
				ac.tokens[key] = &CachedAzureToken{
					Token:          "near-expiry-token",
					ExpirationTime: time.Now().Add(30 * time.Second),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "test-sa",
					ClientID:       "test-client-id",
					TenantID:       "test-tenant-id",
				}
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    true, // Still valid due to dynamic calculation
		},
		{
			name: "token valid - well before renewal threshold",
			setupCache: func(ac *AzureTokenCache) {
				key := "default/test-sa"
				ac.tokens[key] = &CachedAzureToken{
					Token:          "fresh-token",
					ExpirationTime: time.Now().Add(50 * time.Minute),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "test-sa",
					ClientID:       "test-client-id",
					TenantID:       "test-tenant-id",
				}
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    true,
		},
		{
			name: "different namespace - token not found",
			setupCache: func(ac *AzureTokenCache) {
				key := "default/test-sa"
				ac.tokens[key] = &CachedAzureToken{
					Token:          "token",
					ExpirationTime: time.Now().Add(1 * time.Hour),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "test-sa",
					ClientID:       "test-client-id",
					TenantID:       "test-tenant-id",
				}
			},
			namespace:      "kube-system",
			serviceAccount: "test-sa",
			expectValid:    false,
		},
		{
			name: "different service account - token not found",
			setupCache: func(ac *AzureTokenCache) {
				key := "default/test-sa"
				ac.tokens[key] = &CachedAzureToken{
					Token:          "token",
					ExpirationTime: time.Now().Add(1 * time.Hour),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "test-sa",
					ClientID:       "test-client-id",
					TenantID:       "test-tenant-id",
				}
			},
			namespace:      "default",
			serviceAccount: "other-sa",
			expectValid:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := NewAzureTokenCache()
			tt.setupCache(ac)

			valid := ac.IsTokenValid(tt.namespace, tt.serviceAccount)
			assert.Equal(t, tt.expectValid, valid)
		})
	}
}

// TestGetTokenCached tests retrieving cached Azure tokens
func TestGetTokenCached(t *testing.T) {
	ac := NewAzureTokenCache()

	// Pre-populate cache with a valid token
	key := "default/test-sa"
	expectedToken := "cached-azure-token"
	expectedExpiration := time.Now().Add(50 * time.Minute)

	ac.tokens[key] = &CachedAzureToken{
		Token:          expectedToken,
		ExpirationTime: expectedExpiration,
		IssuedAt:       time.Now(),
		Namespace:      "default",
		ServiceAccount: "test-sa",
		ClientID:       "test-client-id",
		TenantID:       "test-tenant-id",
	}

	ctx := context.Background()

	// Call GetToken - should return cached token without exchanging
	token, expiration, err := ac.GetToken(ctx, "default", "test-sa", "k8s-token", "test-client-id", "test-tenant-id")

	assert.NoError(t, err)
	assert.Equal(t, expectedToken, token)
	// Expiration should be close to expected (within 1 second due to timing)
	assert.WithinDuration(t, expectedExpiration, expiration, time.Second)
}

// TestGetTokenUncached tests that GetToken attempts token exchange when cache is empty
func TestGetTokenUncached(t *testing.T) {
	// Note: This test verifies that GetToken calls exchangeToken when cache is invalid.
	// We cannot fully test exchangeToken without mocking Azure SDK, but we can verify
	// the cache miss behavior and that an error is returned (since we're not in Azure).

	ac := NewAzureTokenCache()
	ctx := context.Background()

	// Call GetToken with empty cache - should attempt exchange and fail
	// (because we're not actually in Azure environment)
	token, expiration, err := ac.GetToken(ctx, "default", "test-sa", "k8s-token", "test-client-id", "test-tenant-id")

	// Expect error because we can't actually exchange tokens in test environment
	assert.Error(t, err)
	assert.Empty(t, token)
	assert.True(t, expiration.IsZero())
	assert.Contains(t, err.Error(), "failed to exchange token")
}

// TestAzureTokenCacheThreadSafety tests thread safety of Azure token cache operations
func TestAzureTokenCacheThreadSafety(t *testing.T) {
	ac := NewAzureTokenCache()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := "default/test-sa"
				ac.mu.Lock()
				ac.tokens[key] = &CachedAzureToken{
					Token:          "token",
					ExpirationTime: time.Now().Add(1 * time.Hour),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "test-sa",
					ClientID:       "client-id",
					TenantID:       "tenant-id",
				}
				ac.mu.Unlock()
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = ac.IsTokenValid("default", "test-sa")
			}
		}()
	}

	wg.Wait()

	// If we get here without race detector errors, test passes
	assert.NotNil(t, ac)
}

// TestExtractTenantID tests tenantID extraction from SecretProviderClass
func TestExtractTenantID(t *testing.T) {
	tests := []struct {
		name          string
		obj           *unstructured.Unstructured
		expectError   bool
		expectedValue string
		errorContains string
	}{
		{
			name: "valid tenantID",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"tenantId": "11111111-2222-3333-4444-555555555555",
						},
					},
				},
			},
			expectError:   false,
			expectedValue: "11111111-2222-3333-4444-555555555555",
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
							"tenantId": "attacker.example.com/x",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "invalid tenantId",
		},
		{
			name: "missing tenantID field",
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
			errorContains: "tenantId not found",
		},
		{
			name: "empty tenantID",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"tenantId": "",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "tenantId not found",
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
			errorContains: "tenantId not found",
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
			errorContains: "tenantId not found",
		},
		{
			name: "tenantID with UUID format",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"tenantId": "12345678-1234-1234-1234-123456789012",
						},
					},
				},
			},
			expectError:   false,
			expectedValue: "12345678-1234-1234-1234-123456789012",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenantID, err := ExtractTenantID(tt.obj)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.Empty(t, tenantID)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedValue, tenantID)
			}
		})
	}
}

// TestAzureTokenConstants tests Azure token cache constants
func TestAzureTokenConstants(t *testing.T) {
	assert.Equal(t, 0.8, azureTokenRenewalThreshold)
	assert.Equal(t, "https://vault.azure.net/.default", keyVaultScope)
}

// TestCachedAzureTokenStructure tests the CachedAzureToken structure
func TestCachedAzureTokenStructure(t *testing.T) {
	token := &CachedAzureToken{
		Token:          "test-token",
		ExpirationTime: time.Now().Add(1 * time.Hour),
					IssuedAt:       time.Now(),
		Namespace:      "default",
		ServiceAccount: "test-sa",
		ClientID:       "client-123",
		TenantID:       "tenant-456",
	}

	assert.Equal(t, "test-token", token.Token)
	assert.Equal(t, "default", token.Namespace)
	assert.Equal(t, "test-sa", token.ServiceAccount)
	assert.Equal(t, "client-123", token.ClientID)
	assert.Equal(t, "tenant-456", token.TenantID)
	assert.True(t, token.ExpirationTime.After(time.Now()))
}

// TestAzureTokenCacheKeyFormat tests the key format used for caching
func TestAzureTokenCacheKeyFormat(t *testing.T) {
	tests := []struct {
		name           string
		namespace      string
		serviceAccount string
		expectedKey    string
	}{
		{
			name:           "standard key",
			namespace:      "default",
			serviceAccount: "test-sa",
			expectedKey:    "default/test-sa",
		},
		{
			name:           "different namespace",
			namespace:      "kube-system",
			serviceAccount: "vault-sa",
			expectedKey:    "kube-system/vault-sa",
		},
		{
			name:           "empty namespace",
			namespace:      "",
			serviceAccount: "sa",
			expectedKey:    "/sa",
		},
		{
			name:           "empty service account",
			namespace:      "ns",
			serviceAccount: "",
			expectedKey:    "ns/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := NewAzureTokenCache()
			key := tt.namespace + "/" + tt.serviceAccount

			ac.mu.Lock()
			ac.tokens[key] = &CachedAzureToken{
				Token:          "test",
				ExpirationTime: time.Now().Add(1 * time.Hour),
					IssuedAt:       time.Now(),
				Namespace:      tt.namespace,
				ServiceAccount: tt.serviceAccount,
			}
			ac.mu.Unlock()

			ac.mu.RLock()
			_, exists := ac.tokens[tt.expectedKey]
			ac.mu.RUnlock()

			assert.True(t, exists, "token should exist at key %s", tt.expectedKey)
		})
	}
}

// TestGetTokenMultipleServiceAccounts tests caching multiple service accounts
func TestGetTokenMultipleServiceAccounts(t *testing.T) {
	ac := NewAzureTokenCache()

	// Add multiple cached tokens
	serviceAccounts := []struct {
		namespace string
		name      string
		token     string
	}{
		{"default", "sa1", "token1"},
		{"default", "sa2", "token2"},
		{"kube-system", "sa1", "token3"},
		{"kube-system", "sa2", "token4"},
	}

	for _, sa := range serviceAccounts {
		key := sa.namespace + "/" + sa.name
		ac.tokens[key] = &CachedAzureToken{
			Token:          sa.token,
			ExpirationTime: time.Now().Add(1 * time.Hour),
					IssuedAt:       time.Now(),
			Namespace:      sa.namespace,
			ServiceAccount: sa.name,
			ClientID:       "client-id",
			TenantID:       "tenant-id",
		}
	}

	// Verify all tokens are cached and valid
	for _, sa := range serviceAccounts {
		assert.True(t, ac.IsTokenValid(sa.namespace, sa.name))
	}

	// Verify cache contains exactly 4 entries
	ac.mu.RLock()
	assert.Equal(t, 4, len(ac.tokens))
	ac.mu.RUnlock()
}

// TestIsTokenValidRenewalCalculation tests the renewal threshold calculation
func TestIsTokenValidRenewalCalculation(t *testing.T) {
	ac := NewAzureTokenCache()

	// The Azure renewal calculation is: renewalTime = expiration - (0.2 * timeUntil(expiration))
	// So with 5 minutes remaining: renewalTime = now + 5min - (0.2 * 5min) = now + 4min
	// Token is valid if now < renewalTime, so it's valid until very close to expiration
	tests := []struct {
		name           string
		timeRemaining  time.Duration
		expectValid    bool
		description    string
	}{
		{
			name:          "well before expiry - 50 minutes",
			timeRemaining: 50 * time.Minute,
			expectValid:   true,
			description:   "50 minutes remaining, definitely valid",
		},
		{
			name:          "moderate time - 15 minutes",
			timeRemaining: 15 * time.Minute,
			expectValid:   true,
			description:   "15 minutes remaining, still valid",
		},
		{
			name:          "low time - 5 minutes",
			timeRemaining: 5 * time.Minute,
			expectValid:   true,
			description:   "5 minutes remaining, still valid (renewalTime = now + 4min)",
		},
		{
			name:          "very low time - 1 minute",
			timeRemaining: 1 * time.Minute,
			expectValid:   true,
			description:   "1 minute remaining, still valid (renewalTime = now + 48sec)",
		},
		{
			name:          "nearly expired - 10 seconds",
			timeRemaining: 10 * time.Second,
			expectValid:   true,
			description:   "10 seconds remaining, still valid (renewalTime = now + 8sec)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "default/test-sa"
			ac.tokens[key] = &CachedAzureToken{
				Token:          "test-token",
				ExpirationTime: time.Now().Add(tt.timeRemaining),
					IssuedAt:       time.Now(),
				Namespace:      "default",
				ServiceAccount: "test-sa",
				ClientID:       "client-id",
				TenantID:       "tenant-id",
			}

			valid := ac.IsTokenValid("default", "test-sa")
			assert.Equal(t, tt.expectValid, valid, tt.description)

			// Clean up for next test
			delete(ac.tokens, key)
		})
	}
}

// TestCleanupExpired tests the cleanup of expired Azure tokens
func TestCleanupExpired(t *testing.T) {
	tests := []struct {
		name          string
		setupCache    func(*AzureTokenCache)
		expectedCount int
	}{
		{
			name: "cleanup expired tokens",
			setupCache: func(ac *AzureTokenCache) {
				// Add expired token
				ac.tokens["default/expired-sa"] = &CachedAzureToken{
					Token:          "expired-token",
					ExpirationTime: time.Now().Add(-1 * time.Hour),
					IssuedAt:       time.Now().Add(-2 * time.Hour),
					Namespace:      "default",
					ServiceAccount: "expired-sa",
					ClientID:       "client-id",
					TenantID:       "tenant-id",
				}
				// Add valid token
				ac.tokens["default/valid-sa"] = &CachedAzureToken{
					Token:          "valid-token",
					ExpirationTime: time.Now().Add(1 * time.Hour),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "valid-sa",
					ClientID:       "client-id",
					TenantID:       "tenant-id",
				}
			},
			expectedCount: 1, // Only valid token remains
		},
		{
			name: "no expired tokens",
			setupCache: func(ac *AzureTokenCache) {
				ac.tokens["default/sa1"] = &CachedAzureToken{
					Token:          "token1",
					ExpirationTime: time.Now().Add(1 * time.Hour),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "sa1",
					ClientID:       "client-id",
					TenantID:       "tenant-id",
				}
				ac.tokens["default/sa2"] = &CachedAzureToken{
					Token:          "token2",
					ExpirationTime: time.Now().Add(2 * time.Hour),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: "sa2",
					ClientID:       "client-id",
					TenantID:       "tenant-id",
				}
			},
			expectedCount: 2, // Both tokens remain
		},
		{
			name: "all tokens expired",
			setupCache: func(ac *AzureTokenCache) {
				ac.tokens["default/sa1"] = &CachedAzureToken{
					Token:          "token1",
					ExpirationTime: time.Now().Add(-1 * time.Hour),
					IssuedAt:       time.Now().Add(-2 * time.Hour),
					Namespace:      "default",
					ServiceAccount: "sa1",
					ClientID:       "client-id",
					TenantID:       "tenant-id",
				}
				ac.tokens["default/sa2"] = &CachedAzureToken{
					Token:          "token2",
					ExpirationTime: time.Now().Add(-3 * time.Hour),
					IssuedAt:       time.Now().Add(-4 * time.Hour),
					Namespace:      "default",
					ServiceAccount: "sa2",
					ClientID:       "client-id",
					TenantID:       "tenant-id",
				}
			},
			expectedCount: 0, // All tokens removed
		},
		{
			name: "empty cache",
			setupCache: func(ac *AzureTokenCache) {
				// No tokens
			},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := NewAzureTokenCache()
			tt.setupCache(ac)

			ac.cleanupExpired()

			assert.Equal(t, tt.expectedCount, len(ac.tokens))
		})
	}
}

// TestStartCleanup tests the cleanup goroutine for Azure tokens
func TestStartCleanup(t *testing.T) {
	ac := NewAzureTokenCache()

	// Add some tokens
	ac.tokens["default/sa1"] = &CachedAzureToken{
		Token:          "token1",
		ExpirationTime: time.Now().Add(100 * time.Millisecond),
		IssuedAt:       time.Now(),
		Namespace:      "default",
		ServiceAccount: "sa1",
		ClientID:       "client-id",
		TenantID:       "tenant-id",
	}
	ac.tokens["default/sa2"] = &CachedAzureToken{
		Token:          "token2",
		ExpirationTime: time.Now().Add(1 * time.Hour),
		IssuedAt:       time.Now(),
		Namespace:      "default",
		ServiceAccount: "sa2",
		ClientID:       "client-id",
		TenantID:       "tenant-id",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start cleanup with short interval
	go ac.StartCleanup(ctx, 200*time.Millisecond)

	// Initially should have 2 tokens
	ac.mu.RLock()
	initialCount := len(ac.tokens)
	ac.mu.RUnlock()
	assert.Equal(t, 2, initialCount)

	// Wait for first token to expire and cleanup to run
	time.Sleep(400 * time.Millisecond)

	// Should have only 1 token now (expired one removed)
	ac.mu.RLock()
	count := len(ac.tokens)
	ac.mu.RUnlock()
	assert.Equal(t, 1, count)

	// Cancel context and verify cleanup stops
	cancel()
	time.Sleep(100 * time.Millisecond)
}

// TestStartCleanupContextCancellation tests cleanup goroutine stops on context cancellation
func TestStartCleanupContextCancellation(t *testing.T) {
	ac := NewAzureTokenCache()

	ctx, cancel := context.WithCancel(context.Background())

	// Start cleanup
	done := make(chan struct{})
	go func() {
		ac.StartCleanup(ctx, 100*time.Millisecond)
		close(done)
	}()

	// Cancel context immediately
	cancel()

	// Wait for cleanup to stop (with timeout)
	select {
	case <-done:
		// Success - cleanup stopped
	case <-time.After(1 * time.Second):
		t.Fatal("Cleanup goroutine did not stop after context cancellation")
	}
}

// TestCleanupThreadSafety tests thread safety of Azure token cleanup operations
func TestCleanupThreadSafety(t *testing.T) {
	ac := NewAzureTokenCache()
	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start cleanup goroutine
	go ac.StartCleanup(ctx, 50*time.Millisecond)

	// Concurrent token additions
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				key := fmt.Sprintf("default/sa-%d", id)
				ac.mu.Lock()
				ac.tokens[key] = &CachedAzureToken{
					Token:          "token",
					ExpirationTime: time.Now().Add(100 * time.Millisecond),
					IssuedAt:       time.Now(),
					Namespace:      "default",
					ServiceAccount: fmt.Sprintf("sa-%d", id),
					ClientID:       "client-id",
					TenantID:       "tenant-id",
				}
				ac.mu.Unlock()
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = ac.IsTokenValid("default", fmt.Sprintf("sa-%d", id))
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	// If we get here without race detector errors, test passes
	assert.NotNil(t, ac)
}
