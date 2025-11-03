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

// FilterSecretsByTags filters secrets based on tag key/value pairs (CRD mode).
// If filters is nil or empty, all secrets are returned.
// If filters is provided, only secrets matching ALL filter tags are returned.
func FilterSecretsByTags(secrets []VaultSecret, filters map[string]string) []VaultSecret {
	// No filters means include all secrets
	if len(filters) == 0 {
		slog.Debug("No filters provided, returning all secrets", "count", len(secrets))
		return secrets
	}

	var filtered []VaultSecret

	for _, secret := range secrets {
		if MatchesAllFilters(secret.Tags, filters) {
			filtered = append(filtered, secret)
		}
	}

	slog.Info("Filtered secrets by tags",
		"totalSecrets", len(secrets),
		"matchingSecrets", len(filtered),
		"filters", filters)

	return filtered
}

// FilterCertificatesByTags filters certificates based on tag key/value pairs (CRD mode).
// If filters is nil or empty, all certificates are returned.
// If filters is provided, only certificates matching ALL filter tags are returned.
func FilterCertificatesByTags(certs []VaultCertificate, filters map[string]string) []VaultCertificate {
	// No filters means include all certificates
	if len(filters) == 0 {
		slog.Debug("No filters provided, returning all certificates", "count", len(certs))
		return certs
	}

	var filtered []VaultCertificate

	for _, cert := range certs {
		if MatchesAllFilters(cert.Tags, filters) {
			filtered = append(filtered, cert)
		}
	}

	slog.Info("Filtered certificates by tags",
		"totalCertificates", len(certs),
		"matchingCertificates", len(filtered),
		"filters", filters)

	return filtered
}

// MatchesAllFilters checks if a tag map matches all required filters.
// Returns true only if ALL filter key/value pairs are present and match exactly.
// Returns false if tags is nil or if any filter doesn't match.
// This is used for CRD mode filtering with arbitrary tag key/value pairs.
func MatchesAllFilters(tags map[string]*string, filters map[string]string) bool {
	// Nil tags means no tags - won't match any filters
	if tags == nil {
		return false
	}

	// All filters must match
	for filterKey, filterValue := range filters {
		tagValue, exists := tags[filterKey]

		// Filter key doesn't exist in tags
		if !exists {
			return false
		}

		// Tag value is nil
		if tagValue == nil {
			return false
		}

		// Tag value doesn't match filter value (exact match required)
		if *tagValue != filterValue {
			return false
		}
	}

	// All filters matched
	return true
}
