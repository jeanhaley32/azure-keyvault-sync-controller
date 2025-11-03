package controller

import (
	"strings"
	"testing"

	akvv1alpha1 "github.com/jeanhaley32/azure-keyvault-sync-controller/api/v1alpha1"
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func TestGenerateSecretProviderClass(t *testing.T) {
	tests := []struct {
		name                      string
		akv                       *akvv1alpha1.AzureKeyVaultSync
		secrets                   []azure.VaultSecret
		expectedSPCName           string
		expectedParameterCount    int
		expectedOwnerRefCount     int
		expectedControllerOwned   bool
		expectedBlockDeletion     bool
	}{
		{
			name: "basic CRD with no secrets",
			akv: &akvv1alpha1.AzureKeyVaultSync{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-akv",
					Namespace: "default",
					UID:       "test-uid-123",
				},
				TypeMeta: metav1.TypeMeta{
					APIVersion: "azure-keyvault-sync.io/v1alpha1",
					Kind:       "AzureKeyVaultSync",
				},
				Spec: akvv1alpha1.AzureKeyVaultSyncSpec{
					KeyvaultName:   "my-vault",
					TenantID:       "tenant-123",
					ClientID:       "client-123",
					ServiceAccount: "my-sa",
					DeletePolicy:   akvv1alpha1.DeletePolicyCascade,
				},
			},
			secrets:                []azure.VaultSecret{},
			expectedSPCName:        "test-akv",
			expectedParameterCount: 3, // keyvaultName, tenantId, clientID
			expectedOwnerRefCount:  1,
			expectedControllerOwned: true,
			expectedBlockDeletion:  true,
		},
		{
			name: "CRD with orphan delete policy",
			akv: &akvv1alpha1.AzureKeyVaultSync{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphan-akv",
					Namespace: "production",
					UID:       "orphan-uid-456",
				},
				TypeMeta: metav1.TypeMeta{
					APIVersion: "azure-keyvault-sync.io/v1alpha1",
					Kind:       "AzureKeyVaultSync",
				},
				Spec: akvv1alpha1.AzureKeyVaultSyncSpec{
					KeyvaultName:   "prod-vault",
					TenantID:       "tenant-456",
					ClientID:       "client-456",
					ServiceAccount: "prod-sa",
					DeletePolicy:   akvv1alpha1.DeletePolicyOrphan,
				},
			},
			secrets:                 []azure.VaultSecret{},
			expectedSPCName:         "orphan-akv",
			expectedParameterCount:  3,
			expectedOwnerRefCount:   1,
			expectedControllerOwned: true,
			expectedBlockDeletion:   false, // Orphan policy should not block deletion
		},
		{
			name: "CRD with secrets containing tags",
			akv: &akvv1alpha1.AzureKeyVaultSync{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tagged-akv",
					Namespace: "default",
					UID:       "tagged-uid-789",
				},
				TypeMeta: metav1.TypeMeta{
					APIVersion: "azure-keyvault-sync.io/v1alpha1",
					Kind:       "AzureKeyVaultSync",
				},
				Spec: akvv1alpha1.AzureKeyVaultSyncSpec{
					KeyvaultName:   "tagged-vault",
					TenantID:       "tenant-789",
					ClientID:       "client-789",
					ServiceAccount: "tagged-sa",
					DeletePolicy:   akvv1alpha1.DeletePolicyCascade,
				},
			},
			secrets: []azure.VaultSecret{
				{
					Name: "api-key",
					Tags: map[string]*string{
						"service": stringPtr("api"),
					},
				},
				{
					Name: "db-password",
					Tags: map[string]*string{
						"service":       stringPtr("database"),
						"secret-object": stringPtr("true"),
					},
				},
			},
			expectedSPCName:         "tagged-akv",
			expectedParameterCount:  4, // keyvaultName, tenantId, clientID, objects
			expectedOwnerRefCount:   1,
			expectedControllerOwned: true,
			expectedBlockDeletion:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spc := generateSecretProviderClass(tt.akv, tt.secrets)

			// Verify SPC name matches CRD name
			assert.Equal(t, tt.expectedSPCName, spc.Name)
			assert.Equal(t, tt.akv.Namespace, spc.Namespace)

			// Verify parameters
			assert.Equal(t, tt.expectedParameterCount, len(spc.Spec.Parameters))
			assert.Equal(t, tt.akv.Spec.KeyvaultName, spc.Spec.Parameters["keyvaultName"])
			assert.Equal(t, tt.akv.Spec.TenantID, spc.Spec.Parameters["tenantId"])
			assert.Equal(t, tt.akv.Spec.ClientID, spc.Spec.Parameters["clientID"])

			// Verify provider
			assert.Equal(t, secretsstorev1.Provider("azure"), spc.Spec.Provider)

			// Verify owner references
			assert.Equal(t, tt.expectedOwnerRefCount, len(spc.OwnerReferences))
			if len(spc.OwnerReferences) > 0 {
				ownerRef := spc.OwnerReferences[0]
				assert.Equal(t, tt.akv.APIVersion, ownerRef.APIVersion)
				assert.Equal(t, tt.akv.Kind, ownerRef.Kind)
				assert.Equal(t, tt.akv.Name, ownerRef.Name)
				assert.Equal(t, tt.akv.UID, ownerRef.UID)
				assert.NotNil(t, ownerRef.Controller)
				assert.Equal(t, tt.expectedControllerOwned, *ownerRef.Controller)
				assert.NotNil(t, ownerRef.BlockOwnerDeletion)
				assert.Equal(t, tt.expectedBlockDeletion, *ownerRef.BlockOwnerDeletion)
			}
		})
	}
}

