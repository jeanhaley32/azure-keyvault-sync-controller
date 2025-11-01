package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeletePolicy defines what happens to the SecretProviderClass when the AzureKeyVaultSync is deleted
// +kubebuilder:validation:Enum=Cascade;Orphan
type DeletePolicy string

const (
	// DeletePolicyCascade means the SecretProviderClass will be deleted when the AzureKeyVaultSync is deleted
	DeletePolicyCascade DeletePolicy = "Cascade"

	// DeletePolicyOrphan means the SecretProviderClass will remain when the AzureKeyVaultSync is deleted
	DeletePolicyOrphan DeletePolicy = "Orphan"
)

// AzureKeyVaultSyncSpec defines the desired state of AzureKeyVaultSync
type AzureKeyVaultSyncSpec struct {
	// KeyvaultName is the name of the Azure Key Vault to sync secrets from
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=24
	// +kubebuilder:validation:Pattern="^[a-zA-Z0-9-]+$"
	KeyvaultName string `json:"keyvaultName"`

	// TenantID is the Azure tenant ID (UUID format)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"
	TenantID string `json:"tenantId"`

	// ClientID is the Azure client ID for workload identity (UUID format)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"
	ClientID string `json:"clientID"`

	// ServiceAccount is the Kubernetes ServiceAccount name with Azure workload identity annotations
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ServiceAccount string `json:"serviceAccount"`

	// Filters defines tag key/value pairs for filtering vault secrets.
	// If omitted, all secrets from the vault are synced.
	// If specified, only secrets matching ALL filter tags are synced.
	// +optional
	Filters map[string]string `json:"filters,omitempty"`

	// DeletePolicy defines what happens to the SecretProviderClass when this resource is deleted.
	// Cascade (default): SecretProviderClass is deleted with this resource
	// Orphan: SecretProviderClass remains when this resource is deleted
	// +optional
	// +kubebuilder:default=Cascade
	DeletePolicy DeletePolicy `json:"deletePolicy,omitempty"`
}

// AzureKeyVaultSyncStatus defines the observed state of AzureKeyVaultSync
type AzureKeyVaultSyncStatus struct {
	// Conditions represent the latest available observations of the AzureKeyVaultSync's state
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncTime is the timestamp of the last successful vault synchronization
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// SecretCount is the number of secrets synced from the vault (after filtering)
	// +optional
	SecretCount int `json:"secretCount,omitempty"`

	// SecretObjectCount is the number of secretObjects created (secrets with secret-object: "true" tag)
	// +optional
	SecretObjectCount int `json:"secretObjectCount,omitempty"`

	// GeneratedSPCName is the name of the generated SecretProviderClass (matches CRD name)
	// +optional
	GeneratedSPCName string `json:"generatedSPCName,omitempty"`

	// ObservedGeneration reflects the generation of the most recently observed AzureKeyVaultSync
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// AzureKeyVaultSync is the Schema for the azurekeyvaultsyncs API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=akv;akvs
// +kubebuilder:printcolumn:name="Vault",type=string,JSONPath=`.spec.keyvaultName`
// +kubebuilder:printcolumn:name="Secrets",type=integer,JSONPath=`.status.secretCount`
// +kubebuilder:printcolumn:name="SecretObjects",type=integer,JSONPath=`.status.secretObjectCount`
// +kubebuilder:printcolumn:name="SPC",type=string,JSONPath=`.status.generatedSPCName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type AzureKeyVaultSync struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AzureKeyVaultSyncSpec   `json:"spec,omitempty"`
	Status AzureKeyVaultSyncStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AzureKeyVaultSyncList contains a list of AzureKeyVaultSync
type AzureKeyVaultSyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AzureKeyVaultSync `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AzureKeyVaultSync{}, &AzureKeyVaultSyncList{})
}
