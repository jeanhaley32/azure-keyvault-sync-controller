package azure

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// TagAnnotationPrefix is the prefix for Azure Key Vault tags that should become Kubernetes annotations
	TagAnnotationPrefix = "k8s-annotation."

	// TagLabelPrefix is the prefix for Azure Key Vault tags that should become Kubernetes labels
	TagLabelPrefix = "k8s-label."

	// SPCAnnotationPrefix is the prefix used in SecretProviderClass metadata for per-secret annotations
	SPCAnnotationPrefix = "secret-metadata.azure-keyvault-sync.io/"

	// SPCLabelPrefix is the prefix used in SecretProviderClass metadata for per-secret labels
	SPCLabelPrefix = "secret-label.azure-keyvault-sync.io/"
)

// TransformTagsToSPCAnnotations converts Azure Key Vault secret tags to SecretProviderClass annotations.
// Only tags with the k8s-annotation. prefix are processed.
//
// Transformation format:
//   Azure Tag: k8s-annotation.reflector/allowed = "true"
//   SPC Annotation: secret-metadata.azure-keyvault-sync.io/<secretName>.reflector/allowed: "true"
//
// Parameters:
//   - secretName: The name of the vault secret (used in annotation key)
//   - tags: Map of Azure Key Vault tags
//
// Returns:
//   - Map of SPC annotation keys to values
func TransformTagsToSPCAnnotations(secretName string, tags map[string]*string) map[string]string {
	annotations := make(map[string]string)

	for tagKey, tagValue := range tags {
		// Only process tags with the k8s-annotation. prefix
		if !strings.HasPrefix(tagKey, TagAnnotationPrefix) {
			continue
		}

		// Skip nil values
		if tagValue == nil {
			continue
		}

		// Strip the k8s-annotation. prefix
		annotationKey := strings.TrimPrefix(tagKey, TagAnnotationPrefix)

		// Skip empty annotation keys
		if annotationKey == "" {
			continue
		}

		// Build the SPC annotation key
		spcKey := fmt.Sprintf("%s%s.%s", SPCAnnotationPrefix, secretName, annotationKey)
		annotations[spcKey] = *tagValue
	}

	return annotations
}

// ExtractAnnotationsForSecret extracts annotations for a specific secret from SPC metadata.
// This reverses the transformation done by TransformTagsToSPCAnnotations.
//
// Format:
//   SPC Annotation: secret-metadata.azure-keyvault-sync.io/<secretName>.reflector/allowed: "true"
//   Secret Annotation: reflector/allowed: "true"
//
// Parameters:
//   - spcAnnotations: Annotations from SecretProviderClass metadata
//   - secretName: The name of the secret to extract annotations for
//
// Returns:
//   - Map of annotation keys to values that should be applied to the Kubernetes Secret
func ExtractAnnotationsForSecret(spcAnnotations map[string]string, secretName string) map[string]string {
	annotations := make(map[string]string)

	// Build the prefix to search for
	prefix := fmt.Sprintf("%s%s.", SPCAnnotationPrefix, secretName)

	for spcKey, value := range spcAnnotations {
		// Only process annotations with the expected prefix
		if !strings.HasPrefix(spcKey, prefix) {
			continue
		}

		// Extract the annotation key by removing the prefix
		annotationKey := strings.TrimPrefix(spcKey, prefix)

		// Skip empty keys
		if annotationKey == "" {
			continue
		}

		annotations[annotationKey] = value
	}

	return annotations
}

// TransformTagsToSPCLabels converts Azure Key Vault secret tags to SecretProviderClass annotations for labels.
// Only tags with the k8s-label. prefix are processed.
// All labels for a secret are stored in a single annotation as JSON.
//
// Transformation format:
//   Azure Tags:
//     k8s-label.app = "myapp"
//     k8s-label.team = "platform"
//   SPC Annotation:
//     secret-label.azure-keyvault-sync.io/<secretName>: '{"app":"myapp","team":"platform"}'
//
// Parameters:
//   - secretName: The name of the vault secret (used in annotation key)
//   - tags: Map of Azure Key Vault tags
//
// Returns:
//   - Map of SPC annotation keys to JSON-encoded label values
func TransformTagsToSPCLabels(secretName string, tags map[string]*string) map[string]string {
	labelsMap := make(map[string]string)

	// Collect all k8s-label. tags
	for tagKey, tagValue := range tags {
		// Only process tags with the k8s-label. prefix
		if !strings.HasPrefix(tagKey, TagLabelPrefix) {
			continue
		}

		// Skip nil values
		if tagValue == nil {
			continue
		}

		// Strip the k8s-label. prefix to get the actual label key
		labelKey := strings.TrimPrefix(tagKey, TagLabelPrefix)

		// Skip empty label keys
		if labelKey == "" {
			continue
		}

		labelsMap[labelKey] = *tagValue
	}

	// If no labels found, return empty map
	if len(labelsMap) == 0 {
		return map[string]string{}
	}

	// JSON-encode the labels map
	jsonBytes, err := json.Marshal(labelsMap)
	if err != nil {
		// Should never happen with a simple string map, but handle gracefully
		return map[string]string{}
	}

	// Return single annotation with JSON value
	spcKey := fmt.Sprintf("%s%s", SPCLabelPrefix, secretName)
	return map[string]string{
		spcKey: string(jsonBytes),
	}
}

// ExtractLabelsForSecret extracts labels for a specific secret from SPC metadata.
// This reverses the transformation done by TransformTagsToSPCLabels by decoding the JSON value.
//
// Format:
//   SPC Annotation: secret-label.azure-keyvault-sync.io/<secretName>: '{"app":"myapp","team":"platform"}'
//   Secret Labels: app: "myapp", team: "platform"
//
// Parameters:
//   - spcAnnotations: Annotations from SecretProviderClass metadata
//   - secretName: The name of the secret to extract labels for
//
// Returns:
//   - Map of label keys to values that should be applied to the Kubernetes Secret
func ExtractLabelsForSecret(spcAnnotations map[string]string, secretName string) map[string]string {
	// Build the annotation key for this secret
	spcKey := fmt.Sprintf("%s%s", SPCLabelPrefix, secretName)

	// Get the JSON value
	jsonValue, exists := spcAnnotations[spcKey]
	if !exists || jsonValue == "" {
		return map[string]string{}
	}

	// Decode the JSON
	var labels map[string]string
	if err := json.Unmarshal([]byte(jsonValue), &labels); err != nil {
		// If JSON is malformed, return empty map
		return map[string]string{}
	}

	return labels
}
