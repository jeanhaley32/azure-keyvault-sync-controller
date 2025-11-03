package azure

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMatchesAllFilters tests the MatchesAllFilters function
func TestMatchesAllFilters(t *testing.T) {
	tests := []struct {
		name        string
		tags        map[string]*string
		filters     map[string]string
		expectMatch bool
	}{
		{
			name: "no filters - always matches",
			tags: map[string]*string{
				"service": stringPtr("api"),
			},
			filters:     map[string]string{},
			expectMatch: true,
		},
		{
			name: "nil tags with filters - never matches",
			tags: nil,
			filters: map[string]string{
				"service": "api",
			},
			expectMatch: false,
		},
		{
			name:        "empty tags with filters - never matches",
			tags:        map[string]*string{},
			filters:     map[string]string{"service": "api"},
			expectMatch: false,
		},
		{
			name: "single filter matches",
			tags: map[string]*string{
				"service": stringPtr("api"),
			},
			filters: map[string]string{
				"service": "api",
			},
			expectMatch: true,
		},
		{
			name: "single filter doesn't match",
			tags: map[string]*string{
				"service": stringPtr("web"),
			},
			filters: map[string]string{
				"service": "api",
			},
			expectMatch: false,
		},
		{
			name: "multiple filters all match",
			tags: map[string]*string{
				"service":     stringPtr("api"),
				"environment": stringPtr("production"),
				"team":        stringPtr("backend"),
			},
			filters: map[string]string{
				"service":     "api",
				"environment": "production",
			},
			expectMatch: true,
		},
		{
			name: "multiple filters one doesn't match",
			tags: map[string]*string{
				"service":     stringPtr("api"),
				"environment": stringPtr("staging"),
			},
			filters: map[string]string{
				"service":     "api",
				"environment": "production",
			},
			expectMatch: false,
		},
		{
			name: "filter key not in tags",
			tags: map[string]*string{
				"service": stringPtr("api"),
			},
			filters: map[string]string{
				"service":     "api",
				"environment": "production",
			},
			expectMatch: false,
		},
		{
			name: "tag value is nil",
			tags: map[string]*string{
				"service":     stringPtr("api"),
				"environment": nil,
			},
			filters: map[string]string{
				"environment": "production",
			},
			expectMatch: false,
		},
		{
			name: "extra tags don't prevent match",
			tags: map[string]*string{
				"service":     stringPtr("api"),
				"environment": stringPtr("production"),
				"team":        stringPtr("backend"),
				"owner":       stringPtr("alice"),
			},
			filters: map[string]string{
				"service":     "api",
				"environment": "production",
			},
			expectMatch: true,
		},
		{
			name: "case sensitive matching",
			tags: map[string]*string{
				"service": stringPtr("API"),
			},
			filters: map[string]string{
				"service": "api",
			},
			expectMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchesAllFilters(tt.tags, tt.filters)
			assert.Equal(t, tt.expectMatch, result)
		})
	}
}

// TestFilterSecretsByTags tests filtering secrets by tags
func TestFilterSecretsByTags(t *testing.T) {
	tests := []struct {
		name           string
		secrets        []VaultSecret
		filters        map[string]string
		expectedCount  int
		expectedNames  []string
	}{
		{
			name: "no filters returns all secrets",
			secrets: []VaultSecret{
				{
					Name: "secret1",
					Tags: map[string]*string{"service": stringPtr("api")},
				},
				{
					Name: "secret2",
					Tags: map[string]*string{"service": stringPtr("web")},
				},
			},
			filters:       map[string]string{},
			expectedCount: 2,
			expectedNames: []string{"secret1", "secret2"},
		},
		{
			name: "nil filters returns all secrets",
			secrets: []VaultSecret{
				{
					Name: "secret1",
					Tags: map[string]*string{"service": stringPtr("api")},
				},
			},
			filters:       nil,
			expectedCount: 1,
			expectedNames: []string{"secret1"},
		},
		{
			name: "filter by single tag",
			secrets: []VaultSecret{
				{
					Name: "api-key",
					Tags: map[string]*string{"service": stringPtr("api")},
				},
				{
					Name: "web-key",
					Tags: map[string]*string{"service": stringPtr("web")},
				},
				{
					Name: "db-key",
					Tags: map[string]*string{"service": stringPtr("database")},
				},
			},
			filters: map[string]string{
				"service": "api",
			},
			expectedCount: 1,
			expectedNames: []string{"api-key"},
		},
		{
			name: "filter by multiple tags",
			secrets: []VaultSecret{
				{
					Name: "prod-api-key",
					Tags: map[string]*string{
						"service":     stringPtr("api"),
						"environment": stringPtr("production"),
					},
				},
				{
					Name: "staging-api-key",
					Tags: map[string]*string{
						"service":     stringPtr("api"),
						"environment": stringPtr("staging"),
					},
				},
				{
					Name: "prod-web-key",
					Tags: map[string]*string{
						"service":     stringPtr("web"),
						"environment": stringPtr("production"),
					},
				},
			},
			filters: map[string]string{
				"service":     "api",
				"environment": "production",
			},
			expectedCount: 1,
			expectedNames: []string{"prod-api-key"},
		},
		{
			name: "filter excludes secrets without tags",
			secrets: []VaultSecret{
				{
					Name: "tagged-secret",
					Tags: map[string]*string{"service": stringPtr("api")},
				},
				{
					Name: "untagged-secret",
					Tags: nil,
				},
			},
			filters: map[string]string{
				"service": "api",
			},
			expectedCount: 1,
			expectedNames: []string{"tagged-secret"},
		},
		{
			name: "filter returns empty when no matches",
			secrets: []VaultSecret{
				{
					Name: "secret1",
					Tags: map[string]*string{"service": stringPtr("web")},
				},
			},
			filters: map[string]string{
				"service": "api",
			},
			expectedCount: 0,
			expectedNames: []string{},
		},
		{
			name:          "empty secrets list",
			secrets:       []VaultSecret{},
			filters:       map[string]string{"service": "api"},
			expectedCount: 0,
			expectedNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := FilterSecretsByTags(tt.secrets, tt.filters)

			assert.Equal(t, tt.expectedCount, len(filtered))

			if tt.expectedCount > 0 {
				actualNames := make([]string, len(filtered))
				for i, secret := range filtered {
					actualNames[i] = secret.Name
				}
				assert.ElementsMatch(t, tt.expectedNames, actualNames)
			}
		})
	}
}

