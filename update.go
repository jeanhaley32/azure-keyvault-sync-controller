package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// SecretObject represents a Kubernetes Secret to be created from vault contents
type SecretObject struct {
	SecretName string              `json:"secretName" yaml:"secretName"`
	Type       string              `json:"type" yaml:"type"`
	Data       []SecretObjectData  `json:"data" yaml:"data"`
}

// SecretObjectData represents the data mapping for a Kubernetes Secret
type SecretObjectData struct {
	Key        string `json:"key" yaml:"key"`
	ObjectName string `json:"objectName" yaml:"objectName"`
}

// GenerateObjectsFromVault converts discovered vault items to VaultObject structs
// Vault is the source of truth - no merging with existing objects
func GenerateObjectsFromVault(secrets []string, certs []string) []VaultObject {
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

	// Sort by objectName for consistent output
	sort.Slice(objects, func(i, j int) bool {
		return objects[i].ObjectName < objects[j].ObjectName
	})

	log.Printf("Generated %d objects from vault (%d secrets, %d certs)",
		len(objects), len(secrets), len(certs))

	return objects
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
	secretObjects interface{},
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

	// Handle secretObjects field
	if secretObjects != nil {
		// Check if this is a removal marker
		if secretObjectsStr, ok := secretObjects.(string); ok && secretObjectsStr == "REMOVE_FIELD" {
			// Remove field using JSON Patch "remove" operation
			patch = append(patch, map[string]interface{}{
				"op":   "remove",
				"path": "/spec/secretObjects",
			})
			log.Printf("Removing secretObjects field from %s/%s", namespace, name)
		} else {
			// Replace field with new value
			patch = append(patch, map[string]interface{}{
				"op":    "replace",
				"path":  "/spec/secretObjects",
				"value": secretObjects,
			})
		}
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

// GenerateSecretObjectsFromVault creates SecretObject entries for vault secrets and certificates
// Vault is the source of truth - no merging with existing secretObjects
func GenerateSecretObjectsFromVault(secrets []string, certs []string, enableSecrets bool, enableCerts bool) []SecretObject {
	var secretObjects []SecretObject

	// Add secrets (type: Opaque) if enabled
	if enableSecrets {
		for _, secretName := range secrets {
			secretObjects = append(secretObjects, SecretObject{
				SecretName: secretName,
				Type:       "Opaque",
				Data: []SecretObjectData{
					{
						Key:        secretName,
						ObjectName: secretName,
					},
				},
			})
		}
		log.Printf("Generated %d Opaque secretObjects for secrets", len(secrets))
	}

	// Add certificates (type: kubernetes.io/tls) if enabled
	if enableCerts {
		for _, certName := range certs {
			secretObjects = append(secretObjects, SecretObject{
				SecretName: certName,
				Type:       "kubernetes.io/tls",
				Data: []SecretObjectData{
					{
						Key:        "tls.key",
						ObjectName: certName,
					},
					{
						Key:        "tls.crt",
						ObjectName: certName,
					},
				},
			})
		}
		log.Printf("Generated %d TLS secretObjects for certificates", len(certs))
	}

	// Sort by secretName for consistent output
	sort.Slice(secretObjects, func(i, j int) bool {
		return secretObjects[i].SecretName < secretObjects[j].SecretName
	})

	log.Printf("Generated %d total secretObjects", len(secretObjects))
	return secretObjects
}
