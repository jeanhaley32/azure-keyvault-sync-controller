package controller

import (
	"testing"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/update"
	"github.com/stretchr/testify/assert"
)

// ptr is a helper function to create a pointer to a string
func ptr(s string) *string {
	return &s
}

// TestTagFilteringAndSecretObjectIntegration tests the complete flow:
// 1. Vault objects with tags
// 2. Tag filtering (service/environment)
// 3. Secret-object tag checking
// 4. Final secretObjects generation
func TestTagFilteringAndSecretObjectIntegration(t *testing.T) {
	tests := []struct {
		name                    string
		vaultSecrets            []azure.VaultSecret
		vaultCertificates       []azure.VaultCertificate
		respectTags             bool
		spcServiceLabel         string
		spcEnvironmentLabel     string
		expectedSecretsInArray  int // Count in objects array
		expectedCertsInArray    int // Count in objects array
		expectedSecretObjects   int // Count in secretObjects field
		expectedSecretNames     []string
		expectedCertNames       []string
		expectedSecretObjNames  []string
	}{
		{
			name: "no tag filtering - all secrets included, secret-object tags respected",
			vaultSecrets: []azure.VaultSecret{
				{Name: "secret1", Tags: map[string]*string{"secret-object": ptr("true")}},
				{Name: "secret2", Tags: map[string]*string{}}, // No secret-object tag
			},
			vaultCertificates: []azure.VaultCertificate{
				{Name: "cert1", Tags: map[string]*string{"cert-object": ptr("true")}},
			},
			respectTags:             false, // Tag filtering disabled
			spcServiceLabel:         "",
			spcEnvironmentLabel:     "",
			expectedSecretsInArray:  2, // Both secrets in objects array
			expectedCertsInArray:    1, // Cert in objects array
			expectedSecretObjects:   2, // secret1 + cert1 have object tags
			expectedSecretNames:     []string{"secret1", "secret2"},
			expectedCertNames:       []string{"cert1"},
			expectedSecretObjNames:  []string{"cert1", "secret1"}, // Sorted
		},
		{
			name: "tag filtering enabled - service match required",
			vaultSecrets: []azure.VaultSecret{
				{Name: "web-secret", Tags: map[string]*string{
					"service":       ptr("web-api"),
					"secret-object": ptr("true"),
				}},
				{Name: "mobile-secret", Tags: map[string]*string{
					"service":       ptr("mobile-api"),
					"secret-object": ptr("true"),
				}},
				{Name: "untagged-secret", Tags: map[string]*string{
					"secret-object": ptr("true"),
				}},
			},
			vaultCertificates:       []azure.VaultCertificate{},
			respectTags:             true,
			spcServiceLabel:         "web-api",
			spcEnvironmentLabel:     "",
			expectedSecretsInArray:  1, // Only web-secret matches
			expectedCertsInArray:    0,
			expectedSecretObjects:   1, // Only web-secret has both match + secret-object tag
			expectedSecretNames:     []string{"web-secret"},
			expectedCertNames:       []string{},
			expectedSecretObjNames:  []string{"web-secret"},
		},
		{
			name: "secret passes filtering but no secret-object tag",
			vaultSecrets: []azure.VaultSecret{
				{Name: "secret1", Tags: map[string]*string{
					"service": ptr("web-api"),
					// No secret-object tag
				}},
			},
			vaultCertificates:       []azure.VaultCertificate{},
			respectTags:             true,
			spcServiceLabel:         "web-api",
			spcEnvironmentLabel:     "",
			expectedSecretsInArray:  1, // In objects array
			expectedCertsInArray:    0,
			expectedSecretObjects:   0, // NOT in secretObjects (no secret-object tag)
			expectedSecretNames:     []string{"secret1"},
			expectedCertNames:       []string{},
			expectedSecretObjNames:  []string{},
		},
		{
			name: "secret has secret-object tag but rejected by tag filtering",
			vaultSecrets: []azure.VaultSecret{
				{Name: "wrong-service", Tags: map[string]*string{
					"service":       ptr("mobile-api"), // Mismatch
					"secret-object": ptr("true"),       // Has object tag but won't matter
				}},
			},
			vaultCertificates:       []azure.VaultCertificate{},
			respectTags:             true,
			spcServiceLabel:         "web-api",
			spcEnvironmentLabel:     "",
			expectedSecretsInArray:  0, // Rejected by tag filtering
			expectedCertsInArray:    0,
			expectedSecretObjects:   0, // Never evaluated for secret-object
			expectedSecretNames:     []string{},
			expectedCertNames:       []string{},
			expectedSecretObjNames:  []string{},
		},
		{
			name: "environment filtering with secret-object tags",
			vaultSecrets: []azure.VaultSecret{
				{Name: "prod-secret", Tags: map[string]*string{
					"service":       ptr("web-api"),
					"environment":   ptr("production"),
					"secret-object": ptr("true"),
				}},
				{Name: "staging-secret", Tags: map[string]*string{
					"service":       ptr("web-api"),
					"environment":   ptr("staging"),
					"secret-object": ptr("true"),
				}},
				{Name: "env-agnostic", Tags: map[string]*string{
					"service":       ptr("web-api"),
					"secret-object": ptr("true"),
				}},
			},
			vaultCertificates:       []azure.VaultCertificate{},
			respectTags:             true,
			spcServiceLabel:         "web-api",
			spcEnvironmentLabel:     "production",
			expectedSecretsInArray:  2, // prod-secret + env-agnostic
			expectedCertsInArray:    0,
			expectedSecretObjects:   2, // Both have secret-object tags
			expectedSecretNames:     []string{"env-agnostic", "prod-secret"},
			expectedCertNames:       []string{},
			expectedSecretObjNames:  []string{"env-agnostic", "prod-secret"},
		},
		{
			name: "all secrets filtered out - empty arrays",
			vaultSecrets: []azure.VaultSecret{
				{Name: "mobile-secret", Tags: map[string]*string{
					"service":       ptr("mobile-api"),
					"secret-object": ptr("true"),
				}},
			},
			vaultCertificates:       []azure.VaultCertificate{},
			respectTags:             true,
			spcServiceLabel:         "web-api",
			spcEnvironmentLabel:     "",
			expectedSecretsInArray:  0,
			expectedCertsInArray:    0,
			expectedSecretObjects:   0,
			expectedSecretNames:     []string{},
			expectedCertNames:       []string{},
			expectedSecretObjNames:  []string{},
		},
		{
			name: "mixed secrets and certificates with filtering and object tags",
			vaultSecrets: []azure.VaultSecret{
				{Name: "web-secret-1", Tags: map[string]*string{
					"service":       ptr("web-api"),
					"secret-object": ptr("true"),
				}},
				{Name: "web-secret-2", Tags: map[string]*string{
					"service": ptr("web-api"),
					// No secret-object tag
				}},
			},
			vaultCertificates: []azure.VaultCertificate{
				{Name: "web-cert", Tags: map[string]*string{
					"service":     ptr("web-api"),
					"cert-object": ptr("true"),
				}},
				{Name: "mobile-cert", Tags: map[string]*string{
					"service":     ptr("mobile-api"),
					"cert-object": ptr("true"),
				}},
			},
			respectTags:             true,
			spcServiceLabel:         "web-api",
			spcEnvironmentLabel:     "",
			expectedSecretsInArray:  2, // web-secret-1, web-secret-2
			expectedCertsInArray:    1, // web-cert (mobile-cert filtered out)
			expectedSecretObjects:   2, // web-secret-1 + web-cert
			expectedSecretNames:     []string{"web-secret-1", "web-secret-2"},
			expectedCertNames:       []string{"web-cert"},
			expectedSecretObjNames:  []string{"web-cert", "web-secret-1"},
		},
		{
			name: "certificates with cert-object tags",
			vaultSecrets: []azure.VaultSecret{},
			vaultCertificates: []azure.VaultCertificate{
				{Name: "tls-cert", Tags: map[string]*string{
					"service":     ptr("web-api"),
					"cert-object": ptr("true"),
				}},
				{Name: "ca-cert", Tags: map[string]*string{
					"service": ptr("web-api"),
					// No cert-object tag
				}},
			},
			respectTags:             true,
			spcServiceLabel:         "web-api",
			spcEnvironmentLabel:     "",
			expectedSecretsInArray:  0,
			expectedCertsInArray:    2, // Both certs match service
			expectedSecretObjects:   1, // Only tls-cert has cert-object tag
			expectedSecretNames:     []string{},
			expectedCertNames:       []string{"ca-cert", "tls-cert"},
			expectedSecretObjNames:  []string{"tls-cert"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Step 1: Apply tag filtering
			var filteredSecretNames []string
			var filteredCertNames []string

			filterConfig := azure.TagFilterConfig{
				ServiceLabel:     tt.spcServiceLabel,
				EnvironmentLabel: tt.spcEnvironmentLabel,
			}

			for _, vaultSecret := range tt.vaultSecrets {
				result := azure.MatchesTags(vaultSecret.Tags, filterConfig)
				if result.Include {
					filteredSecretNames = append(filteredSecretNames, vaultSecret.Name)
				}
			}

			for _, vaultCert := range tt.vaultCertificates {
				result := azure.MatchesTags(vaultCert.Tags, filterConfig)
				if result.Include {
					filteredCertNames = append(filteredCertNames, vaultCert.Name)
				}
			}

			// Verify objects array counts
			assert.Equal(t, tt.expectedSecretsInArray, len(filteredSecretNames),
				"unexpected number of secrets in objects array")
			assert.Equal(t, tt.expectedCertsInArray, len(filteredCertNames),
				"unexpected number of certs in objects array")

			// Verify objects array contents
			assert.ElementsMatch(t, tt.expectedSecretNames, filteredSecretNames,
				"secret names in objects array don't match")
			assert.ElementsMatch(t, tt.expectedCertNames, filteredCertNames,
				"cert names in objects array don't match")

			// Step 2: Build VaultSecretWithTags/VaultCertWithTags for secretObjects generation
			var secretsWithTags []update.VaultSecretWithTags
			var certsWithTags []update.VaultCertWithTags

			// Only include secrets that passed filtering
			for _, vaultSecret := range tt.vaultSecrets {
				for _, name := range filteredSecretNames {
					if name == vaultSecret.Name {
						secretsWithTags = append(secretsWithTags, update.VaultSecretWithTags{
							Name: vaultSecret.Name,
							Tags: vaultSecret.Tags,
						})
						break
					}
				}
			}

			// Only include certs that passed filtering
			for _, vaultCert := range tt.vaultCertificates {
				for _, name := range filteredCertNames {
					if name == vaultCert.Name {
						certsWithTags = append(certsWithTags, update.VaultCertWithTags{
							Name: vaultCert.Name,
							Tags: vaultCert.Tags,
						})
						break
					}
				}
			}

			// Step 3: Generate secretObjects
			secretObjects := update.GenerateSecretObjectsFromVault(secretsWithTags, certsWithTags)

			// Verify secretObjects count
			assert.Equal(t, tt.expectedSecretObjects, len(secretObjects),
				"unexpected number of secretObjects")

			// Verify secretObjects names
			var actualSecretObjNames []string
			for _, so := range secretObjects {
				actualSecretObjNames = append(actualSecretObjNames, so.SecretName)
			}
			assert.ElementsMatch(t, tt.expectedSecretObjNames, actualSecretObjNames,
				"secretObject names don't match")

			// Verify secretObjects types
			for _, so := range secretObjects {
				// Find if this came from a secret or cert
				isFromCert := false
				for _, cert := range tt.vaultCertificates {
					if cert.Name == so.SecretName {
						isFromCert = true
						break
					}
				}

				if isFromCert {
					assert.Equal(t, "kubernetes.io/tls", so.Type,
						"certificate should generate TLS type secret")
					assert.Len(t, so.Data, 2, "TLS secret should have 2 data entries")
				} else {
					assert.Equal(t, "Opaque", so.Type,
						"secret should generate Opaque type secret")
					assert.Len(t, so.Data, 1, "Opaque secret should have 1 data entry")
				}
			}
		})
	}
}

