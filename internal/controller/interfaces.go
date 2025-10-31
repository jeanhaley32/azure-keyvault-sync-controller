package controller

import (
	"context"
	"time"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"k8s.io/client-go/kubernetes"
)

// TokenProvider abstracts token acquisition for both Kubernetes and Azure
type TokenProvider interface {
	// GetK8sToken retrieves a Kubernetes service account token
	GetK8sToken(ctx context.Context, clientset kubernetes.Interface, namespace, serviceAccount string) (string, error)

	// GetAzureToken exchanges a Kubernetes token for an Azure AD token
	GetAzureToken(ctx context.Context, namespace, serviceAccount, k8sToken, clientID, tenantID string) (token string, expiration time.Time, err error)
}

// VaultClient abstracts Azure Key Vault operations
type VaultClient interface {
	// ListSecrets lists all secrets in the specified Azure Key Vault
	ListSecrets(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultSecret, error)

	// ListCertificates lists all certificates in the specified Azure Key Vault
	ListCertificates(ctx context.Context, vaultName, token string, expiration time.Time) ([]azure.VaultCertificate, error)
}

// PatchClient abstracts Kubernetes SecretProviderClass patching
type PatchClient interface {
	// PatchSecretProviderClass updates a SecretProviderClass with new objects and secretObjects
	PatchSecretProviderClass(ctx context.Context, namespace, name, objectsYAML string, secretObjects interface{}, timestamp string) error
}
