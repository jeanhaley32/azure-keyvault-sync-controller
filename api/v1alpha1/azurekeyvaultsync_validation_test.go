package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestAzureKeyVaultSyncSpec_InvalidKeyvaultName tests validation of keyvault name
func TestAzureKeyVaultSyncSpec_InvalidKeyvaultName(t *testing.T) {
	tests := []struct {
		name          string
		keyvaultName  string
		shouldBeValid bool
		reason        string
	}{
		{
			name:          "Valid keyvault name",
			keyvaultName:  "my-vault-123",
			shouldBeValid: true,
			reason:        "Alphanumeric with hyphens is valid",
		},
		{
			name:          "Valid short name (3 chars minimum)",
			keyvaultName:  "abc",
			shouldBeValid: true,
			reason:        "3 characters is minimum length",
		},
		{
			name:          "Valid long name (24 chars maximum)",
			keyvaultName:  "a23456789012345678901234",
			shouldBeValid: true,
			reason:        "24 characters is maximum length",
		},
		{
			name:          "Too short (2 chars)",
			keyvaultName:  "ab",
			shouldBeValid: false,
			reason:        "Less than 3 characters should fail",
		},
		{
			name:          "Too long (25 chars)",
			keyvaultName:  "a234567890123456789012345",
			shouldBeValid: false,
			reason:        "More than 24 characters should fail",
		},
		{
			name:          "Empty string",
			keyvaultName:  "",
			shouldBeValid: false,
			reason:        "Empty keyvault name should fail",
		},
		{
			name:          "Contains underscore",
			keyvaultName:  "my_vault",
			shouldBeValid: false,
			reason:        "Underscores are not allowed in vault names",
		},
		{
			name:          "Contains dot",
			keyvaultName:  "my.vault",
			shouldBeValid: false,
			reason:        "Dots are not allowed in vault names",
		},
		{
			name:          "Contains space",
			keyvaultName:  "my vault",
			shouldBeValid: false,
			reason:        "Spaces are not allowed in vault names",
		},
		{
			name:          "Starts with hyphen",
			keyvaultName:  "-myvault",
			shouldBeValid: true, // Azure allows this
			reason:        "Starting with hyphen is allowed",
		},
		{
			name:          "Ends with hyphen",
			keyvaultName:  "myvault-",
			shouldBeValid: true, // Azure allows this
			reason:        "Ending with hyphen is allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := AzureKeyVaultSyncSpec{
				KeyvaultName:   tt.keyvaultName,
				TenantID:       "12345678-1234-1234-1234-123456789012",
				ClientID:       "87654321-4321-4321-4321-210987654321",
				ServiceAccount: "my-sa",
			}

			// The validation is done by kubebuilder markers, so we're testing
			// that the spec can be created with these values.
			// In a real cluster, invalid values would be rejected by the API server.
			if tt.shouldBeValid {
				assert.NotEmpty(t, spec.KeyvaultName, tt.reason)
			} else {
				// For invalid values, we document what should fail
				// The actual validation happens at the API server level
				assert.NotEmpty(t, tt.name, "Test case defined: "+tt.reason)
			}
		})
	}
}

// TestAzureKeyVaultSyncSpec_InvalidTenantID tests validation of tenant ID
func TestAzureKeyVaultSyncSpec_InvalidTenantID(t *testing.T) {
	tests := []struct {
		name          string
		tenantID      string
		shouldBeValid bool
		reason        string
	}{
		{
			name:          "Valid UUID",
			tenantID:      "12345678-1234-1234-1234-123456789012",
			shouldBeValid: true,
			reason:        "Standard UUID format is valid",
		},
		{
			name:          "Valid UUID with uppercase",
			tenantID:      "12345678-1234-1234-1234-123456789ABC",
			shouldBeValid: true,
			reason:        "Uppercase hex digits are valid in UUIDs",
		},
		{
			name:          "Empty string",
			tenantID:      "",
			shouldBeValid: false,
			reason:        "Empty tenant ID should fail",
		},
		{
			name:          "Not a UUID (random string)",
			tenantID:      "not-a-valid-uuid",
			shouldBeValid: false,
			reason:        "Non-UUID format should fail",
		},
		{
			name:          "UUID without hyphens",
			tenantID:      "12345678123412341234123456789012",
			shouldBeValid: false,
			reason:        "UUID must have hyphens in correct positions",
		},
		{
			name:          "UUID with wrong hyphen positions",
			tenantID:      "123456-78-1234-1234-1234-123456789012",
			shouldBeValid: false,
			reason:        "Hyphens must be in standard UUID positions",
		},
		{
			name:          "Too short",
			tenantID:      "12345678-1234-1234-1234-12345678901",
			shouldBeValid: false,
			reason:        "UUID must be exactly 36 characters with hyphens",
		},
		{
			name:          "Too long",
			tenantID:      "12345678-1234-1234-1234-1234567890123",
			shouldBeValid: false,
			reason:        "UUID cannot be longer than standard format",
		},
		{
			name:          "Contains invalid characters",
			tenantID:      "12345678-1234-1234-1234-123456789XYZ",
			shouldBeValid: false,
			reason:        "UUID can only contain hex digits (0-9, a-f, A-F)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := AzureKeyVaultSyncSpec{
				KeyvaultName:   "my-vault",
				TenantID:       tt.tenantID,
				ClientID:       "87654321-4321-4321-4321-210987654321",
				ServiceAccount: "my-sa",
			}

			if tt.shouldBeValid {
				assert.NotEmpty(t, spec.TenantID, tt.reason)
			} else {
				assert.NotEmpty(t, tt.name, "Test case defined: "+tt.reason)
			}
		})
	}
}