func TestGenerateSecretProviderClass_SecretObjectTag(t *testing.T) {
	akv := &akvv1alpha1.AzureKeyVaultSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-akv",
			Namespace: "default",
			UID:       "test-uid",
		},
		TypeMeta: metav1.TypeMeta{
			APIVersion: "azure-keyvault-sync.io/v1alpha1",
			Kind:       "AzureKeyVaultSync",
		},
		Spec: akvv1alpha1.AzureKeyVaultSyncSpec{
			KeyvaultName:   "my-vault",
			TenantID:       "tenant-123",
			ClientID:       "client-123",
			ServiceAccount: "my-sa",
			DeletePolicy:   akvv1alpha1.DeletePolicyCascade,
		},
	}

	secrets := []azure.VaultSecret{
		{
			Name: "normal-secret",
			Tags: map[string]*string{
				"service": stringPtr("api"),
				"sync":    stringPtr("true"), // Must have sync opt-in
			},
		},
		{
			Name: "secret-with-object-tag",
			Tags: map[string]*string{
				"service":       stringPtr("api"),
				"secret-object": stringPtr("true"), // secret-object implies sync
			},
		},
		{
			Name: "another-normal-secret",
			Tags: map[string]*string{
				"sync": stringPtr("true"), // Must have sync opt-in
			},
		},
	}

	spc := generateSecretProviderClass(akv, secrets)

	// Verify objects parameter exists when secrets are present
	assert.Contains(t, spc.Spec.Parameters, "objects")
	assert.NotEmpty(t, spc.Spec.Parameters["objects"])

	// Verify the objects array string contains all synced secrets (with sync opt-in)
	objectsStr := spc.Spec.Parameters["objects"]
	assert.Contains(t, objectsStr, "normal-secret")
	assert.Contains(t, objectsStr, "secret-with-object-tag")
	assert.Contains(t, objectsStr, "another-normal-secret")
}

