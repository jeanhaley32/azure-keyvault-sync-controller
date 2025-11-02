package azure

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTransformTagsToSPCAnnotations(t *testing.T) {
	tests := []struct {
		name       string
		secretName string
		tags       map[string]*string
		expected   map[string]string
	}{
		{
			name:       "single k8s-annotation tag",
			secretName: "my-secret",
			tags: map[string]*string{
				"k8s-annotation.reflector/allowed": strPtr("true"),
			},
			expected: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/my-secret.reflector/allowed": "true",
			},
		},
		{
			name:       "multiple k8s-annotation tags",
			secretName: "db-password",
			tags: map[string]*string{
				"k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed": strPtr("true"),
				"k8s-annotation.owner": strPtr("team-alpha"),
				"k8s-annotation.environment": strPtr("production"),
			},
			expected: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/db-password.reflector.v1.k8s.emberstack.com/reflection-allowed": "true",
				"secret-metadata.azure-keyvault-sync.io/db-password.owner":       "team-alpha",
				"secret-metadata.azure-keyvault-sync.io/db-password.environment": "production",
			},
		},
		{
			name:       "mixed tags (k8s-annotation and others)",
			secretName: "api-key",
			tags: map[string]*string{
				"k8s-annotation.team":        strPtr("backend"),
				"azure-tag":                  strPtr("value"),
				"environment":                strPtr("prod"),
				"k8s-annotation.app-version": strPtr("1.2.3"),
			},
			expected: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/api-key.team":        "backend",
				"secret-metadata.azure-keyvault-sync.io/api-key.app-version": "1.2.3",
			},
		},
		{
			name:       "no k8s-annotation tags",
			secretName: "my-secret",
			tags: map[string]*string{
				"environment": strPtr("staging"),
				"owner":       strPtr("team-alpha"),
			},
			expected: map[string]string{},
		},
		{
			name:       "nil tag value",
			secretName: "my-secret",
			tags: map[string]*string{
				"k8s-annotation.valid": strPtr("value"),
				"k8s-annotation.nil":   nil,
			},
			expected: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/my-secret.valid": "value",
			},
		},
		{
			name:       "empty annotation key after prefix",
			secretName: "my-secret",
			tags: map[string]*string{
				"k8s-annotation.": strPtr("value"),
			},
			expected: map[string]string{},
		},
		{
			name:       "empty tags map",
			secretName: "my-secret",
			tags:       map[string]*string{},
			expected:   map[string]string{},
		},
		{
			name:       "nil tags map",
			secretName: "my-secret",
			tags:       nil,
			expected:   map[string]string{},
		},
		{
			name:       "complex annotation keys with slashes and dots",
			secretName: "tls-cert",
			tags: map[string]*string{
				"k8s-annotation.cert-manager.io/issuer":       strPtr("letsencrypt-prod"),
				"k8s-annotation.cert-manager.io/common-name":  strPtr("example.com"),
				"k8s-annotation.nginx.ingress.kubernetes.io/ssl-redirect": strPtr("true"),
			},
			expected: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/tls-cert.cert-manager.io/issuer":      "letsencrypt-prod",
				"secret-metadata.azure-keyvault-sync.io/tls-cert.cert-manager.io/common-name": "example.com",
				"secret-metadata.azure-keyvault-sync.io/tls-cert.nginx.ingress.kubernetes.io/ssl-redirect": "true",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TransformTagsToSPCAnnotations(tt.secretName, tt.tags)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractAnnotationsForSecret(t *testing.T) {
	tests := []struct {
		name           string
		spcAnnotations map[string]string
		secretName     string
		expected       map[string]string
	}{
		{
			name: "single annotation for secret",
			spcAnnotations: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/my-secret.reflector/allowed": "true",
			},
			secretName: "my-secret",
			expected: map[string]string{
				"reflector/allowed": "true",
			},
		},
		{
			name: "multiple annotations for secret",
			spcAnnotations: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/db-password.reflector.v1.k8s.emberstack.com/reflection-allowed": "true",
				"secret-metadata.azure-keyvault-sync.io/db-password.owner":       "team-alpha",
				"secret-metadata.azure-keyvault-sync.io/db-password.environment": "production",
			},
			secretName: "db-password",
			expected: map[string]string{
				"reflector.v1.k8s.emberstack.com/reflection-allowed": "true",
				"owner":       "team-alpha",
				"environment": "production",
			},
		},
		{
			name: "multiple secrets - extract only one",
			spcAnnotations: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/secret-a.owner": "team-a",
				"secret-metadata.azure-keyvault-sync.io/secret-b.owner": "team-b",
				"secret-metadata.azure-keyvault-sync.io/secret-b.env":   "prod",
			},
			secretName: "secret-b",
			expected: map[string]string{
				"owner": "team-b",
				"env":   "prod",
			},
		},
		{
			name: "no annotations for specified secret",
			spcAnnotations: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/other-secret.owner": "team",
			},
			secretName: "my-secret",
			expected:   map[string]string{},
		},
		{
			name: "mixed annotations (secret-specific and others)",
			spcAnnotations: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/my-secret.team": "backend",
				"other-annotation": "value",
				"random-key":       "random-value",
			},
			secretName: "my-secret",
			expected: map[string]string{
				"team": "backend",
			},
		},
		{
			name:           "empty SPC annotations",
			spcAnnotations: map[string]string{},
			secretName:     "my-secret",
			expected:       map[string]string{},
		},
		{
			name:           "nil SPC annotations",
			spcAnnotations: nil,
			secretName:     "my-secret",
			expected:       map[string]string{},
		},
		{
			name: "complex annotation keys",
			spcAnnotations: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/tls-cert.cert-manager.io/issuer":       "letsencrypt",
				"secret-metadata.azure-keyvault-sync.io/tls-cert.cert-manager.io/common-name":  "example.com",
				"secret-metadata.azure-keyvault-sync.io/tls-cert.nginx.ingress.kubernetes.io/ssl-redirect": "true",
			},
			secretName: "tls-cert",
			expected: map[string]string{
				"cert-manager.io/issuer":      "letsencrypt",
				"cert-manager.io/common-name": "example.com",
				"nginx.ingress.kubernetes.io/ssl-redirect": "true",
			},
		},
		{
			name: "secret name with special characters",
			spcAnnotations: map[string]string{
				"secret-metadata.azure-keyvault-sync.io/my-secret-v2.owner": "team",
			},
			secretName: "my-secret-v2",
			expected: map[string]string{
				"owner": "team",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractAnnotationsForSecret(tt.spcAnnotations, tt.secretName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTransformTagsToSPCAnnotations_RoundTrip(t *testing.T) {
	// Test that transforming and extracting gives us the expected final annotations
	secretName := "my-db-password"
	originalTags := map[string]*string{
		"k8s-annotation.reflector.v1.k8s.emberstack.com/reflection-allowed": strPtr("true"),
		"k8s-annotation.owner": strPtr("team-alpha"),
		"azure-only-tag":       strPtr("ignored"),
	}

	// Step 1: Transform tags to SPC annotations
	spcAnnotations := TransformTagsToSPCAnnotations(secretName, originalTags)

	// Step 2: Extract annotations back for the secret
	finalAnnotations := ExtractAnnotationsForSecret(spcAnnotations, secretName)

	// Verify final annotations match what we expect
	expected := map[string]string{
		"reflector.v1.k8s.emberstack.com/reflection-allowed": "true",
		"owner": "team-alpha",
	}

	assert.Equal(t, expected, finalAnnotations)
}

// Helper function to create string pointers
func strPtr(s string) *string {
	return &s
}
