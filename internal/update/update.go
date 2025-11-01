package update

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	spcclient "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned"
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

	slog.Info("Generated objects from vault",
		"totalObjects", len(objects), "secrets", len(secrets), "certificates", len(certs))

	return objects
}

// FormatObjectsYAML converts VaultObject slice to Azure provider YAML format
// Returns YAML with literal block scalar format required by Azure CSI driver:
// array:
//   - |
//     objectName: secret-name
//     objectType: secret
//     objectVersion: ""
func FormatObjectsYAML(objects []VaultObject) (string, error) {
	if len(objects) == 0 {
		return "", nil
	}

	// Manually construct YAML with literal block scalars (|)
	// The Azure CSI driver requires each array element to be a literal string,
	// not a nested map structure
	var sb strings.Builder
	sb.WriteString("array:\n")

	for _, obj := range objects {
		// Start literal block scalar for this array element
		sb.WriteString("  - |\n")

		// Write object properties with 4-space indentation
		sb.WriteString(fmt.Sprintf("    objectName: %s\n", obj.ObjectName))
		sb.WriteString(fmt.Sprintf("    objectType: %s\n", obj.ObjectType))

		// Always include objectVersion field (empty string for latest version)
		if obj.ObjectVersion != "" {
			sb.WriteString(fmt.Sprintf("    objectVersion: %s\n", obj.ObjectVersion))
		} else {
			sb.WriteString("    objectVersion: \"\"\n")
		}
	}

	return sb.String(), nil
}

// DetectChanges determines if objects have changed
func DetectChanges(current string, new string) bool {
	// Normalize whitespace for comparison
	currentNorm := strings.TrimSpace(current)
	newNorm := strings.TrimSpace(new)

	changed := currentNorm != newNorm

	if changed {
		slog.Debug("Objects changed",
			"currentLength", len(currentNorm), "newLength", len(newNorm))
	} else {
		slog.Debug("Objects unchanged",
			"length", len(currentNorm))
	}

	return changed
}