// TestFilterCertificatesByTags tests filtering certificates by tags
func TestFilterCertificatesByTags(t *testing.T) {
	tests := []struct {
		name          string
		certificates  []VaultCertificate
		filters       map[string]string
		expectedCount int
		expectedNames []string
	}{
		{
			name: "no filters returns all certificates",
			certificates: []VaultCertificate{
				{
					Name: "cert1",
					Tags: map[string]*string{"type": stringPtr("tls")},
				},
				{
					Name: "cert2",
					Tags: map[string]*string{"type": stringPtr("ca")},
				},
			},
			filters:       map[string]string{},
			expectedCount: 2,
			expectedNames: []string{"cert1", "cert2"},
		},
		{
			name: "filter by single tag",
			certificates: []VaultCertificate{
				{
					Name: "tls-cert",
					Tags: map[string]*string{"type": stringPtr("tls")},
				},
				{
					Name: "ca-cert",
					Tags: map[string]*string{"type": stringPtr("ca")},
				},
			},
			filters: map[string]string{
				"type": "tls",
			},
			expectedCount: 1,
			expectedNames: []string{"tls-cert"},
		},
		{
			name: "filter by multiple tags",
			certificates: []VaultCertificate{
				{
					Name: "prod-api-cert",
					Tags: map[string]*string{
						"service":     stringPtr("api"),
						"environment": stringPtr("production"),
					},
				},
				{
					Name: "staging-api-cert",
					Tags: map[string]*string{
						"service":     stringPtr("api"),
						"environment": stringPtr("staging"),
					},
				},
			},
			filters: map[string]string{
				"service":     "api",
				"environment": "production",
			},
			expectedCount: 1,
			expectedNames: []string{"prod-api-cert"},
		},
		{
			name: "filter excludes certificates without matching tags",
			certificates: []VaultCertificate{
				{
					Name: "tagged-cert",
					Tags: map[string]*string{"service": stringPtr("api")},
				},
				{
					Name: "other-cert",
					Tags: map[string]*string{"service": stringPtr("web")},
				},
				{
					Name: "untagged-cert",
					Tags: nil,
				},
			},
			filters: map[string]string{
				"service": "api",
			},
			expectedCount: 1,
			expectedNames: []string{"tagged-cert"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := FilterCertificatesByTags(tt.certificates, tt.filters)

			assert.Equal(t, tt.expectedCount, len(filtered))

			if tt.expectedCount > 0 {
				actualNames := make([]string, len(filtered))
				for i, cert := range filtered {
					actualNames[i] = cert.Name
				}
				assert.ElementsMatch(t, tt.expectedNames, actualNames)
			}
		})
	}
}

// TestMatchesAllFilters_EdgeCases tests edge cases for MatchesAllFilters
func TestMatchesAllFilters_EdgeCases(t *testing.T) {
	t.Run("nil tags with nil/empty filters - doesn't match", func(t *testing.T) {
		// Nil tags means no tags at all, which should fail even with no filters
		// This is consistent: a secret with no tags shouldn't match anything
		// FilterSecretsByTags returns ALL secrets when filters is empty, but that's
		// handled at the FilterSecretsByTags level, not in MatchesAllFilters
		result := MatchesAllFilters(nil, nil)
		assert.False(t, result, "nil tags should always return false (no tags = can't match)")
	})

	t.Run("both empty - matches (no filters to fail)", func(t *testing.T) {
		result := MatchesAllFilters(map[string]*string{}, map[string]string{})
		assert.True(t, result)
	})

	t.Run("whitespace doesn't count as match", func(t *testing.T) {
		tags := map[string]*string{
			"service": stringPtr("api "),
		}
		filters := map[string]string{
			"service": "api",
		}
		result := MatchesAllFilters(tags, filters)
		assert.False(t, result, "whitespace should cause mismatch (no normalization)")
	})

	t.Run("empty string tag value doesn't match empty string filter", func(t *testing.T) {
		tags := map[string]*string{
			"service": stringPtr(""),
		}
		filters := map[string]string{
			"service": "",
		}
		result := MatchesAllFilters(tags, filters)
		assert.True(t, result, "empty strings should match exactly")
	})
}
