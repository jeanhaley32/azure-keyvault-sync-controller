package token

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// TestNewTokenCache tests the token cache constructor
func TestNewTokenCache(t *testing.T) {
	tc := NewTokenCache()

	assert.NotNil(t, tc)
	assert.NotNil(t, tc.tokens)
	assert.Equal(t, 0, len(tc.tokens))
}

// TestTokenCacheKey tests the cache key generation
func TestTokenCacheKey(t *testing.T) {
	tests := []struct {
		name           string
		namespace      string
		serviceAccount string
		expected       string
	}{
		{
			name:           "standard namespace and service account",
			namespace:      "default",
			serviceAccount: "my-sa",
			expected:       "default/my-sa",
		},
		{
			name:           "different namespace",
			namespace:      "kube-system",
			serviceAccount: "test-sa",
			expected:       "kube-system/test-sa",
		},
		{
			name:           "empty namespace",
			namespace:      "",
			serviceAccount: "sa",
			expected:       "/sa",
		},
		{
			name:           "empty service account",
			namespace:      "ns",
			serviceAccount: "",
			expected:       "ns/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := tokenCacheKey(tt.namespace, tt.serviceAccount)
			assert.Equal(t, tt.expected, key)
		})
	}
}

// TestIsTokenValid tests token validity checking
func TestIsTokenValid(t *testing.T) {
	tests := []struct {
		name           string
		setupCache     func(*TokenCache)
		namespace      string
		serviceAccount string
		expectValid    bool
	}{
		{
			name: "token not in cache",
			setupCache: func(tc *TokenCache) {
				// Empty cache
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    false,
		},
		{
			name: "token valid - just cached",
			setupCache: func(tc *TokenCache) {
				tc.tokens["default/test-sa"] = &CachedToken{
					Token:          "valid-token",
					ExpirationTime: time.Now().Add(1 * time.Hour),
					Namespace:      "default",
					ServiceAccount: "test-sa",
				}
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    true,
		},
		{
			name: "token expired - past expiration time",
			setupCache: func(tc *TokenCache) {
				tc.tokens["default/test-sa"] = &CachedToken{
					Token:          "expired-token",
					ExpirationTime: time.Now().Add(-1 * time.Hour),
					Namespace:      "default",
					ServiceAccount: "test-sa",
				}
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    false,
		},
		{
			name: "token at renewal threshold - 80% of lifetime",
			setupCache: func(tc *TokenCache) {
				// K8s tokens have 1-hour (3600s) lifetime
				// With 0.8 threshold, renewal threshold = 3600 * (1-0.8) = 720s
				// Token should be renewed when remaining ≤ 720s
				// Setting to 10 minutes (600s) which is < 720s, so should trigger renewal
				tc.tokens["default/test-sa"] = &CachedToken{
					Token:          "near-expiry-token",
					ExpirationTime: time.Now().Add(10 * time.Minute),
					Namespace:      "default",
					ServiceAccount: "test-sa",
				}
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    false, // Should trigger renewal
		},
		{
			name: "token valid - well before renewal threshold",
			setupCache: func(tc *TokenCache) {
				tc.tokens["default/test-sa"] = &CachedToken{
					Token:          "fresh-token",
					ExpirationTime: time.Now().Add(50 * time.Minute),
					Namespace:      "default",
					ServiceAccount: "test-sa",
				}
			},
			namespace:      "default",
			serviceAccount: "test-sa",
			expectValid:    true,
		},
		{
			name: "different namespace - token not found",
			setupCache: func(tc *TokenCache) {
				tc.tokens["default/test-sa"] = &CachedToken{
					Token:          "token",
					ExpirationTime: time.Now().Add(1 * time.Hour),
					Namespace:      "default",
					ServiceAccount: "test-sa",
				}
			},
			namespace:      "kube-system",
			serviceAccount: "test-sa",
			expectValid:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := NewTokenCache()
			tt.setupCache(tc)

			valid := tc.IsTokenValid(tt.namespace, tt.serviceAccount)
			assert.Equal(t, tt.expectValid, valid)
		})
	}
}

// TestRequestToken tests token request functionality
func TestRequestToken(t *testing.T) {
	tests := []struct {
		name           string
		namespace      string
		serviceAccount string
		setupClientset func(*fake.Clientset)
		expectError    bool
		errorContains  string
	}{
		{
			name:           "successful token request",
			namespace:      "default",
			serviceAccount: "test-sa",
			setupClientset: func(clientset *fake.Clientset) {
				clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					// Verify this is a token creation request
					createAction := action.(k8stesting.CreateAction)
					obj := createAction.GetObject()

					if tokenReq, ok := obj.(*authenticationv1.TokenRequest); ok {
						// Create a successful response
						expTime := metav1.NewTime(time.Now().Add(1 * time.Hour))
						tokenReq.Status = authenticationv1.TokenRequestStatus{
							Token:               "test-token-12345",
							ExpirationTimestamp: expTime,
						}
						return true, tokenReq, nil
					}
					return false, nil, nil
				})
			},
			expectError: false,
		},
		{
			name:           "service account not found",
			namespace:      "default",
			serviceAccount: "nonexistent-sa",
			setupClientset: func(clientset *fake.Clientset) {
				clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New("serviceaccounts \"nonexistent-sa\" not found")
				})
			},
			expectError:   true,
			errorContains: "failed to request token",
		},
		{
			name:           "different namespace",
			namespace:      "kube-system",
			serviceAccount: "test-sa",
			setupClientset: func(clientset *fake.Clientset) {
				clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					createAction := action.(k8stesting.CreateAction)

					// Verify namespace
					if createAction.GetNamespace() != "kube-system" {
						return true, nil, errors.New("wrong namespace")
					}

					obj := createAction.GetObject()
					if tokenReq, ok := obj.(*authenticationv1.TokenRequest); ok {
						expTime := metav1.NewTime(time.Now().Add(1 * time.Hour))
						tokenReq.Status = authenticationv1.TokenRequestStatus{
							Token:               "kube-system-token",
							ExpirationTimestamp: expTime,
						}
						return true, tokenReq, nil
					}
					return false, nil, nil
				})
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := NewTokenCache()
			clientset := fake.NewSimpleClientset()
			tt.setupClientset(clientset)

			ctx := context.Background()
			token, expiration, err := tc.requestToken(ctx, clientset, tt.namespace, tt.serviceAccount)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.Empty(t, token)
				assert.True(t, expiration.IsZero())
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, token)
				assert.False(t, expiration.IsZero())
				assert.True(t, expiration.After(time.Now()))
			}
		})
	}
}

