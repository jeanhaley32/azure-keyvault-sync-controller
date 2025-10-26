package main

import (
	"context"
	"fmt"
	"log"
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

// ExtractKeyvaultName extracts the keyvaultName from a SecretProviderClass spec
func ExtractKeyvaultName(obj *unstructured.Unstructured) (string, error) {
	keyvaultName, found, err := unstructured.NestedString(obj.Object, "spec", "parameters", "keyvaultName")
	if err != nil {
		return "", fmt.Errorf("error accessing spec.parameters.keyvaultName: %w", err)
	}
	if !found || keyvaultName == "" {
		return "", fmt.Errorf("keyvaultName not found in spec.parameters")
	}

	log.Printf("Extracted keyvaultName: %s from %s/%s",
		keyvaultName, obj.GetNamespace(), obj.GetName())

	return keyvaultName, nil
}

// ListSecrets lists all secrets in the specified Azure Key Vault
func ListSecrets(
	ctx context.Context,
	vaultName string,
	token string,
	expiration time.Time,
) ([]string, error) {
	vaultURL := fmt.Sprintf("https://%s.vault.azure.net", vaultName)

	log.Printf("Listing secrets from vault: %s", vaultURL)

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
	var secrets []string
	pager := client.NewListSecretPropertiesPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get next page of secrets: %w", err)
		}

		for _, secret := range page.Value {
			// Only include enabled secrets
			if secret.Attributes != nil && secret.Attributes.Enabled != nil && *secret.Attributes.Enabled {
				if secret.ID != nil {
					secretName := secret.ID.Name()
					secrets = append(secrets, secretName)
					log.Printf("  Found secret: %s (enabled)", secretName)
				}
			} else {
				if secret.ID != nil {
					log.Printf("  Skipping disabled secret: %s", secret.ID.Name())
				}
			}
		}
	}

	log.Printf("Successfully listed %d enabled secrets from vault %s", len(secrets), vaultName)
	return secrets, nil
}

// ListCertificates lists all certificates in the specified Azure Key Vault
func ListCertificates(
	ctx context.Context,
	vaultName string,
	token string,
	expiration time.Time,
) ([]string, error) {
	vaultURL := fmt.Sprintf("https://%s.vault.azure.net", vaultName)

	log.Printf("Listing certificates from vault: %s", vaultURL)

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
	var certificates []string
	pager := client.NewListCertificatePropertiesPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get next page of certificates: %w", err)
		}

		for _, cert := range page.Value {
			// Only include enabled certificates
			if cert.Attributes != nil && cert.Attributes.Enabled != nil && *cert.Attributes.Enabled {
				if cert.ID != nil {
					certName := cert.ID.Name()
					certificates = append(certificates, certName)
					log.Printf("  Found certificate: %s (enabled)", certName)
				}
			} else {
				if cert.ID != nil {
					log.Printf("  Skipping disabled certificate: %s", cert.ID.Name())
				}
			}
		}
	}

	log.Printf("Successfully listed %d enabled certificates from vault %s", len(certificates), vaultName)
	return certificates, nil
}