// PatchSecretProviderClass applies JSON Patch to update SecretProviderClass
func PatchSecretProviderClass(
	ctx context.Context,
	client spcclient.Interface,
	namespace string,
	name string,
	objectsYAML string,
	secretObjects interface{},
	annotations map[string]string,
	timestamp string,
) error {
	slog.Info("Patching SecretProviderClass", "namespace", namespace, "name", name)

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

	// Add per-secret annotations from vault tags
	for key, value := range annotations {
		// Escape forward slashes in annotation keys (JSON Pointer RFC 6901)
		escapedKey := strings.ReplaceAll(key, "/", "~1")
		escapedKey = strings.ReplaceAll(escapedKey, "~", "~0")
		patch = append(patch, map[string]interface{}{
			"op":    "add",
			"path":  "/metadata/annotations/" + escapedKey,
			"value": value,
		})
	}

	slog.Info("Adding annotations to SPC",
		"namespace", namespace, "name", name, "annotationCount", len(annotations))

	// Handle secretObjects field
	if secretObjects != nil {
		// Check if this is a removal marker
		if secretObjectsStr, ok := secretObjects.(string); ok && secretObjectsStr == "REMOVE_FIELD" {
			// Remove field using JSON Patch "remove" operation
			patch = append(patch, map[string]interface{}{
				"op":   "remove",
				"path": "/spec/secretObjects",
			})
			slog.Debug("Removing secretObjects field", "namespace", namespace, "name", name)
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
	slog.Debug("Applying JSON Patch", "namespace", namespace, "name", name, "patch", string(patchBytes))

	// Apply the patch
	_, err = client.SecretsstoreV1().SecretProviderClasses(namespace).Patch(
		ctx,
		name,
		types.JSONPatchType,
		patchBytes,
		metav1.PatchOptions{},
	)

	if err != nil {
		return fmt.Errorf("error applying patch: %w", err)
	}

	slog.Info("Successfully patched SecretProviderClass", "namespace", namespace, "name", name)
	return nil
}

// CompareSecretObjects compares existing secretObjects in the resource with generated ones
// Returns true if they are different, false if identical
func CompareSecretObjects(obj *secretsstorev1.SecretProviderClass, generated []*secretsstorev1.SecretObject) bool {
	existingObjects := obj.Spec.SecretObjects

	if len(existingObjects) != len(generated) {
		slog.Debug("SecretObjects count changed", "existing", len(existingObjects), "generated", len(generated))
		return true
	}

	if len(existingObjects) == 0 {
		return false
	}

	existingMap := make(map[string]*secretsstorev1.SecretObject, len(existingObjects))
	for _, o := range existingObjects {
		existingMap[o.SecretName] = o
	}

	for _, gen := range generated {
		exist, ok := existingMap[gen.SecretName]
		if !ok {
			slog.Debug("Generated SecretObject not found in existing objects", "secretName", gen.SecretName)
			return true
		}
		if !secretObjectsEqual(exist, gen) {
			slog.Debug("SecretObject content has changed", "secretName", gen.SecretName)
			return true
		}
	}

	return false
}

// secretObjectsEqual compares two SecretObject structs for equality
func secretObjectsEqual(a, b *secretsstorev1.SecretObject) bool {
	if a.SecretName != b.SecretName || a.Type != b.Type || len(a.Data) != len(b.Data) || len(a.Labels) != len(b.Labels) {
		return false
	}

	// Compare Labels
	for k, v := range a.Labels {
		if b.Labels[k] != v {
			return false
		}
	}

	if len(a.Data) == 0 {
		return true
	}

	aDataMap := make(map[string]string)
	for _, d := range a.Data {
		aDataMap[d.Key] = d.ObjectName
	}

	for _, d := range b.Data {
		objName, ok := aDataMap[d.Key]
		if !ok || objName != d.ObjectName {
			return false
		}
	}

	return true
}

// GenerateSecretObjectsFromVault creates SecretObject entries for vault secrets and certificates
// Vault is the source of truth - no merging with existing secretObjects
// VaultSecretWithTags represents a vault secret with its name and tags
type VaultSecretWithTags struct {
	Name string
	Tags map[string]*string
}

// VaultCertWithTags represents a vault certificate with its name and tags
type VaultCertWithTags struct {
	Name string
	Tags map[string]*string
}

// hasTag checks if a tag key has the exact value "true"
func hasTag(tags map[string]*string, key string) bool {
	if tags == nil {
		return false
	}
	value, exists := tags[key]
	if !exists || value == nil {
		return false
	}
	return *value == "true"
}

// GenerateSecretObjectsFromVault generates K8s Secret objects based on vault tags
// Only secrets/certs with secret-object=true or cert-object=true tags are included
// Vault tags are the source of truth - SPC annotations are no longer used
func GenerateSecretObjectsFromVault(secrets []VaultSecretWithTags, certs []VaultCertWithTags) []*secretsstorev1.SecretObject {
	var secretObjects []*secretsstorev1.SecretObject
	var secretCount, certCount int

	// Add secrets (type: Opaque) if they have secret-object=true tag
	for _, secret := range secrets {
		if hasTag(secret.Tags, "secret-object") {
			secretObjects = append(secretObjects, &secretsstorev1.SecretObject{
				SecretName: secret.Name,
				Type:       "Opaque",
				Data: []*secretsstorev1.SecretObjectData{
					{
						Key:        secret.Name,
						ObjectName: secret.Name,
					},
				},
			})
			secretCount++
			slog.Debug("Secret opted into K8s Secret generation", "name", secret.Name)
		} else {
			slog.Debug("Secret opted out of K8s Secret generation", "name", secret.Name)
		}
	}

	// Add certificates (type: kubernetes.io/tls) if they have cert-object=true tag
	for _, cert := range certs {
		if hasTag(cert.Tags, "cert-object") {
			secretObjects = append(secretObjects, &secretsstorev1.SecretObject{
				SecretName: cert.Name,
				Type:       "kubernetes.io/tls",
				Data: []*secretsstorev1.SecretObjectData{
					{
						Key:        "tls.key",
						ObjectName: cert.Name,
					},
					{
						Key:        "tls.crt",
						ObjectName: cert.Name,
					},
				},
			})
			certCount++
			slog.Debug("Certificate opted into K8s Secret generation", "name", cert.Name)
		} else {
			slog.Debug("Certificate opted out of K8s Secret generation", "name", cert.Name)
		}
	}

	// Sort by secretName for consistent output
	sort.Slice(secretObjects, func(i, j int) bool {
		return secretObjects[i].SecretName < secretObjects[j].SecretName
	})

	slog.Info("Generated secretObjects from vault tags",
		"totalCount", len(secretObjects),
		"secretsWithTag", secretCount,
		"certsWithTag", certCount)
	return secretObjects
}
