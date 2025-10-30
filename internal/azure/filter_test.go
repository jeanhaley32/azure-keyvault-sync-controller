package azure

import (
	"testing"
)

// ptr is a helper function to create a pointer to a string
func ptr(s string) *string {
	return &s
}

func TestMatchesTags(t *testing.T) {
	tests := []struct {
		name          string
		vaultTags     map[string]*string
		config        TagFilterConfig
		expectInclude bool
		expectReason  RejectionReason
	}{
		// Path 1: respect-tags disabled → Include all
		{
			name:      "RespectTagsDisabled_NoTags",
			vaultTags: nil,
			config: TagFilterConfig{
				RespectTags:      false,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},
		{
			name: "RespectTagsDisabled_WithTags",
			vaultTags: map[string]*string{
				"service":     ptr("mobile-api"),
				"environment": ptr("staging"),
			},
			config: TagFilterConfig{
				RespectTags:      false,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},

		// Path 2: No service tag in vault → Reject
		{
			name:      "NoServiceTag_NilTags",
			vaultTags: nil,
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: false,
			expectReason:  ReasonNoServiceTag,
		},
		{
			name:      "NoServiceTag_EmptyMap",
			vaultTags: map[string]*string{},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: false,
			expectReason:  ReasonNoServiceTag,
		},
		{
			name: "NoServiceTag_OnlyEnvironment",
			vaultTags: map[string]*string{
				"environment": ptr("production"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: false,
			expectReason:  ReasonNoServiceTag,
		},
		{
			name: "NoServiceTag_EmptyString",
			vaultTags: map[string]*string{
				"service": ptr(""),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: false,
			expectReason:  ReasonNoServiceTag,
		},

		// Path 3: Service mismatch → Reject
		{
			name: "ServiceMismatch",
			vaultTags: map[string]*string{
				"service": ptr("mobile-api"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "",
			},
			expectInclude: false,
			expectReason:  ReasonServiceMismatch,
		},
		{
			name: "ServiceMismatch_WithEnvironment",
			vaultTags: map[string]*string{
				"service":     ptr("mobile-api"),
				"environment": ptr("production"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: false,
			expectReason:  ReasonServiceMismatch,
		},

		// Path 4: Service matches, no environment tag → Include
		{
			name: "ServiceMatch_NoEnvironmentTag",
			vaultTags: map[string]*string{
				"service": ptr("web-api"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},
		{
			name: "ServiceMatch_NoEnvironmentTag_SPCHasEnvironment",
			vaultTags: map[string]*string{
				"service": ptr("web-api"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},
		{
			name: "ServiceMatch_EmptyEnvironmentTag",
			vaultTags: map[string]*string{
				"service":     ptr("web-api"),
				"environment": ptr(""),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},

		// Path 5: Vault has environment, SPC doesn't → Reject
		{
			name: "VaultHasEnv_SPCDoesNot",
			vaultTags: map[string]*string{
				"service":     ptr("web-api"),
				"environment": ptr("production"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "",
			},
			expectInclude: false,
			expectReason:  ReasonVaultEnvSPCNoEnv,
		},

		// Path 6: Environment mismatch → Reject
		{
			name: "EnvironmentMismatch",
			vaultTags: map[string]*string{
				"service":     ptr("web-api"),
				"environment": ptr("production"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "staging",
			},
			expectInclude: false,
			expectReason:  ReasonEnvMismatch,
		},

		// Path 7: Both service and environment match → Include
		{
			name: "BothMatch",
			vaultTags: map[string]*string{
				"service":     ptr("web-api"),
				"environment": ptr("production"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},

		// Case sensitivity tests
		{
			name: "CaseInsensitive_Service",
			vaultTags: map[string]*string{
				"service": ptr("Web-API"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},
		{
			name: "CaseInsensitive_Environment",
			vaultTags: map[string]*string{
				"service":     ptr("web-api"),
				"environment": ptr("PRODUCTION"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},
		{
			name: "CaseInsensitive_Both",
			vaultTags: map[string]*string{
				"service":     ptr("WEB-API"),
				"environment": ptr("Production"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "Web-Api",
				EnvironmentLabel: "PRODUCTION",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},

		// Whitespace handling
		{
			name: "Whitespace_Service",
			vaultTags: map[string]*string{
				"service": ptr("  web-api  "),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},
		{
			name: "Whitespace_Environment",
			vaultTags: map[string]*string{
				"service":     ptr("web-api"),
				"environment": ptr(" production "),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},

		// Extra tags in vault (should be ignored)
		{
			name: "ExtraTags_Ignored",
			vaultTags: map[string]*string{
				"service":     ptr("web-api"),
				"environment": ptr("production"),
				"team":        ptr("platform"),
				"owner":       ptr("john"),
				"cost-center": ptr("engineering"),
			},
			config: TagFilterConfig{
				RespectTags:      true,
				ServiceLabel:     "web-api",
				EnvironmentLabel: "production",
			},
			expectInclude: true,
			expectReason:  ReasonIncluded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchesTags(tt.vaultTags, tt.config)

			if result.Include != tt.expectInclude {
				t.Errorf("Include = %v, want %v", result.Include, tt.expectInclude)
			}

			if result.Reason != tt.expectReason {
				t.Errorf("Reason = %v, want %v", result.Reason, tt.expectReason)
			}
		})
	}
}

func TestNormalizeTag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "AlreadyLowercase",
			input:    "web-api",
			expected: "web-api",
		},
		{
			name:     "UpperCase",
			input:    "WEB-API",
			expected: "web-api",
		},
		{
			name:     "MixedCase",
			input:    "Web-API",
			expected: "web-api",
		},
		{
			name:     "WithLeadingWhitespace",
			input:    "  web-api",
			expected: "web-api",
		},
		{
			name:     "WithTrailingWhitespace",
			input:    "web-api  ",
			expected: "web-api",
		},
		{
			name:     "WithBothWhitespace",
			input:    "  web-api  ",
			expected: "web-api",
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
		{
			name:     "OnlyWhitespace",
			input:    "   ",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeTag(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeTag(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetTagValue(t *testing.T) {
	tests := []struct {
		name     string
		tags     map[string]*string
		key      string
		expected string
	}{
		{
			name:     "NilMap",
			tags:     nil,
			key:      "service",
			expected: "",
		},
		{
			name:     "EmptyMap",
			tags:     map[string]*string{},
			key:      "service",
			expected: "",
		},
		{
			name: "KeyExists",
			tags: map[string]*string{
				"service": ptr("web-api"),
			},
			key:      "service",
			expected: "web-api",
		},
		{
			name: "KeyDoesNotExist",
			tags: map[string]*string{
				"service": ptr("web-api"),
			},
			key:      "environment",
			expected: "",
		},
		{
			name: "NilValue",
			tags: map[string]*string{
				"service": nil,
			},
			key:      "service",
			expected: "",
		},
		{
			name: "EmptyStringValue",
			tags: map[string]*string{
				"service": ptr(""),
			},
			key:      "service",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getTagValue(tt.tags, tt.key)
			if result != tt.expected {
				t.Errorf("getTagValue(%v, %q) = %q, want %q", tt.tags, tt.key, result, tt.expected)
			}
		})
	}
}
