package azure

import (
	"strings"
)

// RejectionReason represents why a secret was rejected by tag filtering
type RejectionReason string

const (
	// ReasonIncluded indicates the secret passed all filters
	ReasonIncluded RejectionReason = "included"

	// ReasonNoServiceTag indicates the vault secret has no service tag
	ReasonNoServiceTag RejectionReason = "no_service_tag"

	// ReasonServiceMismatch indicates the service tag doesn't match SPC label
	ReasonServiceMismatch RejectionReason = "service_mismatch"

	// ReasonVaultEnvSPCNoEnv indicates vault has environment tag but SPC doesn't
	ReasonVaultEnvSPCNoEnv RejectionReason = "vault_env_spc_no_env"

	// ReasonEnvMismatch indicates the environment tag doesn't match SPC label
	ReasonEnvMismatch RejectionReason = "environment_mismatch"
)

// FilterResult contains the result of tag filtering evaluation
type FilterResult struct {
	// Include indicates whether the secret should be included
	Include bool
	// Reason provides the reason for inclusion or rejection
	Reason RejectionReason
}

// TagFilterConfig contains the configuration for tag filtering
type TagFilterConfig struct {
	// RespectTags indicates whether tag filtering is enabled
	RespectTags bool
	// ServiceLabel is the service label from the SPC
	ServiceLabel string
	// EnvironmentLabel is the environment label from the SPC (optional)
	EnvironmentLabel string
}

// normalizeTag normalizes a tag value for comparison
// - Converts to lowercase
// - Trims whitespace
func normalizeTag(tag string) string {
	return strings.ToLower(strings.TrimSpace(tag))
}

// getTagValue safely extracts a tag value from a map, returning empty string if not found
func getTagValue(tags map[string]*string, key string) string {
	if tags == nil {
		return ""
	}
	if value, exists := tags[key]; exists && value != nil {
		return *value
	}
	return ""
}

// MatchesTags evaluates whether a vault secret/certificate should be included
// based on its tags and the SecretProviderClass configuration.
//
// Decision tree:
// 1. If respect-tags is disabled → Include (current behavior)
// 2. If respect-tags is enabled:
//    a. If vault has no service tag → Reject
//    b. If service tag doesn't match SPC → Reject
//    c. If service matches and vault has no environment tag → Include
//    d. If vault has environment tag but SPC doesn't → Reject
//    e. If environment tags don't match → Reject
//    f. If both service and environment match → Include
func MatchesTags(vaultTags map[string]*string, config TagFilterConfig) FilterResult {
	// Path 1: respect-tags disabled → Include all
	if !config.RespectTags {
		return FilterResult{
			Include: true,
			Reason:  ReasonIncluded,
		}
	}

	// Extract and normalize tag values
	vaultService := normalizeTag(getTagValue(vaultTags, "service"))
	vaultEnvironment := normalizeTag(getTagValue(vaultTags, "environment"))
	spcService := normalizeTag(config.ServiceLabel)
	spcEnvironment := normalizeTag(config.EnvironmentLabel)

	// Path 2: No service tag in vault → Reject (strict mode)
	if vaultService == "" {
		return FilterResult{
			Include: false,
			Reason:  ReasonNoServiceTag,
		}
	}

	// Path 3: Service tag mismatch → Reject
	if vaultService != spcService {
		return FilterResult{
			Include: false,
			Reason:  ReasonServiceMismatch,
		}
	}

	// At this point: service matches

	// Path 4: Service matches, no environment tag in vault → Include
	// (environment-agnostic secret)
	if vaultEnvironment == "" {
		return FilterResult{
			Include: true,
			Reason:  ReasonIncluded,
		}
	}

	// At this point: vault has environment tag

	// Path 5: Vault has environment tag but SPC doesn't → Reject
	// (prevents production secrets syncing to non-environment-specific SPCs)
	if spcEnvironment == "" {
		return FilterResult{
			Include: false,
			Reason:  ReasonVaultEnvSPCNoEnv,
		}
	}

	// Path 6: Environment tag mismatch → Reject
	if vaultEnvironment != spcEnvironment {
		return FilterResult{
			Include: false,
			Reason:  ReasonEnvMismatch,
		}
	}

	// Path 7: Both service and environment match → Include
	return FilterResult{
		Include: true,
		Reason:  ReasonIncluded,
	}
}
