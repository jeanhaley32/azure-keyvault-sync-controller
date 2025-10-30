package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

// TestIsValidForSync tests the core opt-in logic
func TestIsValidForSync(t *testing.T) {
	tests := []struct {
		name               string
		annotations        map[string]string
		expectValid        bool
		expectServiceAcct  string
	}{
		{
			name: "with service-account annotation",
			annotations: map[string]string{
				annotationServiceAccount: "my-app",
			},
			expectValid:       true,
			expectServiceAcct: "my-app",
		},
		{
			name:               "no annotations at all",
			annotations:        nil,
			expectValid:        false,
			expectServiceAcct:  "",
		},
		{
			name:               "empty annotations map",
			annotations:        map[string]string{},
			expectValid:        false,
			expectServiceAcct:  "",
		},
		{
			name: "service-account with empty value",
			annotations: map[string]string{
				annotationServiceAccount: "",
			},
			expectValid:       true,
			expectServiceAcct: "",
		},
		{
			name: "only respect-tags annotation (no service-account)",
			annotations: map[string]string{
				annotationRespectTags: "true",
			},
			expectValid:       false,
			expectServiceAcct: "",
		},
		{
			name: "service-account with other annotations",
			annotations: map[string]string{
				annotationServiceAccount: "web-api",
				annotationRespectTags:    "true",
				"azure-keyvault-sync/last-sync": "2025-10-29T12:34:56Z",
			},
			expectValid:       true,
			expectServiceAcct: "web-api",
		},
		{
			name: "service-account with whitespace",
			annotations: map[string]string{
				annotationServiceAccount: "  app-sa  ",
			},
			expectValid:       true,
			expectServiceAcct: "  app-sa  ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spc := &secretsstorev1.SecretProviderClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-spc",
					Namespace:   "default",
					Annotations: tt.annotations,
				},
			}

			valid, serviceAccount := isValidForSync(spc)
			assert.Equal(t, tt.expectValid, valid)
			assert.Equal(t, tt.expectServiceAcct, serviceAccount)
		})
	}
}

// TestGetServiceAccount tests the service account extraction
func TestGetServiceAccount(t *testing.T) {
	tests := []struct {
		name          string
		annotations   map[string]string
		expectSA      string
		expectExists  bool
	}{
		{
			name: "service-account present",
			annotations: map[string]string{
				annotationServiceAccount: "my-service",
			},
			expectSA:     "my-service",
			expectExists: true,
		},
		{
			name:         "nil annotations",
			annotations:  nil,
			expectSA:     "",
			expectExists: false,
		},
		{
			name:         "empty annotations map",
			annotations:  map[string]string{},
			expectSA:     "",
			expectExists: false,
		},
		{
			name: "service-account with empty string value",
			annotations: map[string]string{
				annotationServiceAccount: "",
			},
			expectSA:     "",
			expectExists: true,
		},
		{
			name: "other annotations present but not service-account",
			annotations: map[string]string{
				annotationRespectTags: "true",
				"some-other-annotation": "value",
			},
			expectSA:     "",
			expectExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spc := &secretsstorev1.SecretProviderClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-spc",
					Namespace:   "default",
					Annotations: tt.annotations,
				},
			}

			sa, exists := getServiceAccount(spc)
			assert.Equal(t, tt.expectSA, sa)
			assert.Equal(t, tt.expectExists, exists)
		})
	}
}

// TestAnnotationOptIn tests the implicit opt-in behavior
func TestAnnotationOptIn(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		description string
		shouldSync  bool
	}{
		{
			name: "implicit opt-in with service-account",
			annotations: map[string]string{
				annotationServiceAccount: "my-app",
			},
			description: "Adding service-account annotation should enable sync",
			shouldSync:  true,
		},
		{
			name:        "no opt-in without service-account",
			annotations: map[string]string{},
			description: "No service-account annotation means no sync",
			shouldSync:  false,
		},
		{
			name: "respect-tags alone is not enough",
			annotations: map[string]string{
				annotationRespectTags: "true",
			},
			description: "respect-tags without service-account should not enable sync",
			shouldSync:  false,
		},
		{
			name: "last-sync alone is not enough",
			annotations: map[string]string{
				"azure-keyvault-sync/last-sync": "2025-10-29T12:34:56Z",
			},
			description: "Controller-set annotations without service-account should not enable sync",
			shouldSync:  false,
		},
		{
			name: "service-account is sufficient",
			annotations: map[string]string{
				annotationServiceAccount: "app",
			},
			description: "Only service-account annotation is needed",
			shouldSync:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spc := &secretsstorev1.SecretProviderClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-spc",
					Namespace:   "default",
					Annotations: tt.annotations,
				},
			}

			valid, _ := isValidForSync(spc)
			assert.Equal(t, tt.shouldSync, valid, tt.description)
		})
	}
}

// TestAnnotationLifecycle tests annotation changes over time
func TestAnnotationLifecycle(t *testing.T) {
	t.Run("adding service-account enables sync", func(t *testing.T) {
		// Start with no annotations
		spc := &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-spc",
				Namespace:   "default",
				Annotations: nil,
			},
		}
		valid, _ := isValidForSync(spc)
		assert.False(t, valid, "Should not sync without annotations")

		// Add service-account annotation
		spc.Annotations = map[string]string{
			annotationServiceAccount: "my-app",
		}
		valid, sa := isValidForSync(spc)
		assert.True(t, valid, "Should sync after adding service-account")
		assert.Equal(t, "my-app", sa)
	})

	t.Run("removing service-account disables sync", func(t *testing.T) {
		// Start with service-account
		spc := &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-spc",
				Namespace: "default",
				Annotations: map[string]string{
					annotationServiceAccount: "my-app",
				},
			},
		}
		valid, _ := isValidForSync(spc)
		assert.True(t, valid, "Should sync with service-account")

		// Remove service-account annotation
		delete(spc.Annotations, annotationServiceAccount)
		valid, sa := isValidForSync(spc)
		assert.False(t, valid, "Should not sync after removing service-account")
		assert.Equal(t, "", sa)
	})

	t.Run("changing service-account value", func(t *testing.T) {
		spc := &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-spc",
				Namespace: "default",
				Annotations: map[string]string{
					annotationServiceAccount: "old-sa",
				},
			},
		}
		valid, sa := isValidForSync(spc)
		assert.True(t, valid)
		assert.Equal(t, "old-sa", sa)

		// Change to new service account
		spc.Annotations[annotationServiceAccount] = "new-sa"
		valid, sa = isValidForSync(spc)
		assert.True(t, valid)
		assert.Equal(t, "new-sa", sa)
	})
}

// TestBackwardCompatibility ensures no old "enabled" annotation behavior
func TestBackwardCompatibility(t *testing.T) {
	t.Run("old enabled annotation is ignored", func(t *testing.T) {
		spc := &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-spc",
				Namespace: "default",
				Annotations: map[string]string{
					"azure-keyvault-sync/enabled": "true",
				},
			},
		}
		valid, _ := isValidForSync(spc)
		assert.False(t, valid, "Old 'enabled' annotation should be ignored without service-account")
	})

	t.Run("enabled with service-account", func(t *testing.T) {
		spc := &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-spc",
				Namespace: "default",
				Annotations: map[string]string{
					"azure-keyvault-sync/enabled": "true",
					annotationServiceAccount:      "my-app",
				},
			},
		}
		valid, sa := isValidForSync(spc)
		assert.True(t, valid, "Service-account should enable sync regardless of old enabled annotation")
		assert.Equal(t, "my-app", sa)
	})
}
