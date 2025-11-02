package controller

import (
	"context"
	"time"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"k8s.io/client-go/kubernetes"
)

// MockTokenProvider is a mock implementation of TokenProvider for testing
type MockTokenProvider struct {
	GetK8sTokenFunc   func(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error)
	GetAzureTokenFunc func(ctx context.Context, namespace, serviceAccount, k8sToken, clientID, tenantID string) (string, time.Time, error)
}

// GetK8sToken calls the mock function
func (m *MockTokenProvider) GetK8sToken(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error) {
	if m.GetK8sTokenFunc != nil {
		return m.GetK8sTokenFunc(ctx, clientset, namespace, serviceAccount)
	}
	return "mock-k8s-token", nil
}

// GetAzureToken calls the mock function
func (m *MockTokenProvider) GetAzureToken(ctx context.Context, namespace, serviceAccount, k8sToken, clientID, tenantID string) (string, time.Time, error) {
	if m.GetAzureTokenFunc != nil {
		return m.GetAzureTokenFunc(ctx, namespace, serviceAccount, k8sToken, clientID, tenantID)
	}
	return "mock-azure-token", time.Now().Add(1 * time.Hour), nil
}

// MockVaultClient is a mock implementation of VaultClient for testing
type MockVaultClient struct {
	ListSecretsFunc      func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error)
	ListCertificatesFunc func(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error)
}

// ListSecrets calls the mock function
func (m *MockVaultClient) ListSecrets(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error) {
	if m.ListSecretsFunc != nil {
		return m.ListSecretsFunc(ctx, vaultName, token, expiration)
	}
	return []azure.VaultSecret{
		{Name: "mock-secret-1", Tags: map[string]*string{"sync": ptr("true")}},
		{Name: "mock-secret-2", Tags: map[string]*string{"sync": ptr("true")}},
	}, nil
}

// ListCertificates calls the mock function
func (m *MockVaultClient) ListCertificates(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error) {
	if m.ListCertificatesFunc != nil {
		return m.ListCertificatesFunc(ctx, vaultName, token, expiration)
	}
	return []azure.VaultCertificate{
		{Name: "mock-cert-1", Tags: map[string]*string{"sync": ptr("true")}},
	}, nil
}

// MockPatchClient is a mock implementation of PatchClient for testing
type MockPatchClient struct {
	PatchSecretProviderClassFunc func(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, annotations map[string]string, timestamp string) error
}

// PatchSecretProviderClass calls the mock function
func (m *MockPatchClient) PatchSecretProviderClass(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, annotations map[string]string, timestamp string) error {
	if m.PatchSecretProviderClassFunc != nil {
		return m.PatchSecretProviderClassFunc(ctx, namespace, name, objectsYAML, secretObjects, annotations, timestamp)
	}
	return nil
}