// TestTagFilteringEdgeCases tests edge cases in tag filtering logic
func TestTagFilteringEdgeCases(t *testing.T) {
	tests := []struct {
		name            string
		vaultTags       map[string]*string
		serviceLabel    string
		environmentLabel string
		shouldInclude   bool
		reason          string
	}{
		{
			name:            "nil tags with service label set",
			vaultTags:       nil,
			serviceLabel:    "web-api",
			environmentLabel: "",
			shouldInclude:   false,
			reason:          "no service tag should reject",
		},
		{
			name:            "empty string service tag",
			vaultTags:       map[string]*string{"service": ptr("")},
			serviceLabel:    "web-api",
			environmentLabel: "",
			shouldInclude:   false,
			reason:          "empty service tag treated as missing",
		},
		{
			name: "case insensitive matching",
			vaultTags: map[string]*string{
				"service":     ptr("WEB-API"),
				"environment": ptr("PRODUCTION"),
			},
			serviceLabel:     "web-api",
			environmentLabel: "production",
			shouldInclude:    true,
			reason:           "case insensitive matching works",
		},
		{
			name: "whitespace in tags",
			vaultTags: map[string]*string{
				"service": ptr("  web-api  "),
			},
			serviceLabel:     "web-api",
			environmentLabel: "",
			shouldInclude:    true,
			reason:           "whitespace is trimmed",
		},
		{
			name: "extra tags ignored",
			vaultTags: map[string]*string{
				"service":       ptr("web-api"),
				"team":          ptr("platform"),
				"cost-center":   ptr("engineering"),
				"secret-object": ptr("true"),
			},
			serviceLabel:     "web-api",
			environmentLabel: "",
			shouldInclude:    true,
			reason:           "extra tags don't affect filtering",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filterConfig := azure.TagFilterConfig{
				ServiceLabel:     tt.serviceLabel,
				EnvironmentLabel: tt.environmentLabel,
			}

			result := azure.MatchesTags(tt.vaultTags, filterConfig)
			assert.Equal(t, tt.shouldInclude, result.Include, tt.reason)
		})
	}
}