func TestBuildObjectsArrayString(t *testing.T) {
	tests := []struct {
		name             string
		objects          []map[string]interface{}
		expectedContains []string
	}{
		{
			name: "single object without alias",
			objects: []map[string]interface{}{
				{
					"objectName": "api-key",
					"objectType": "secret",
				},
			},
			expectedContains: []string{
				"objectName: api-key",
				"objectType: secret",
			},
		},
		{
			name: "object with alias",
			objects: []map[string]interface{}{
				{
					"objectName":  "db-password",
					"objectType":  "secret",
					"objectAlias": "db-password",
				},
			},
			expectedContains: []string{
				"objectName: db-password",
				"objectType: secret",
				"objectAlias: db-password",
			},
		},
		{
			name: "multiple objects mixed",
			objects: []map[string]interface{}{
				{
					"objectName": "secret1",
					"objectType": "secret",
				},
				{
					"objectName":  "secret2",
					"objectType":  "secret",
					"objectAlias": "secret2",
				},
			},
			expectedContains: []string{
				"objectName: secret1",
				"objectName: secret2",
				"objectAlias: secret2",
			},
		},
		{
			name: "special characters are properly escaped",
			objects: []map[string]interface{}{
				{
					"objectName": "secret-with-special: chars\nand newlines",
					"objectType": "secret",
				},
			},
			expectedContains: []string{
				"array:",
				"objectName:",
				"objectType: secret",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildObjectsArrayString(tt.objects)

			// Verify no error occurred
			assert.NoError(t, err)
			assert.NotEmpty(t, result)

			// Verify it starts with array marker
			assert.Contains(t, result, "array:")

			// Verify all expected strings are present
			for _, expected := range tt.expectedContains {
				assert.Contains(t, result, expected)
			}
		})
	}
}

// TestBuildObjectsArrayStringInjectionPrevention tests that YAML injection is prevented
func TestBuildObjectsArrayStringInjectionPrevention(t *testing.T) {
	tests := []struct {
		name         string
		objects      []map[string]interface{}
		mustContain  []string
		description  string
	}{
		{
			name: "newline injection attempt is safely escaped",
			objects: []map[string]interface{}{
				{
					"objectName": "malicious\ninjection: true\nobjectName: real",
					"objectType": "secret",
				},
			},
			mustContain: []string{
				"array:",
				"objectType: secret",
				// The injected content should be in a literal block (|-) not as separate fields
				"objectName: |-",
			},
			description: "Newlines in objectName should be in a literal block, not create new YAML fields",
		},
		{
			name: "colon injection attempt is safely quoted",
			objects: []map[string]interface{}{
				{
					"objectName": "malicious: injected-value",
					"objectType": "secret",
				},
			},
			mustContain: []string{
				"array:",
				"objectType: secret",
				// Colons in values should be quoted or in a literal block
				"objectName:",
			},
			description: "Colons should be properly quoted/escaped",
		},
		{
			name: "multi-line injection with indentation",
			objects: []map[string]interface{}{
				{
					"objectName": "test\n    newField: malicious\n    anotherField: injection",
					"objectType": "secret",
				},
			},
			mustContain: []string{
				"array:",
				"objectType: secret",
				"objectName: |-",
			},
			description: "Indentation injection should be contained in literal block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildObjectsArrayString(tt.objects)

			// Should not error
			assert.NoError(t, err, tt.description)

			// Should produce valid YAML
			assert.NotEmpty(t, result)

			// Verify expected safe patterns are present
			for _, expected := range tt.mustContain {
				assert.Contains(t, result, expected, tt.description)
			}

			// Verify the structure: objectType should be a sibling to objectName, not nested
			// Count the number of times "objectType: secret" appears at the correct indentation
			// It should appear exactly once per object, at the correct level
			lines := strings.Split(result, "\n")
			objectTypeCount := 0
			for _, line := range lines {
				// objectType should be indented with 2 spaces (array item level)
				if strings.TrimSpace(line) == "objectType: secret" && strings.HasPrefix(line, "  ") {
					objectTypeCount++
				}
			}
			assert.Equal(t, len(tt.objects), objectTypeCount,
				"objectType should appear once per object at correct indentation level")
		})
	}
}

func TestBoolPtr(t *testing.T) {
	t.Run("true pointer", func(t *testing.T) {
		ptr := boolPtr(true)
		assert.NotNil(t, ptr)
		assert.True(t, *ptr)
	})

	t.Run("false pointer", func(t *testing.T) {
		ptr := boolPtr(false)
		assert.NotNil(t, ptr)
		assert.False(t, *ptr)
	})
}

// Helper function for creating string pointers in tests
func stringPtr(s string) *string {
	return &s
}
