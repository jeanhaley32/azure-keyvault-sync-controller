package main

import (
	"strings"
	"testing"
)

func TestFormatObjectsYAML(t *testing.T) {
	tests := []struct {
		name     string
		objects  []VaultObject
		expected string
	}{
		{
			name:     "empty objects",
			objects:  []VaultObject{},
			expected: "",
		},
		{
			name: "single secret without version",
			objects: []VaultObject{
				{
					ObjectName:    "database-password",
					ObjectType:    "secret",
					ObjectVersion: "",
				},
			},
			expected: `array:
  - |
    objectName: database-password
    objectType: secret
    objectVersion: ""
`,
		},
		{
			name: "single secret with version",
			objects: []VaultObject{
				{
					ObjectName:    "api-key",
					ObjectType:    "secret",
					ObjectVersion: "abc123",
				},
			},
			expected: `array:
  - |
    objectName: api-key
    objectType: secret
    objectVersion: abc123
`,
		},
		{
			name: "single certificate",
			objects: []VaultObject{
				{
					ObjectName:    "tls-cert",
					ObjectType:    "cert",
					ObjectVersion: "",
				},
			},
			expected: `array:
  - |
    objectName: tls-cert
    objectType: cert
    objectVersion: ""
`,
		},
		{
			name: "multiple secrets and certificates",
			objects: []VaultObject{
				{
					ObjectName:    "api-key",
					ObjectType:    "secret",
					ObjectVersion: "",
				},
				{
					ObjectName:    "database-password",
					ObjectType:    "secret",
					ObjectVersion: "v1",
				},
				{
					ObjectName:    "tls-cert",
					ObjectType:    "cert",
					ObjectVersion: "",
				},
			},
			expected: `array:
  - |
    objectName: api-key
    objectType: secret
    objectVersion: ""
  - |
    objectName: database-password
    objectType: secret
    objectVersion: v1
  - |
    objectName: tls-cert
    objectType: cert
    objectVersion: ""
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FormatObjectsYAML(tt.objects)
			if err != nil {
				t.Fatalf("FormatObjectsYAML() error = %v", err)
			}

			if result != tt.expected {
				t.Errorf("FormatObjectsYAML() mismatch:\nGot:\n%s\nExpected:\n%s", result, tt.expected)
				// Show differences character by character
				t.Logf("Got length: %d, Expected length: %d", len(result), len(tt.expected))
				for i := 0; i < len(result) && i < len(tt.expected); i++ {
					if result[i] != tt.expected[i] {
						t.Logf("First difference at position %d: got %q, expected %q", i, result[i], tt.expected[i])
						break
					}
				}
			}
		})
	}
}

func TestFormatObjectsYAML_LiteralBlockScalarFormat(t *testing.T) {
	// Test that output contains literal block scalar indicators (|)
	objects := []VaultObject{
		{ObjectName: "test-secret", ObjectType: "secret", ObjectVersion: ""},
	}

	result, err := FormatObjectsYAML(objects)
	if err != nil {
		t.Fatalf("FormatObjectsYAML() error = %v", err)
	}

	// Verify the literal block scalar indicator is present
	if !strings.Contains(result, "  - |") {
		t.Errorf("FormatObjectsYAML() missing literal block scalar indicator '|': %s", result)
	}

	// Verify proper indentation (4 spaces for object properties)
	if !strings.Contains(result, "    objectName:") {
		t.Errorf("FormatObjectsYAML() missing proper indentation (4 spaces): %s", result)
	}

	// Verify array structure starts correctly
	if !strings.HasPrefix(result, "array:\n") {
		t.Errorf("FormatObjectsYAML() should start with 'array:\\n', got: %s", result)
	}
}

func TestFormatObjectsYAML_NoNestedMaps(t *testing.T) {
	// Verify that the output does NOT contain nested map structures
	// (which would cause CSI driver to fail with "cannot unmarshal !!map into string")
	objects := []VaultObject{
		{ObjectName: "test-secret", ObjectType: "secret", ObjectVersion: ""},
	}

	result, err := FormatObjectsYAML(objects)
	if err != nil {
		t.Fatalf("FormatObjectsYAML() error = %v", err)
	}

	// The incorrect format would have "- objectName:" without the pipe
	incorrectFormat := "  - objectName:"
	if strings.Contains(result, incorrectFormat) {
		t.Errorf("FormatObjectsYAML() contains nested map format (should use literal block scalar): %s", result)
	}
}

func TestGenerateObjectsFromVault(t *testing.T) {
	tests := []struct {
		name     string
		secrets  []string
		certs    []string
		expected []VaultObject
	}{
		{
			name:     "no secrets or certificates",
			secrets:  []string{},
			certs:    []string{},
			expected: []VaultObject{},
		},
		{
			name:    "only secrets",
			secrets: []string{"secret1", "secret2"},
			certs:   []string{},
			expected: []VaultObject{
				{ObjectName: "secret1", ObjectType: "secret", ObjectVersion: ""},
				{ObjectName: "secret2", ObjectType: "secret", ObjectVersion: ""},
			},
		},
		{
			name:    "only certificates",
			secrets: []string{},
			certs:   []string{"cert1", "cert2"},
			expected: []VaultObject{
				{ObjectName: "cert1", ObjectType: "cert", ObjectVersion: ""},
				{ObjectName: "cert2", ObjectType: "cert", ObjectVersion: ""},
			},
		},
		{
			name:    "mixed secrets and certificates",
			secrets: []string{"database-password", "api-key"},
			certs:   []string{"tls-cert"},
			expected: []VaultObject{
				{ObjectName: "api-key", ObjectType: "secret", ObjectVersion: ""},
				{ObjectName: "database-password", ObjectType: "secret", ObjectVersion: ""},
				{ObjectName: "tls-cert", ObjectType: "cert", ObjectVersion: ""},
			},
		},
		{
			name:    "sorted output",
			secrets: []string{"zebra-secret", "alpha-secret"},
			certs:   []string{"beta-cert"},
			expected: []VaultObject{
				{ObjectName: "alpha-secret", ObjectType: "secret", ObjectVersion: ""},
				{ObjectName: "beta-cert", ObjectType: "cert", ObjectVersion: ""},
				{ObjectName: "zebra-secret", ObjectType: "secret", ObjectVersion: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateObjectsFromVault(tt.secrets, tt.certs)

			if len(result) != len(tt.expected) {
				t.Fatalf("GenerateObjectsFromVault() length mismatch: got %d, expected %d", len(result), len(tt.expected))
			}

			for i, obj := range result {
				if obj.ObjectName != tt.expected[i].ObjectName ||
					obj.ObjectType != tt.expected[i].ObjectType ||
					obj.ObjectVersion != tt.expected[i].ObjectVersion {
					t.Errorf("GenerateObjectsFromVault() object mismatch at index %d:\ngot: %+v\nexpected: %+v",
						i, obj, tt.expected[i])
				}
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
			name:     "no changes",
			current:  "array:\n  - |\n    objectName: test\n",
			new:      "array:\n  - |\n    objectName: test\n",
			expected: false,
		},
		{
			name:     "whitespace differences ignored",
			current:  "  array:\n  - |\n    objectName: test\n  ",
			new:      "array:\n  - |\n    objectName: test\n",
			expected: false,
		},
		{
			name:     "content changed",
			current:  "array:\n  - |\n    objectName: old\n",
			new:      "array:\n  - |\n    objectName: new\n",
			expected: true,
		},
		{
			name:     "empty to non-empty",
			current:  "",
			new:      "array:\n  - |\n    objectName: test\n",
			expected: true,
		},
		{
			name:     "both empty",
			current:  "",
			new:      "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectChanges(tt.current, tt.new)
			if result != tt.expected {
				t.Errorf("DetectChanges() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
