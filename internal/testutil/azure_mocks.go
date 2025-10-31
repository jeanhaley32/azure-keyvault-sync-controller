package testutil

import (
	"context"
	"fmt"
	"time"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"k8s.io/client-go/kubernetes"
)

// MockAzureTokenCache is a mock implementation of the Azure token cache for testing
type MockAzureTokenCache struct {
	GetTokenFunc func(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount, clientID, tenantID string) (string, time.Time, error)
	tokens       map[string]tokenEntry
	callCount    int
}

type tokenEntry struct {
	token      string
	expiration time.Time
	err        error
}

// NewMockAzureTokenCache creates a new mock Azure token cache
func NewMockAzureTokenCache() *MockAzureTokenCache {
	return &MockAzureTokenCache{
		tokens: make(map[string]tokenEntry),
	}
}

// GetToken returns a mocked Azure token
func (m *MockAzureTokenCache) GetToken(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount, clientID, tenantID string) (string, time.Time, error) {
	m.callCount++

	// If custom function is provided, use it
	if m.GetTokenFunc != nil {
		return m.GetTokenFunc(ctx, clientset, namespace, serviceAccount, clientID, tenantID)
	}

	// Otherwise use configured token for this service account
	key := fmt.Sprintf("%s/%s", namespace, serviceAccount)
	if entry, ok := m.tokens[key]; ok {
		return entry.token, entry.expiration, entry.err
	}

	// Default: return a valid token
	return "mock-azure-token", time.Now().Add(1 * time.Hour), nil
}

// WithToken configures the mock to return a specific token for a service account
func (m *MockAzureTokenCache) WithToken(namespace, serviceAccount, token string, expiration time.Time) *MockAzureTokenCache {
	key := fmt.Sprintf("%s/%s", namespace, serviceAccount)
	m.tokens[key] = tokenEntry{
		token:      token,
		expiration: expiration,
		err:        nil,
	}
	return m
}

// WithError configures the mock to return an error for a service account
func (m *MockAzureTokenCache) WithError(namespace, serviceAccount string, err error) *MockAzureTokenCache {
	key := fmt.Sprintf("%s/%s", namespace, serviceAccount)
	m.tokens[key] = tokenEntry{
		err: err,
	}
	return m
}

// WithExpiredToken configures the mock to return an expired token
func (m *MockAzureTokenCache) WithExpiredToken(namespace, serviceAccount string) *MockAzureTokenCache {
	key := fmt.Sprintf("%s/%s", namespace, serviceAccount)
	m.tokens[key] = tokenEntry{
		token:      "expired-token",
		expiration: time.Now().Add(-1 * time.Hour),
		err:        nil,
	}
	return m
}

// CallCount returns the number of times GetToken was called
func (m *MockAzureTokenCache) CallCount() int {
	return m.callCount
}

// Reset resets the mock state
func (m *MockAzureTokenCache) Reset() {
	m.tokens = make(map[string]tokenEntry)
	m.callCount = 0
	m.GetTokenFunc = nil
}

// MockVaultClient is a mock implementation of an Azure Key Vault client for testing
type MockVaultClient struct {
	ListSecretsFunc      func(ctx context.Context, keyvaultName string) ([]azure.VaultSecret, error)
	ListCertificatesFunc func(ctx context.Context, keyvaultName string) ([]azure.VaultCertificate, error)
	secrets              map[string][]azure.VaultSecret
	certificates         map[string][]azure.VaultCertificate
	callCount            int
}

// NewMockVaultClient creates a new mock vault client
func NewMockVaultClient() *MockVaultClient {
	return &MockVaultClient{
		secrets:      make(map[string][]azure.VaultSecret),
		certificates: make(map[string][]azure.VaultCertificate),
	}
}

// ListSecrets returns mocked secrets from the vault
func (m *MockVaultClient) ListSecrets(ctx context.Context, keyvaultName string) ([]azure.VaultSecret, error) {
	m.callCount++

	// If custom function is provided, use it
	if m.ListSecretsFunc != nil {
		return m.ListSecretsFunc(ctx, keyvaultName)
	}

	// Otherwise return configured secrets
	if secrets, ok := m.secrets[keyvaultName]; ok {
		return secrets, nil
	}

	// Default: return empty list
	return []azure.VaultSecret{}, nil
}

// ListCertificates returns mocked certificates from the vault
func (m *MockVaultClient) ListCertificates(ctx context.Context, keyvaultName string) ([]azure.VaultCertificate, error) {
	m.callCount++

	// If custom function is provided, use it
	if m.ListCertificatesFunc != nil {
		return m.ListCertificatesFunc(ctx, keyvaultName)
	}

	// Otherwise return configured certificates
	if certs, ok := m.certificates[keyvaultName]; ok {
		return certs, nil
	}

	// Default: return empty list
	return []azure.VaultCertificate{}, nil
}

// WithSecrets configures the mock to return specific secrets for a vault
func (m *MockVaultClient) WithSecrets(keyvaultName string, secrets []azure.VaultSecret) *MockVaultClient {
	m.secrets[keyvaultName] = secrets
	return m
}

// WithCertificates configures the mock to return specific certificates for a vault
func (m *MockVaultClient) WithCertificates(keyvaultName string, certificates []azure.VaultCertificate) *MockVaultClient {
	m.certificates[keyvaultName] = certificates
	return m
}

// CallCount returns the number of times vault operations were called
func (m *MockVaultClient) CallCount() int {
	return m.callCount
}

// Reset resets the mock state
func (m *MockVaultClient) Reset() {
	m.secrets = make(map[string][]azure.VaultSecret)
	m.certificates = make(map[string][]azure.VaultCertificate)
	m.callCount = 0
	m.ListSecretsFunc = nil
	m.ListCertificatesFunc = nil
}
