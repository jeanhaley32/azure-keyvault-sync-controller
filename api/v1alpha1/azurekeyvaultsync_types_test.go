package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDeletePolicy_Constants(t *testing.T) {
	assert.Equal(t, DeletePolicy("Cascade"), DeletePolicyCascade)
	assert.Equal(t, DeletePolicy("Orphan"), DeletePolicyOrphan)
}

func TestAzureKeyVaultSync_Creation(t *testing.T) {
	akv := &AzureKeyVaultSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-akv",
			Namespace: "default",
		},
		Spec: AzureKeyVaultSyncSpec{
			KeyvaultName:   "my-vault",
			TenantID:       "12345678-1234-1234-1234-123456789012",
			ClientID:       "87654321-4321-4321-4321-210987654321",
			ServiceAccount: "my-sa",
		},
	}

	assert.Equal(t, "test-akv", akv.Name)
	assert.Equal(t, "default", akv.Namespace)
	assert.Equal(t, "my-vault", akv.Spec.KeyvaultName)
}

func TestAzureKeyVaultSyncSpec_WithFilters(t *testing.T) {
	spec := AzureKeyVaultSyncSpec{
		KeyvaultName:   "shared-vault",
		TenantID:       "12345678-1234-1234-1234-123456789012",
		ClientID:       "87654321-4321-4321-4321-210987654321",
		ServiceAccount: "my-sa",
		Filters: map[string]string{
			"service":     "backend",
			"environment": "prod",
		},
	}

	assert.NotNil(t, spec.Filters)
	assert.Equal(t, "backend", spec.Filters["service"])
	assert.Equal(t, "prod", spec.Filters["environment"])
	assert.Len(t, spec.Filters, 2)
}

func TestAzureKeyVaultSyncSpec_WithoutFilters(t *testing.T) {
	spec := AzureKeyVaultSyncSpec{
		KeyvaultName:   "dedicated-vault",
		TenantID:       "12345678-1234-1234-1234-123456789012",
		ClientID:       "87654321-4321-4321-4321-210987654321",
		ServiceAccount: "my-sa",
		// No filters
	}

	assert.Nil(t, spec.Filters)
	assert.Len(t, spec.Filters, 0)
}

func TestAzureKeyVaultSyncSpec_DefaultDeletePolicy(t *testing.T) {
	spec := AzureKeyVaultSyncSpec{
		KeyvaultName:   "my-vault",
		TenantID:       "12345678-1234-1234-1234-123456789012",
		ClientID:       "87654321-4321-4321-4321-210987654321",
		ServiceAccount: "my-sa",
		// DeletePolicy not specified
	}

	// Default should be empty (will be set to Cascade by webhook/defaulting)
	assert.Empty(t, spec.DeletePolicy)
}

func TestAzureKeyVaultSyncSpec_ExplicitDeletePolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy DeletePolicy
		want   DeletePolicy
	}{
		{
			name:   "cascade policy",
			policy: DeletePolicyCascade,
			want:   DeletePolicyCascade,
		},
		{
			name:   "orphan policy",
			policy: DeletePolicyOrphan,
			want:   DeletePolicyOrphan,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := AzureKeyVaultSyncSpec{
				KeyvaultName:   "my-vault",
				TenantID:       "12345678-1234-1234-1234-123456789012",
				ClientID:       "87654321-4321-4321-4321-210987654321",
				ServiceAccount: "my-sa",
				DeletePolicy:   tt.policy,
			}

			assert.Equal(t, tt.want, spec.DeletePolicy)
		})
	}
}

func TestAzureKeyVaultSyncStatus_Initialization(t *testing.T) {
	status := AzureKeyVaultSyncStatus{}

	assert.Nil(t, status.Conditions)
	assert.Nil(t, status.LastSyncTime)
	assert.Equal(t, 0, status.SecretCount)
	assert.Equal(t, 0, status.SecretObjectCount)
	assert.Equal(t, "", status.GeneratedSPCName)
	assert.Equal(t, int64(0), status.ObservedGeneration)
}

func TestAzureKeyVaultSyncStatus_WithData(t *testing.T) {
	now := metav1.Now()
	status := AzureKeyVaultSyncStatus{
		Conditions: []metav1.Condition{
			{
				Type:   "SPCReady",
				Status: metav1.ConditionTrue,
				Reason: "SPCCreated",
			},
		},
		LastSyncTime:       &now,
		SecretCount:        5,
		SecretObjectCount:  3,
		GeneratedSPCName:   "my-app-secrets",
		ObservedGeneration: 1,
	}

	assert.Len(t, status.Conditions, 1)
	assert.Equal(t, "SPCReady", status.Conditions[0].Type)
	assert.NotNil(t, status.LastSyncTime)
	assert.Equal(t, 5, status.SecretCount)
	assert.Equal(t, 3, status.SecretObjectCount)
	assert.Equal(t, "my-app-secrets", status.GeneratedSPCName)
	assert.Equal(t, int64(1), status.ObservedGeneration)
}

func TestAzureKeyVaultSync_Complete(t *testing.T) {
	now := metav1.Now()
	akv := &AzureKeyVaultSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "flow-staging-secrets",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: AzureKeyVaultSyncSpec{
			KeyvaultName:   "staging-flow-vault",
			TenantID:       "12345678-1234-1234-1234-123456789012",
			ClientID:       "87654321-4321-4321-4321-210987654321",
			ServiceAccount: "flow-staging-sa",
			Filters: map[string]string{
				"service":     "flow",
				"environment": "staging",
			},
			DeletePolicy: DeletePolicyCascade,
		},
		Status: AzureKeyVaultSyncStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "SPCReady",
					Status:             metav1.ConditionTrue,
					Reason:             "SPCCreated",
					Message:            "SecretProviderClass flow-staging-secrets created successfully",
					LastTransitionTime: now,
				},
			},
			LastSyncTime:       &now,
			SecretCount:        5,
			SecretObjectCount:  3,
			GeneratedSPCName:   "flow-staging-secrets",
			ObservedGeneration: 1,
		},
	}

	// Verify spec
	assert.Equal(t, "flow-staging-secrets", akv.Name)
	assert.Equal(t, "default", akv.Namespace)
	assert.Equal(t, "staging-flow-vault", akv.Spec.KeyvaultName)
	assert.Equal(t, 2, len(akv.Spec.Filters))
	assert.Equal(t, DeletePolicyCascade, akv.Spec.DeletePolicy)

	// Verify status
	assert.Len(t, akv.Status.Conditions, 1)
	assert.Equal(t, "SPCReady", akv.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, akv.Status.Conditions[0].Status)
	assert.Equal(t, 5, akv.Status.SecretCount)
	assert.Equal(t, 3, akv.Status.SecretObjectCount)
	assert.Equal(t, "flow-staging-secrets", akv.Status.GeneratedSPCName)
	assert.Equal(t, int64(1), akv.Status.ObservedGeneration)
}

func TestAzureKeyVaultSyncList(t *testing.T) {
	list := &AzureKeyVaultSyncList{
		Items: []AzureKeyVaultSync{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "akv-1",
					Namespace: "default",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "akv-2",
					Namespace: "default",
				},
			},
		},
	}

	assert.Len(t, list.Items, 2)
	assert.Equal(t, "akv-1", list.Items[0].Name)
	assert.Equal(t, "akv-2", list.Items[1].Name)
}
