package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func TestGenerateObjectsFromVault(t *testing.T) {
	tests := []struct {
		name           string
		secrets        []string
		certs          []string
		expectedLen    int
		expectedFirst  *VaultObject
		expectedLast   *VaultObject
		checkSorting   bool
	}{
		{
			name:        "empty inputs",
			secrets:     []string{},
			certs:       []string{},
			expectedLen: 0,
		},
		{
			name:        "only secrets",
			secrets:     []string{"secret1", "secret2"},
			certs:       []string{},
			expectedLen: 2,
			expectedFirst: &VaultObject{
				ObjectName:    "secret1",
				ObjectType:    "secret",
				ObjectVersion: "",
			},
			expectedLast: &VaultObject{
				ObjectName:    "secret2",
				ObjectType:    "secret",
				ObjectVersion: "",
			},
		},
		{
			name:        "only certificates",
			secrets:     []string{},
			certs:       []string{"cert1", "cert2"},
			expectedLen: 2,
			expectedFirst: &VaultObject{
				ObjectName:    "cert1",
				ObjectType:    "cert",
				ObjectVersion: "",
			},
			expectedLast: &VaultObject{
				ObjectName:    "cert2",
				ObjectType:    "cert",
				ObjectVersion: "",
			},
		},
		{
			name:        "mixed secrets and certs",
			secrets:     []string{"api-key", "db-password"},
			certs:       []string{"tls-cert", "ca-cert"},
			expectedLen: 4,
			checkSorting: true,
		},
		{
			name:        "unsorted input gets sorted",
			secrets:     []string{"zebra-secret", "apple-secret"},
			certs:       []string{"yankee-cert", "bravo-cert"},
			expectedLen: 4,
			expectedFirst: &VaultObject{
				ObjectName:    "apple-secret",
				ObjectType:    "secret",
				ObjectVersion: "",
			},
			expectedLast: &VaultObject{
				ObjectName:    "zebra-secret",
				ObjectType:    "secret",
				ObjectVersion: "",
			},
			checkSorting: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateObjectsFromVault(tt.secrets, tt.certs)

			assert.Equal(t, tt.expectedLen, len(result), "unexpected number of objects")

			if tt.expectedLen > 0 {
				if tt.expectedFirst != nil {
					assert.Equal(t, tt.expectedFirst.ObjectName, result[0].ObjectName)
					assert.Equal(t, tt.expectedFirst.ObjectType, result[0].ObjectType)
					assert.Equal(t, tt.expectedFirst.ObjectVersion, result[0].ObjectVersion)
				}

				if tt.expectedLast != nil {
					assert.Equal(t, tt.expectedLast.ObjectName, result[len(result)-1].ObjectName)
					assert.Equal(t, tt.expectedLast.ObjectType, result[len(result)-1].ObjectType)
					assert.Equal(t, tt.expectedLast.ObjectVersion, result[len(result)-1].ObjectVersion)
				}

				if tt.checkSorting {
					// Verify sorting
					for i := 1; i < len(result); i++ {
						assert.True(t, result[i-1].ObjectName < result[i].ObjectName,
							"objects not sorted: %s should come before %s", result[i-1].ObjectName, result[i].ObjectName)
					}
				}
			}
		})
	}
}

func TestFormatObjectsYAML(t *testing.T) {
	tests := []struct {
		name           string
		objects        []VaultObject
		expectedError  bool
		expectedOutput string
		checkContains  []string
	}{
		{
			name:           "empty objects",
			objects:        []VaultObject{},
			expectedError:  false,
			expectedOutput: "",
		},
		{
			name: "single secret",
			objects: []VaultObject{
				{ObjectName: "my-secret", ObjectType: "secret", ObjectVersion: ""},
			},
			expectedError: false,
			checkContains: []string{
				"array:",
				"objectName: my-secret",
				"objectType: secret",
			},
		},
		{
			name: "single cert with version",
			objects: []VaultObject{
				{ObjectName: "my-cert", ObjectType: "cert", ObjectVersion: "v1"},
			},
			expectedError: false,
			checkContains: []string{
				"array:",
				"objectName: my-cert",
				"objectType: cert",
				"objectVersion: v1",
			},
		},
		{
			name: "multiple objects",
			objects: []VaultObject{
				{ObjectName: "secret1", ObjectType: "secret", ObjectVersion: ""},
				{ObjectName: "cert1", ObjectType: "cert", ObjectVersion: ""},
			},
			expectedError: false,
			checkContains: []string{
				"array:",
				"objectName: secret1",
				"objectType: secret",
				"objectName: cert1",
				"objectType: cert",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FormatObjectsYAML(tt.objects)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectedOutput != "" {
				assert.Equal(t, tt.expectedOutput, result)
			}

			for _, substr := range tt.checkContains {
				assert.Contains(t, result, substr, "expected YAML to contain: %s", substr)
			}
		})
	}
}