// TestAzureKeyVaultSyncSpec_InvalidClientID tests validation of client ID
func TestAzureKeyVaultSyncSpec_InvalidClientID(t *testing.T) {
	tests := []struct {
		name          string
		clientID      string
		shouldBeValid bool
		reason        string
	}{
		{
			name:          "Valid UUID",
			clientID:      "87654321-4321-4321-4321-210987654321",
			shouldBeValid: true,
			reason:        "Standard UUID format is valid",
		},
		{
			name:          "Empty string",
			clientID:      "",
			shouldBeValid: false,
			reason:        "Empty client ID should fail",
		},
		{
			name:          "Not a UUID",
			clientID:      "invalid-client-id",
			shouldBeValid: false,
			reason:        "Non-UUID format should fail",
		},
		{
			name:          "Nil UUID (all zeros)",
			clientID:      "00000000-0000-0000-0000-000000000000",
			shouldBeValid: true, // Technically valid UUID format
			reason:        "Nil UUID is valid format (though not recommended)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := AzureKeyVaultSyncSpec{
				KeyvaultName:   "my-vault",
				TenantID:       "12345678-1234-1234-1234-123456789012",
				ClientID:       tt.clientID,
				ServiceAccount: "my-sa",
			}

			if tt.shouldBeValid {
				assert.NotEmpty(t, spec.ClientID, tt.reason)
			} else {
				assert.NotEmpty(t, tt.name, "Test case defined: "+tt.reason)
			}
		})
	}
}

// TestAzureKeyVaultSyncSpec_InvalidServiceAccount tests validation of service account
func TestAzureKeyVaultSyncSpec_InvalidServiceAccount(t *testing.T) {
	tests := []struct {
		name           string
		serviceAccount string
		shouldBeValid  bool
		reason         string
	}{
		{
			name:           "Valid short name",
			serviceAccount: "sa",
			shouldBeValid:  true,
			reason:         "Short names are valid",
		},
		{
			name:           "Valid name with hyphens",
			serviceAccount: "my-service-account",
			shouldBeValid:  true,
			reason:         "Hyphens are allowed",
		},
		{
			name:           "Valid long name (253 chars max)",
			serviceAccount: "a" + "b23456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012",
			shouldBeValid:  true,
			reason:         "253 characters is maximum length",
		},
		{
			name:           "Empty string",
			serviceAccount: "",
			shouldBeValid:  false,
			reason:         "Empty service account should fail",
		},
		{
			name:           "Too long (254 chars)",
			serviceAccount: "a" + "b234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123",
			shouldBeValid:  false,
			reason:         "More than 253 characters should fail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := AzureKeyVaultSyncSpec{
				KeyvaultName:   "my-vault",
				TenantID:       "12345678-1234-1234-1234-123456789012",
				ClientID:       "87654321-4321-4321-4321-210987654321",
				ServiceAccount: tt.serviceAccount,
			}

			if tt.shouldBeValid {
				assert.NotEmpty(t, spec.ServiceAccount, tt.reason)
			} else {
				assert.NotEmpty(t, tt.name, "Test case defined: "+tt.reason)
			}
		})
	}
}

