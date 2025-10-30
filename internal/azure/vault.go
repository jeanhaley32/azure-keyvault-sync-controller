package azure

import (
	"log/slog"
	"context"
	"fmt"
	
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CachedTokenCredential implements azcore.TokenCredential using a cached Azure AD token
type CachedTokenCredential struct {
	token      string
	expiration time.Time
}

// GetToken returns the cached Azure AD token
func (c *CachedTokenCredential) GetToken(
	ctx context.Context,
	opts policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     c.token,
		ExpiresOn: c.expiration,
	}, nil
}

// VaultSecret represents a secret from Azure Key Vault with its tags
type VaultSecret struct {
	Name string
	Tags map[string]*string
}

// VaultCertificate represents a certificate from Azure Key Vault with its tags
type VaultCertificate struct {
	Name string
	Tags map[string]*string
}

// ExtractKeyvaultName extracts the keyvaultName from a SecretProviderClass spec
func ExtractKeyvaultName(obj *unstructured.Unstructured) (string, error) {
	keyvaultName, found, err := unstructured.NestedString(obj.Object, "spec", "parameters", "keyvaultName")
	if err != nil {
		return "", fmt.Errorf("error accessing spec.parameters.keyvaultName: %w", err)
	}
	if !found || keyvaultName == "" {
		return "", fmt.Errorf("keyvaultName not found in spec.parameters")
	}

	slog.Info("Extracted keyvaultName",
		    "vault", keyvaultName, "namespace", obj.GetNamespace(), "name", obj.GetName())

	return keyvaultName, nil
}

// ListSecrets lists all secrets in the specified Azure Key Vault with their tags
func ListSecrets(
	ctx context.Context,
	vaultName string,
	token string,
	expiration time.Time,
) ([]VaultSecret, error) {
	vaultURL := fmt.Sprintf("https://%s.vault.azure.net", vaultName)

	slog.Debug("Listing secrets from vault", "url", vaultURL)

	// Create credential wrapper
	cred := &CachedTokenCredential{
		token:      token,
		expiration: expiration,
	}

	// Create secrets client
	client, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create secrets client: %w", err)
	}

	// List secrets using pager
	var secrets []VaultSecret
	pager := client.NewListSecretPropertiesPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// Check for Azure throttling (429)
			if IsAzureThrottled(err) {
				retryAfter := ExtractRetryAfter(err)
				slog.Warn("Azure throttled secrets request",
					"vault", vaultName,
					"retryAfter", retryAfter)

				// Wait for the retry period
				time.Sleep(retryAfter)
				continue // Retry this page
			}

			return nil, fmt.Errorf("failed to get next page of secrets: %w", err)
		}

		for _, secret := range page.Value {
			// Only include enabled secrets
			if secret.Attributes != nil && secret.Attributes.Enabled != nil && *secret.Attributes.Enabled {
				if secret.ID != nil {
					secretName := secret.ID.Name()
					vaultSecret := VaultSecret{
						Name: secretName,
						Tags: secret.Tags,
					}
					secrets = append(secrets, vaultSecret)
					slog.Debug("Found enabled secret", "name", secretName, "tags", secret.Tags)
				}
			} else {
				if secret.ID != nil {
					slog.Debug("Skipping disabled secret", "name", secret.ID.Name())
				}
			}
		}
	}

	slog.Info("Listed secrets from vault", "count", len(secrets), "vault", vaultName)
	return secrets, nil
}

// ListCertificates lists all certificates in the specified Azure Key Vault with their tags
func ListCertificates(
	ctx context.Context,
	vaultName string,
	token string,
	expiration time.Time,
) ([]VaultCertificate, error) {
	vaultURL := fmt.Sprintf("https://%s.vault.azure.net", vaultName)

	slog.Debug("Listing certificates from vault", "url", vaultURL)

	// Create credential wrapper
	cred := &CachedTokenCredential{
		token:      token,
		expiration: expiration,
	}

	// Create certificates client
	client, err := azcertificates.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificates client: %w", err)
	}

	// List certificates using pager
	var certificates []VaultCertificate
	pager := client.NewListCertificatePropertiesPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// Check for Azure throttling (429)
			if IsAzureThrottled(err) {
				retryAfter := ExtractRetryAfter(err)
				slog.Warn("Azure throttled certificates request",
					"vault", vaultName,
					"retryAfter", retryAfter)

				// Wait for the retry period
				time.Sleep(retryAfter)
				continue // Retry this page
			}

			return nil, fmt.Errorf("failed to get next page of certificates: %w", err)
		}

		for _, cert := range page.Value {
			// Only include enabled certificates
			if cert.Attributes != nil && cert.Attributes.Enabled != nil && *cert.Attributes.Enabled {
				if cert.ID != nil {
					certName := cert.ID.Name()
					vaultCert := VaultCertificate{
						Name: certName,
						Tags: cert.Tags,
					}
					certificates = append(certificates, vaultCert)
					slog.Debug("Found enabled certificate", "name", certName, "tags", cert.Tags)
				}
			} else {
				if cert.ID != nil {
					slog.Debug("Skipping disabled certificate", "name", cert.ID.Name())
				}
			}
		}
	}

	slog.Info("Listed certificates from vault", "count", len(certificates), "vault", vaultName)
	return certificates, nil
}