// TestGetToken tests the main token retrieval function
func TestGetToken(t *testing.T) {
	tests := []struct {
		name           string
		namespace      string
		serviceAccount string
		setupCache     func(*TokenCache)
		setupClientset func(*fake.Clientset)
		expectError    bool
		expectCached   bool
		errorContains  string
	}{
		{
			name:           "get token from empty cache - success",
			namespace:      "default",
			serviceAccount: "test-sa",
			setupCache: func(tc *TokenCache) {
				// Empty cache
			},
			setupClientset: func(clientset *fake.Clientset) {
				clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					createAction := action.(k8stesting.CreateAction)
					obj := createAction.GetObject()
					if tokenReq, ok := obj.(*authenticationv1.TokenRequest); ok {
						expTime := metav1.NewTime(time.Now().Add(1 * time.Hour))
						tokenReq.Status = authenticationv1.TokenRequestStatus{
							Token:               "new-token",
							ExpirationTimestamp: expTime,
						}
						return true, tokenReq, nil
					}
					return false, nil, nil
				})
			},
			expectError:  false,
			expectCached: false,
		},
		{
			name:           "get token from valid cache - no API call",
			namespace:      "default",
			serviceAccount: "test-sa",
			setupCache: func(tc *TokenCache) {
				tc.tokens["default/test-sa"] = &CachedToken{
					Token:          "cached-token",
					ExpirationTime: time.Now().Add(50 * time.Minute),
					Namespace:      "default",
					ServiceAccount: "test-sa",
				}
			},
			setupClientset: func(clientset *fake.Clientset) {
				// Should not be called
			},
			expectError:  false,
			expectCached: true,
		},
		{
			name:           "get token with expired cache - refresh",
			namespace:      "default",
			serviceAccount: "test-sa",
			setupCache: func(tc *TokenCache) {
				tc.tokens["default/test-sa"] = &CachedToken{
					Token:          "old-token",
					ExpirationTime: time.Now().Add(5 * time.Minute), // Below renewal threshold
					Namespace:      "default",
					ServiceAccount: "test-sa",
				}
			},
			setupClientset: func(clientset *fake.Clientset) {
				clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					createAction := action.(k8stesting.CreateAction)
					obj := createAction.GetObject()
					if tokenReq, ok := obj.(*authenticationv1.TokenRequest); ok {
						expTime := metav1.NewTime(time.Now().Add(1 * time.Hour))
						tokenReq.Status = authenticationv1.TokenRequestStatus{
							Token:               "refreshed-token",
							ExpirationTimestamp: expTime,
						}
						return true, tokenReq, nil
					}
					return false, nil, nil
				})
			},
			expectError:  false,
			expectCached: false,
		},
		{
			name:           "get token with API error",
			namespace:      "default",
			serviceAccount: "bad-sa",
			setupCache: func(tc *TokenCache) {
				// Empty cache
			},
			setupClientset: func(clientset *fake.Clientset) {
				clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New("forbidden")
				})
			},
			expectError:   true,
			expectCached:  false,
			errorContains: "failed to request token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := NewTokenCache()
			tt.setupCache(tc)

			clientset := fake.NewSimpleClientset()
			tt.setupClientset(clientset)

			ctx := context.Background()
			token, err := tc.GetToken(ctx, clientset, tt.namespace, tt.serviceAccount)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.Empty(t, token)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, token)

				if tt.expectCached {
					assert.Equal(t, "cached-token", token)
				}

				// Verify token is now in cache
				assert.True(t, tc.IsTokenValid(tt.namespace, tt.serviceAccount))
			}
		})
	}
}

