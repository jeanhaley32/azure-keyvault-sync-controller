package update

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	spcfake "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned/fake"
)

// TestPatchSecretProviderClass demonstrates how to test Kubernetes API calls using fake clients
func TestPatchSecretProviderClass(t *testing.T) {
	t.Run("successfully patches objects and timestamp", func(t *testing.T) {
		// 1. Create fake Kubernetes client with proper scheme
		scheme := runtime.NewScheme()
		_ = secretsstorev1.AddToScheme(scheme)
		fakeClient := spcfake.NewSimpleClientset()

		// 2. Create initial SecretProviderClass in fake cluster
		spc := &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-spc",
				Namespace: "default",
				Annotations: map[string]string{
					"azure-keyvault-sync/last-sync": "old-timestamp",
				},
			},
			Spec: secretsstorev1.SecretProviderClassSpec{
				Provider: "azure",
				Parameters: map[string]string{
					"objects":      "old-objects-yaml",
					"keyvaultName": "test-vault",
				},
			},
		}

		_, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Create(
			context.Background(),
			spc,
			metav1.CreateOptions{},
		)
		assert.NoError(t, err)

		// 3. Call PatchSecretProviderClass with new values
		timestamp := time.Now().Format(time.RFC3339)
		newObjectsYAML := "array:\n  - objectName: secret1\n  - objectName: secret2"

		err = PatchSecretProviderClass(
			context.Background(),
			fakeClient,
			"default",
			"test-spc",
			newObjectsYAML,
			nil, // no secretObjects change
			nil, // no annotations
			timestamp,
		)

		// 4. Verify patch succeeded
		assert.NoError(t, err)

		// 5. Retrieve updated resource from fake cluster
		updated, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Get(
			context.Background(),
			"test-spc",
			metav1.GetOptions{},
		)

		assert.NoError(t, err)
		assert.Equal(t, newObjectsYAML, updated.Spec.Parameters["objects"])
		assert.Equal(t, timestamp, updated.Annotations["azure-keyvault-sync/last-sync"])
	})

	t.Run("replaces secretObjects when provided", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = secretsstorev1.AddToScheme(scheme)
		fakeClient := spcfake.NewSimpleClientset()

		// Create SPC with existing secretObjects (required for replace operation)
		spc := &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-spc",
				Namespace: "default",
				Annotations: map[string]string{
					"azure-keyvault-sync/last-sync": "old-timestamp",
				},
			},
			Spec: secretsstorev1.SecretProviderClassSpec{
				Provider: "azure",
				Parameters: map[string]string{
					"objects": "array:\n  - objectName: secret1",
				},
				SecretObjects: []*secretsstorev1.SecretObject{
					{
						SecretName: "old-secret",
						Type:       "Opaque",
					},
				},
			},
		}

		_, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Create(
			context.Background(),
			spc,
			metav1.CreateOptions{},
		)
		assert.NoError(t, err)

		// Patch with new secretObjects
		secretObjects := []*secretsstorev1.SecretObject{
			{
				SecretName: "k8s-secret-1",
				Type:       "Opaque",
				Data: []*secretsstorev1.SecretObjectData{
					{
						ObjectName: "secret1",
						Key:        "password",
					},
				},
			},
		}

		err = PatchSecretProviderClass(
			context.Background(),
			fakeClient,
			"default",
			"test-spc",
			"array:\n  - objectName: secret1",
			secretObjects,
			nil, // no annotations
			time.Now().Format(time.RFC3339),
		)

		assert.NoError(t, err)

		// Verify secretObjects were replaced
		updated, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Get(
			context.Background(),
			"test-spc",
			metav1.GetOptions{},
		)

		assert.NoError(t, err)
		assert.Len(t, updated.Spec.SecretObjects, 1)
		assert.Equal(t, "k8s-secret-1", updated.Spec.SecretObjects[0].SecretName)
		assert.Equal(t, "Opaque", updated.Spec.SecretObjects[0].Type)
	})

	t.Run("removes secretObjects when REMOVE_FIELD marker provided", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = secretsstorev1.AddToScheme(scheme)
		fakeClient := spcfake.NewSimpleClientset()

		// Create SPC with existing secretObjects
		spc := &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-spc",
				Namespace: "default",
				Annotations: map[string]string{
					"azure-keyvault-sync/last-sync": "old-timestamp",
				},
			},
			Spec: secretsstorev1.SecretProviderClassSpec{
				Provider: "azure",
				Parameters: map[string]string{
					"objects": "array:\n  - objectName: secret1",
				},
				SecretObjects: []*secretsstorev1.SecretObject{
					{
						SecretName: "old-secret",
						Type:       "Opaque",
					},
				},
			},
		}

		_, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Create(
			context.Background(),
			spc,
			metav1.CreateOptions{},
		)
		assert.NoError(t, err)

		// Patch with REMOVE_FIELD marker
		err = PatchSecretProviderClass(
			context.Background(),
			fakeClient,
			"default",
			"test-spc",
			"array:\n  - objectName: secret1",
			"REMOVE_FIELD", // Special marker to remove field
			nil, // no annotations
			time.Now().Format(time.RFC3339),
		)

		assert.NoError(t, err)

		// Verify secretObjects were removed
		updated, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Get(
			context.Background(),
			"test-spc",
			metav1.GetOptions{},
		)

		assert.NoError(t, err)
		assert.Nil(t, updated.Spec.SecretObjects)
	})

	t.Run("returns error when resource does not exist", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = secretsstorev1.AddToScheme(scheme)
		fakeClient := spcfake.NewSimpleClientset()

		// Try to patch non-existent resource
		err := PatchSecretProviderClass(
			context.Background(),
			fakeClient,
			"default",
			"does-not-exist",
			"array:\n  - objectName: secret1",
			nil,
			nil, // no annotations
			time.Now().Format(time.RFC3339),
		)

		// Should get error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error applying patch")
	})

	t.Run("handles complex secretObjects with multiple data mappings", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = secretsstorev1.AddToScheme(scheme)
		fakeClient := spcfake.NewSimpleClientset()

		spc := &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-spc",
				Namespace: "default",
				Annotations: map[string]string{
					"azure-keyvault-sync/last-sync": "old-timestamp",
				},
			},
			Spec: secretsstorev1.SecretProviderClassSpec{
				Provider: "azure",
				Parameters: map[string]string{
					"objects": "array:\n  - objectName: db-password\n  - objectName: api-key",
				},
				SecretObjects: []*secretsstorev1.SecretObject{
					{
						SecretName: "placeholder-secret",
						Type:       "Opaque",
					},
				},
			},
		}

		_, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Create(
			context.Background(),
			spc,
			metav1.CreateOptions{},
		)
		assert.NoError(t, err)

		// Complex secretObjects with multiple data mappings
		secretObjects := []*secretsstorev1.SecretObject{
			{
				SecretName: "database-creds",
				Type:       "Opaque",
				Data: []*secretsstorev1.SecretObjectData{
					{
						ObjectName: "db-password",
						Key:        "password",
					},
					{
						ObjectName: "db-username",
						Key:        "username",
					},
				},
			},
			{
				SecretName: "api-credentials",
				Type:       "Opaque",
				Data: []*secretsstorev1.SecretObjectData{
					{
						ObjectName: "api-key",
						Key:        "key",
					},
				},
			},
		}

		err = PatchSecretProviderClass(
			context.Background(),
			fakeClient,
			"default",
			"test-spc",
			"array:\n  - objectName: db-password\n  - objectName: api-key",
			secretObjects,
			nil, // no annotations
			time.Now().Format(time.RFC3339),
		)

		assert.NoError(t, err)

		// Verify complex structure
		updated, err := fakeClient.SecretsstoreV1().SecretProviderClasses("default").Get(
			context.Background(),
			"test-spc",
			metav1.GetOptions{},
		)

		assert.NoError(t, err)
		assert.Len(t, updated.Spec.SecretObjects, 2)

		// Check first secret object
		assert.Equal(t, "database-creds", updated.Spec.SecretObjects[0].SecretName)
		assert.Len(t, updated.Spec.SecretObjects[0].Data, 2)

		// Check second secret object
		assert.Equal(t, "api-credentials", updated.Spec.SecretObjects[1].SecretName)
		assert.Len(t, updated.Spec.SecretObjects[1].Data, 1)
	})
}
