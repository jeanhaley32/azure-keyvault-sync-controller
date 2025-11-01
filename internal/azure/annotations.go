package azure

import (
	"fmt"
	"strings"
)

const (
	// TagAnnotationPrefix is the prefix for Azure Key Vault tags that should become Kubernetes annotations
	TagAnnotationPrefix = "k8s-annotation."

	// SPCAnnotationPrefix is the prefix used in SecretProviderClass metadata for per-secret annotations
	SPCAnnotationPrefix = "secret-metadata.azure-keyvault-sync.io/"
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