// TestGetTokenConcurrency tests concurrent token retrieval
func TestGetTokenConcurrency(t *testing.T) {
	tc := NewTokenCache()
	clientset := fake.NewSimpleClientset()

	// Setup reactor to return tokens
	clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		createAction := action.(k8stesting.CreateAction)
		obj := createAction.GetObject()
		if tokenReq, ok := obj.(*authenticationv1.TokenRequest); ok {
			expTime := metav1.NewTime(time.Now().Add(1 * time.Hour))
			tokenReq.Status = authenticationv1.TokenRequestStatus{
				Token:               "concurrent-token",
				ExpirationTimestamp: expTime,
			}
			return true, tokenReq, nil
		}
		return false, nil, nil
	})

	// Concurrently request tokens for the same service account
	var wg sync.WaitGroup
	numGoroutines := 10
	tokens := make([]string, numGoroutines)
	errors := make([]error, numGoroutines)

	ctx := context.Background()

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			token, err := tc.GetToken(ctx, clientset, "default", "concurrent-sa")
			tokens[index] = token
			errors[index] = err
		}(i)
	}

	wg.Wait()

	// Verify all goroutines succeeded
	for i := 0; i < numGoroutines; i++ {
		assert.NoError(t, errors[i])
		assert.NotEmpty(t, tokens[i])
	}

	// Verify token is cached
	assert.True(t, tc.IsTokenValid("default", "concurrent-sa"))
}

