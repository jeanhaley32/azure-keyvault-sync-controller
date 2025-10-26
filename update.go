package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"gopkg.in/yaml.v3"
)

// VaultObject represents a single object in the SecretProviderClass objects array
type VaultObject struct {
	ObjectName    string `yaml:"objectName"`
	ObjectType    string `yaml:"objectType"` // "secret" or "cert"
	ObjectVersion string `yaml:"objectVersion,omitempty"`
}

// ObjectsSpec represents the full objects structure for Azure provider
type ObjectsSpec struct {
	Array []VaultObject `yaml:"array"`
}

// ParseExistingObjects extracts and parses current objects from SecretProviderClass
func ParseExistingObjects(obj *unstructured.Unstructured) ([]VaultObject, error) {
	// Get spec.parameters.objects string
	objectsStr, found, err := unstructured.NestedString(obj.Object, "spec", "parameters", "objects")
	if err != nil {
		return nil, fmt.Errorf("error accessing spec.parameters.objects: %w", err)
	}

	// If objects field is empty or missing, return empty slice
	if !found || strings.TrimSpace(objectsStr) == "" {
		log.Printf("No existing objects found in %s/%s", obj.GetNamespace(), obj.GetName())
		return []VaultObject{}, nil
	}

	// Parse YAML
	var spec ObjectsSpec
	err = yaml.Unmarshal([]byte(objectsStr), &spec)
	if err != nil {
		return nil, fmt.Errorf("error parsing existing objects YAML: %w", err)
	}

	log.Printf("Parsed %d existing objects from %s/%s",
		len(spec.Array), obj.GetNamespace(), obj.GetName())

	return spec.Array, nil
}

// GenerateObjectsArray converts discovered vault items to VaultObject structs
func GenerateObjectsArray(secrets []string, certs []string) []VaultObject {
	var objects []VaultObject

	// Add secrets
	for _, secretName := range secrets {
		objects = append(objects, VaultObject{
			ObjectName:    secretName,
			ObjectType:    "secret",
			ObjectVersion: "", // Empty = latest version
		})
	}

	// Add certificates
	for _, certName := range certs {
		objects = append(objects, VaultObject{
			ObjectName:    certName,
			ObjectType:    "cert",
			ObjectVersion: "", // Empty = latest version
		})
	}

	log.Printf("Generated %d objects from vault (%d secrets, %d certs)",
		len(objects), len(secrets), len(certs))

	return objects
}

// MergeObjects combines existing and discovered objects without duplicates
func MergeObjects(existing []VaultObject, discovered []VaultObject) []VaultObject {
	// Use map for deduplication (key = objectName)
	objectMap := make(map[string]VaultObject)

	// Add existing objects first (they take precedence)
	for _, obj := range existing {
		objectMap[obj.ObjectName] = obj
	}

	// Add discovered objects (only if not already present)
	for _, obj := range discovered {
		if _, exists := objectMap[obj.ObjectName]; !exists {
			objectMap[obj.ObjectName] = obj
		}
	}

	// Convert map back to slice
	var merged []VaultObject
	for _, obj := range objectMap {
		merged = append(merged, obj)
	}

	// Sort by objectName for consistent output
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].ObjectName < merged[j].ObjectName
	})

	log.Printf("Merged objects: %d existing + %d discovered = %d total",
		len(existing), len(discovered), len(merged))

	return merged
}

// FormatObjectsYAML converts VaultObject slice to Azure provider YAML format
func FormatObjectsYAML(objects []VaultObject) (string, error) {
	if len(objects) == 0 {
		return "", nil
	}

	// Create ObjectsSpec
	spec := ObjectsSpec{
		Array: objects,
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(&spec)
	if err != nil {
		return "", fmt.Errorf("error marshaling objects to YAML: %w", err)
	}

	return string(yamlBytes), nil
}

// DetectChanges determines if objects have changed
func DetectChanges(current string, new string) bool {
	// Normalize whitespace for comparison
	currentNorm := strings.TrimSpace(current)
	newNorm := strings.TrimSpace(new)

	changed := currentNorm != newNorm

	if changed {
		log.Printf("Change detected: current length=%d, new length=%d",
			len(currentNorm), len(newNorm))
	}

	return changed
}

// PatchSecretProviderClass applies JSON Patch to update SecretProviderClass
func PatchSecretProviderClass(
	ctx context.Context,
	client dynamic.Interface,
	namespace string,
	name string,
	gvr schema.GroupVersionResource,
	objectsYAML string,
	timestamp string,
) error {
	log.Printf("Patching SecretProviderClass %s/%s", namespace, name)

	// Create JSON Patch payload
	// Note: Use ~1 to escape / in annotation key (JSON Pointer RFC 6901)
	patch := []map[string]interface{}{
		{
			"op":    "replace",
			"path":  "/spec/parameters/objects",
			"value": objectsYAML,
		},
		{
			"op":    "add", // "add" works for both create and update
			"path":  "/metadata/annotations/azure-keyvault-sync~1last-sync",
			"value": timestamp,
		},
	}

	// Marshal patch to JSON
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("error marshaling patch: %w", err)
	}

	// Log patch for debugging
	log.Printf("DEBUG: Applying JSON Patch to %s/%s: %s", namespace, name, string(patchBytes))

	// Apply the patch
	_, err = client.Resource(gvr).Namespace(namespace).Patch(
		ctx,
		name,
		types.JSONPatchType,
		patchBytes,
		metav1.PatchOptions{},
	)

	if err != nil {
		return fmt.Errorf("error applying patch: %w", err)
	}

	log.Printf("Successfully patched %s/%s", namespace, name)
	return nil
}