// TestSecretObjectTagEdgeCases tests edge cases in secret-object tag checking
func TestSecretObjectTagEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		tags          map[string]*string
		shouldGenerate bool
		reason        string
	}{
		{
			name:           "secret-object=true",
			tags:           map[string]*string{"secret-object": ptr("true")},
			shouldGenerate: true,
			reason:         "exact true value generates secret",
		},
		{
			name:           "secret-object=True (capitalized)",
			tags:           map[string]*string{"secret-object": ptr("True")},
			shouldGenerate: false,
			reason:         "case sensitive - only lowercase true works",
		},
		{
			name:           "secret-object=1",
			tags:           map[string]*string{"secret-object": ptr("1")},
			shouldGenerate: false,
			reason:         "only string true works, not 1",
		},
		{
			name:           "secret-object=yes",
			tags:           map[string]*string{"secret-object": ptr("yes")},
			shouldGenerate: false,
			reason:         "only true works, not yes",
		},
		{
			name:           "secret-object present but nil",
			tags:           map[string]*string{"secret-object": nil},
			shouldGenerate: false,
			reason:         "nil value doesn't generate secret",
		},
		{
			name:           "no secret-object tag at all",
			tags:           map[string]*string{"service": ptr("web-api")},
			shouldGenerate: false,
			reason:         "missing tag doesn't generate secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secrets := []update.VaultSecretWithTags{
				{Name: "test-secret", Tags: tt.tags},
			}
			certs := []update.VaultCertWithTags{}

			secretObjects := update.GenerateSecretObjectsFromVault(secrets, certs)

			if tt.shouldGenerate {
				assert.Len(t, secretObjects, 1, tt.reason)
			} else {
				assert.Len(t, secretObjects, 0, tt.reason)
			}
		})
	}
}