// TestGetTokenMultipleServiceAccounts tests caching multiple service accounts
func TestGetTokenMultipleServiceAccounts(t *testing.T) {
	tc := NewTokenCache()
	clientset := fake.NewSimpleClientset()

	// Setup reactor to return unique tokens for each service account
	clientset.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		createAction := action.(k8stesting.CreateAction)
		obj := createAction.GetObject()
		if tokenReq, ok := obj.(*authenticationv1.TokenRequest); ok {
			// Create unique token based on namespace and service account
			namespace := createAction.GetNamespace()
			tokenValue := "token-" + namespace
			expTime := metav1.NewTime(time.Now().Add(1 * time.Hour))
			tokenReq.Status = authenticationv1.TokenRequestStatus{
				Token:               tokenValue,
				ExpirationTimestamp: expTime,
			}
			return true, tokenReq, nil
		}
		return false, nil, nil
	})

	ctx := context.Background()

	// Request tokens for multiple service accounts
	serviceAccounts := []struct {
		namespace string
		name      string
	}{
		{"default", "sa1"},
		{"default", "sa2"},
		{"kube-system", "sa1"},
		{"kube-system", "sa2"},
	}

	for _, sa := range serviceAccounts {
		token, err := tc.GetToken(ctx, clientset, sa.namespace, sa.name)
		require.NoError(t, err)
		assert.NotEmpty(t, token)
	}

	// Verify all tokens are cached and valid
	for _, sa := range serviceAccounts {
		assert.True(t, tc.IsTokenValid(sa.namespace, sa.name))
	}

	// Verify cache contains exactly 4 entries
	tc.mu.RLock()
	assert.Equal(t, 4, len(tc.tokens))
	tc.mu.RUnlock()
}

// TestExtractClientID tests clientID extraction from SecretProviderClass
func TestExtractClientID(t *testing.T) {
	tests := []struct {
		name          string
		obj           *unstructured.Unstructured
		expectError   bool
		expectedValue string
		errorContains string
	}{
		{
			name: "valid clientID",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"clientID": "test-client-id-12345",
						},
					},
				},
			},
			expectError:   false,
			expectedValue: "test-client-id-12345",
		},
		{
			name: "missing clientID field",
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
			errorContains: "clientID not found",
		},
		{
			name: "empty clientID",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"clientID": "",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "clientID not found",
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
			errorContains: "clientID not found",
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
			errorContains: "clientID not found",
		},
		{
			name: "clientID with special characters",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-spc",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"parameters": map[string]interface{}{
							"clientID": "client-id-with-dashes_and_underscores",
						},
					},
				},
			},
			expectError:   false,
			expectedValue: "client-id-with-dashes_and_underscores",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientID, err := ExtractClientID(tt.obj)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.Empty(t, clientID)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedValue, clientID)
			}
		})
	}
}

// TestTokenCacheThreadSafety tests thread safety of token cache operations
func TestTokenCacheThreadSafety(t *testing.T) {
	tc := NewTokenCache()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := tokenCacheKey("default", "sa")
				tc.mu.Lock()
				tc.tokens[key] = &CachedToken{
					Token:          "token",
					ExpirationTime: time.Now().Add(1 * time.Hour),
					Namespace:      "default",
					ServiceAccount: "sa",
				}
				tc.mu.Unlock()
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = tc.IsTokenValid("default", "sa")
			}
		}()
	}

	wg.Wait()

	// If we get here without race detector errors, test passes
	assert.NotNil(t, tc)
}

// TestTokenConstants tests that token constants have expected values
func TestTokenConstants(t *testing.T) {
	assert.Equal(t, int64(3600), int64(tokenExpirationSeconds))
	assert.Equal(t, "api://AzureADTokenExchange", tokenAudience)
	assert.Equal(t, 0.8, tokenRenewalThreshold)
}