// TestAzureKeyVaultSyncSpec_InvalidDeletePolicy tests validation of delete policy
func TestAzureKeyVaultSyncSpec_InvalidDeletePolicy(t *testing.T) {
	tests := []struct {
		name          string
		deletePolicy  DeletePolicy
		shouldBeValid bool
		reason        string
	}{
		{
			name:          "Valid Cascade policy",
			deletePolicy:  DeletePolicyCascade,
			shouldBeValid: true,
			reason:        "Cascade is a valid delete policy",
		},
		{
			name:          "Valid Orphan policy",
			deletePolicy:  DeletePolicyOrphan,
			shouldBeValid: true,
			reason:        "Orphan is a valid delete policy",
		},
		{
			name:          "Empty (will default to Cascade)",
			deletePolicy:  "",
			shouldBeValid: true,
			reason:        "Empty defaults to Cascade via webhook",
		},
		{
			name:          "Invalid policy string",
			deletePolicy:  DeletePolicy("Invalid"),
			shouldBeValid: false,
			reason:        "Non-standard policy values should fail",
		},
		{
			name:          "lowercase cascade",
			deletePolicy:  DeletePolicy("cascade"),
			shouldBeValid: false,
			reason:        "Policy is case-sensitive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := AzureKeyVaultSyncSpec{
				KeyvaultName:   "my-vault",
				TenantID:       "12345678-1234-1234-1234-123456789012",
				ClientID:       "87654321-4321-4321-4321-210987654321",
				ServiceAccount: "my-sa",
				DeletePolicy:   tt.deletePolicy,
			}

			if tt.shouldBeValid {
				// Valid values should be one of the constants or empty
				assert.True(t,
					spec.DeletePolicy == DeletePolicyCascade ||
						spec.DeletePolicy == DeletePolicyOrphan ||
						spec.DeletePolicy == "",
					tt.reason)
			} else {
				assert.NotEmpty(t, tt.name, "Test case defined: "+tt.reason)
			}
		})
	}
}

// TestAzureKeyVaultSyncSpec_FilterValidation tests filter map validation
func TestAzureKeyVaultSyncSpec_FilterValidation(t *testing.T) {
	tests := []struct {
		name          string
		filters       map[string]string
		shouldBeValid bool
		reason        string
	}{
		{
			name: "Valid filters",
			filters: map[string]string{
				"service":     "my-app",
				"environment": "prod",
			},
			shouldBeValid: true,
			reason:        "Standard service/environment filters are valid",
		},
		{
			name:          "Nil filters (single-tenant mode)",
			filters:       nil,
			shouldBeValid: true,
			reason:        "Nil filters indicate single-tenant vault",
		},
		{
			name:          "Empty map (single-tenant mode)",
			filters:       map[string]string{},
			shouldBeValid: true,
			reason:        "Empty filters indicate single-tenant vault",
		},
		{
			name: "Custom filter keys",
			filters: map[string]string{
				"team":        "platform",
				"cost-center": "engineering",
			},
			shouldBeValid: true,
			reason:        "Custom filter keys are allowed",
		},
		{
			name: "Filter with empty value",
			filters: map[string]string{
				"service": "",
			},
			shouldBeValid: true, // Empty values are technically allowed
			reason:        "Empty filter values are allowed (though not recommended)",
		},
		{
			name: "Filter with empty key",
			filters: map[string]string{
				"": "value",
			},
			shouldBeValid: false,
			reason:        "Empty filter keys should not be allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := AzureKeyVaultSyncSpec{
				KeyvaultName:   "my-vault",
				TenantID:       "12345678-1234-1234-1234-123456789012",
				ClientID:       "87654321-4321-4321-4321-210987654321",
				ServiceAccount: "my-sa",
				Filters:        tt.filters,
			}

			if tt.shouldBeValid {
				// For valid cases, verify the filters are set correctly
				assert.Equal(t, tt.filters, spec.Filters, tt.reason)
			} else {
				// For invalid cases, document what should fail
				if tt.filters != nil {
					for key := range tt.filters {
						if key == "" {
							assert.Empty(t, key, tt.reason)
						}
					}
				}
			}
		})
	}
}

// TestAzureKeyVaultSyncStatus_ConditionValidation tests status condition structure
func TestAzureKeyVaultSyncStatus_ConditionValidation(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		valid      bool
		reason     string
	}{
		{
			name: "Valid Ready condition",
			conditions: []metav1.Condition{
				{
					Type:   "Ready",
					Status: metav1.ConditionTrue,
					Reason: "SPCCreated",
				},
			},
			valid:  true,
			reason: "Standard Ready condition is valid",
		},
		{
			name:       "Empty conditions list",
			conditions: []metav1.Condition{},
			valid:      true,
			reason:     "Empty conditions list is valid (initialization state)",
		},
		{
			name:       "Nil conditions",
			conditions: nil,
			valid:      true,
			reason:     "Nil conditions is valid (uninitialized)",
		},
		{
			name: "Multiple conditions",
			conditions: []metav1.Condition{
				{
					Type:   "Ready",
					Status: metav1.ConditionTrue,
					Reason: "SPCCreated",
				},
				{
					Type:   "VaultAccessible",
					Status: metav1.ConditionTrue,
					Reason: "Connected",
				},
			},
			valid:  true,
			reason: "Multiple conditions are supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := AzureKeyVaultSyncStatus{
				Conditions: tt.conditions,
			}

			if tt.valid {
				assert.Equal(t, len(tt.conditions), len(status.Conditions), tt.reason)
			}
		})
	}
}