func TestDetectChanges(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		new      string
		expected bool
	}{
		{
			name:     "identical strings",
			current:  "array:\n  - objectName: secret1",
			new:      "array:\n  - objectName: secret1",
			expected: false,
		},
		{
			name:     "different content",
			current:  "array:\n  - objectName: secret1",
			new:      "array:\n  - objectName: secret2",
			expected: true,
		},
		{
			name:     "whitespace differences normalized",
			current:  "  array:\n  - objectName: secret1  ",
			new:      "array:\n  - objectName: secret1",
			expected: false,
		},
		{
			name:     "empty to non-empty",
			current:  "",
			new:      "array:\n  - objectName: secret1",
			expected: true,
		},
		{
			name:     "non-empty to empty",
			current:  "array:\n  - objectName: secret1",
			new:      "",
			expected: true,
		},
		{
			name:     "both empty",
			current:  "",
			new:      "",
			expected: false,
		},
		{
			name:     "both whitespace only",
			current:  "   \n  \t  ",
			new:      "  \t  \n   ",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectChanges(tt.current, tt.new)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func ptr(s string) *string {
	return &s
}

func TestGenerateSecretObjectsFromVault(t *testing.T) {
	tests := []struct {
		name        string
		secrets     []VaultSecretWithTags
		certs       []VaultCertWithTags
		expectedLen int
		checkFirst  *secretsstorev1.SecretObject
		checkLast   *secretsstorev1.SecretObject
	}{
		{
			name:        "empty inputs",
			secrets:     []VaultSecretWithTags{},
			certs:       []VaultCertWithTags{},
			expectedLen: 0,
		},
		{
			name: "secrets with secret-object=true tag",
			secrets: []VaultSecretWithTags{
				{Name: "db-password", Tags: map[string]*string{"secret-object": ptr("true")}},
				{Name: "api-key", Tags: map[string]*string{"secret-object": ptr("true")}},
			},
			certs:       []VaultCertWithTags{},
			expectedLen: 2,
			checkFirst: &secretsstorev1.SecretObject{
				SecretName: "api-key",
				Type:       "Opaque",
				Data: []*secretsstorev1.SecretObjectData{
					{Key: "api-key", ObjectName: "api-key"},
				},
			},
		},
		{
			name: "secrets without secret-object tag (opted out)",
			secrets: []VaultSecretWithTags{
				{Name: "db-password", Tags: map[string]*string{}},
				{Name: "api-key", Tags: nil},
			},
			certs:       []VaultCertWithTags{},
			expectedLen: 0,
		},
		{
			name: "secrets with secret-object=false (explicit opt-out)",
			secrets: []VaultSecretWithTags{
				{Name: "db-password", Tags: map[string]*string{"secret-object": ptr("false")}},
			},
			certs:       []VaultCertWithTags{},
			expectedLen: 0,
		},
		{
			name:    "certs with cert-object=true tag",
			secrets: []VaultSecretWithTags{},
			certs: []VaultCertWithTags{
				{Name: "tls-cert", Tags: map[string]*string{"cert-object": ptr("true")}},
				{Name: "ca-cert", Tags: map[string]*string{"cert-object": ptr("true")}},
			},
			expectedLen: 2,
			checkFirst: &secretsstorev1.SecretObject{
				SecretName: "ca-cert",
				Type:       "kubernetes.io/tls",
				Data: []*secretsstorev1.SecretObjectData{
					{Key: "tls.key", ObjectName: "ca-cert"},
					{Key: "tls.crt", ObjectName: "ca-cert"},
				},
			},
		},
		{
			name:    "certs without cert-object tag (opted out)",
			secrets: []VaultSecretWithTags{},
			certs: []VaultCertWithTags{
				{Name: "tls-cert", Tags: map[string]*string{}},
			},
			expectedLen: 0,
		},
		{
			name: "mixed - some opted in, some opted out",
			secrets: []VaultSecretWithTags{
				{Name: "secret1", Tags: map[string]*string{"secret-object": ptr("true")}},
				{Name: "secret2", Tags: map[string]*string{}}, // No tag - opt out
			},
			certs: []VaultCertWithTags{
				{Name: "cert1", Tags: map[string]*string{"cert-object": ptr("true")}},
				{Name: "cert2", Tags: map[string]*string{}}, // No tag - opt out
			},
			expectedLen: 2, // Only secret1 and cert1
		},
		{
			name: "sorting verification",
			secrets: []VaultSecretWithTags{
				{Name: "zebra", Tags: map[string]*string{"secret-object": ptr("true")}},
				{Name: "apple", Tags: map[string]*string{"secret-object": ptr("true")}},
				{Name: "middle", Tags: map[string]*string{"secret-object": ptr("true")}},
			},
			certs:       []VaultCertWithTags{},
			expectedLen: 3,
			checkFirst: &secretsstorev1.SecretObject{
				SecretName: "apple",
				Type:       "Opaque",
			},
			checkLast: &secretsstorev1.SecretObject{
				SecretName: "zebra",
				Type:       "Opaque",
			},
		},
		{
			name: "tags with service and environment (should be ignored for secret-object decision)",
			secrets: []VaultSecretWithTags{
				{Name: "db-password", Tags: map[string]*string{
					"service":       ptr("web-api"),
					"environment":   ptr("production"),
					"secret-object": ptr("true"), // This is what matters
				}},
			},
			certs:       []VaultCertWithTags{},
			expectedLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateSecretObjectsFromVault(tt.secrets, tt.certs)

			assert.Equal(t, tt.expectedLen, len(result), "unexpected number of secret objects")

			if tt.expectedLen > 0 {
				// Verify sorting
				for i := 1; i < len(result); i++ {
					assert.True(t, result[i-1].SecretName <= result[i].SecretName,
						"secretObjects not sorted: %s should come before or equal to %s",
						result[i-1].SecretName, result[i].SecretName)
				}

				if tt.checkFirst != nil {
					assert.Equal(t, tt.checkFirst.SecretName, result[0].SecretName)
					assert.Equal(t, tt.checkFirst.Type, result[0].Type)
					if tt.checkFirst.Data != nil {
						assert.Equal(t, len(tt.checkFirst.Data), len(result[0].Data))
						for i, d := range tt.checkFirst.Data {
							assert.Equal(t, d.Key, result[0].Data[i].Key)
							assert.Equal(t, d.ObjectName, result[0].Data[i].ObjectName)
						}
					}
				}

				if tt.checkLast != nil {
					last := result[len(result)-1]
					assert.Equal(t, tt.checkLast.SecretName, last.SecretName)
					assert.Equal(t, tt.checkLast.Type, last.Type)
				}
			}

            // Verify correct types
            for _, so := range result {
                switch so.Type {
                case "Opaque":
                    assert.Len(t, so.Data, 1, "Opaque secrets should have 1 data entry")
                case "kubernetes.io/tls":
                    assert.Len(t, so.Data, 2, "TLS secrets should have 2 data entries (tls.key and tls.crt)")
                    assert.Equal(t, "tls.key", so.Data[0].Key)
                    assert.Equal(t, "tls.crt", so.Data[1].Key)
                }
            }
		})
	}
}

func TestHasTag(t *testing.T) {
	tests := []struct {
		name     string
		tags     map[string]*string
		key      string
		expected bool
	}{
		{
			name:     "nil tags",
			tags:     nil,
			key:      "secret-object",
			expected: false,
		},
		{
			name:     "empty tags",
			tags:     map[string]*string{},
			key:      "secret-object",
			expected: false,
		},
		{
			name:     "tag exists with true value",
			tags:     map[string]*string{"secret-object": ptr("true")},
			key:      "secret-object",
			expected: true,
		},
		{
			name:     "tag exists with false value",
			tags:     map[string]*string{"secret-object": ptr("false")},
			key:      "secret-object",
			expected: false,
		},
		{
			name:     "tag exists with empty value",
			tags:     map[string]*string{"secret-object": ptr("")},
			key:      "secret-object",
			expected: false,
		},
		{
			name:     "tag exists with random value",
			tags:     map[string]*string{"secret-object": ptr("yes")},
			key:      "secret-object",
			expected: false,
		},
		{
			name:     "tag key doesn't exist",
			tags:     map[string]*string{"other-tag": ptr("true")},
			key:      "secret-object",
			expected: false,
		},
		{
			name:     "tag exists but is nil",
			tags:     map[string]*string{"secret-object": nil},
			key:      "secret-object",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasTag(tt.tags, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCompareSecretObjects(t *testing.T) {
	tests := []struct {
		name      string
		existing  []*secretsstorev1.SecretObject
		generated []*secretsstorev1.SecretObject
		expected  bool // true = different, false = identical
	}{
		{
			name:      "both empty",
			existing:  []*secretsstorev1.SecretObject{},
			generated: []*secretsstorev1.SecretObject{},
			expected:  false,
		},
		{
			name:     "different lengths",
			existing: []*secretsstorev1.SecretObject{},
			generated: []*secretsstorev1.SecretObject{
				{SecretName: "secret1", Type: "Opaque"},
			},
			expected: true,
		},
		{
			name: "identical single object",
			existing: []*secretsstorev1.SecretObject{
				{
					SecretName: "secret1",
					Type:       "Opaque",
					Data: []*secretsstorev1.SecretObjectData{
						{Key: "key1", ObjectName: "obj1"},
					},
				},
			},
			generated: []*secretsstorev1.SecretObject{
				{
					SecretName: "secret1",
					Type:       "Opaque",
					Data: []*secretsstorev1.SecretObjectData{
						{Key: "key1", ObjectName: "obj1"},
					},
				},
			},
			expected: false,
		},
		{
			name: "different secret names",
			existing: []*secretsstorev1.SecretObject{
				{SecretName: "secret1", Type: "Opaque"},
			},
			generated: []*secretsstorev1.SecretObject{
				{SecretName: "secret2", Type: "Opaque"},
			},
			expected: true,
		},
		{
			name: "different types",
			existing: []*secretsstorev1.SecretObject{
				{SecretName: "secret1", Type: "Opaque"},
			},
			generated: []*secretsstorev1.SecretObject{
				{SecretName: "secret1", Type: "kubernetes.io/tls"},
			},
			expected: true,
		},
		{
			name: "different data content",
			existing: []*secretsstorev1.SecretObject{
				{
					SecretName: "secret1",
					Type:       "Opaque",
					Data: []*secretsstorev1.SecretObjectData{
						{Key: "key1", ObjectName: "obj1"},
					},
				},
			},
			generated: []*secretsstorev1.SecretObject{
				{
					SecretName: "secret1",
					Type:       "Opaque",
					Data: []*secretsstorev1.SecretObjectData{
						{Key: "key1", ObjectName: "obj2"},
					},
				},
			},
			expected: true,
		},
		{
			name: "identical multiple objects",
			existing: []*secretsstorev1.SecretObject{
				{SecretName: "secret1", Type: "Opaque"},
				{SecretName: "secret2", Type: "Opaque"},
			},
			generated: []*secretsstorev1.SecretObject{
				{SecretName: "secret1", Type: "Opaque"},
				{SecretName: "secret2", Type: "Opaque"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock SecretProviderClass
			spc := &secretsstorev1.SecretProviderClass{
				Spec: secretsstorev1.SecretProviderClassSpec{
					SecretObjects: tt.existing,
				},
			}

			result := CompareSecretObjects(spc, tt.generated)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSecretObjectsEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        *secretsstorev1.SecretObject
		b        *secretsstorev1.SecretObject
		expected bool
	}{
		{
			name: "identical simple objects",
			a: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
				Data:       []*secretsstorev1.SecretObjectData{},
				Labels:     map[string]string{},
			},
			b: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
				Data:       []*secretsstorev1.SecretObjectData{},
				Labels:     map[string]string{},
			},
			expected: true,
		},
		{
			name: "different names",
			a: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
			},
			b: &secretsstorev1.SecretObject{
				SecretName: "secret2",
				Type:       "Opaque",
			},
			expected: false,
		},
		{
			name: "different types",
			a: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
			},
			b: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "kubernetes.io/tls",
			},
			expected: false,
		},
		{
			name: "different data length",
			a: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
				Data: []*secretsstorev1.SecretObjectData{
					{Key: "key1", ObjectName: "obj1"},
				},
			},
			b: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
				Data: []*secretsstorev1.SecretObjectData{
					{Key: "key1", ObjectName: "obj1"},
					{Key: "key2", ObjectName: "obj2"},
				},
			},
			expected: false,
		},
		{
			name: "different labels",
			a: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
				Labels:     map[string]string{"env": "prod"},
			},
			b: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
				Labels:     map[string]string{"env": "dev"},
			},
			expected: false,
		},
		{
			name: "identical with labels and data",
			a: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
				Labels:     map[string]string{"env": "prod", "team": "platform"},
				Data: []*secretsstorev1.SecretObjectData{
					{Key: "key1", ObjectName: "obj1"},
					{Key: "key2", ObjectName: "obj2"},
				},
			},
			b: &secretsstorev1.SecretObject{
				SecretName: "secret1",
				Type:       "Opaque",
				Labels:     map[string]string{"env": "prod", "team": "platform"},
				Data: []*secretsstorev1.SecretObjectData{
					{Key: "key1", ObjectName: "obj1"},
					{Key: "key2", ObjectName: "obj2"},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := secretObjectsEqual(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}
