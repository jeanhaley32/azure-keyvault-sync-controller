package testutil

import (
	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

// Helper function to create string pointers
func Ptr(s string) *string {
	return &s
}

// SecretProviderClassBuilder provides a fluent API for building test SecretProviderClass resources
type SecretProviderClassBuilder struct {
	spc *secretsstorev1.SecretProviderClass
}

// NewSecretProviderClass creates a new SecretProviderClass builder with sensible defaults
func NewSecretProviderClass(namespace, name string) *SecretProviderClassBuilder {
	return &SecretProviderClassBuilder{
		spc: &secretsstorev1.SecretProviderClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Annotations: make(map[string]string),
				Labels:      make(map[string]string),
			},
			Spec: secretsstorev1.SecretProviderClassSpec{
				Provider: "azure",
				Parameters: map[string]string{
					"keyvaultName": "test-vault",
					"tenantId":     "test-tenant-id",
				},
			},
		},
	}
}

// WithServiceAccount adds the service-account annotation
func (b *SecretProviderClassBuilder) WithServiceAccount(sa string) *SecretProviderClassBuilder {
	b.spc.Annotations["azure-keyvault-sync/service-account"] = sa
	return b
}

// WithRespectTags adds the respect-tags annotation
func (b *SecretProviderClassBuilder) WithRespectTags(enabled bool) *SecretProviderClassBuilder {
	if enabled {
		b.spc.Annotations["azure-keyvault-sync/respect-tags"] = "true"
	} else {
		b.spc.Annotations["azure-keyvault-sync/respect-tags"] = "false"
	}
	return b
}

// WithLabel adds a label to the SecretProviderClass
func (b *SecretProviderClassBuilder) WithLabel(key, value string) *SecretProviderClassBuilder {
	b.spc.Labels[key] = value
	return b
}

// WithAnnotation adds an annotation to the SecretProviderClass
func (b *SecretProviderClassBuilder) WithAnnotation(key, value string) *SecretProviderClassBuilder {
	b.spc.Annotations[key] = value
	return b
}

// WithKeyvaultName sets the keyvault name parameter
func (b *SecretProviderClassBuilder) WithKeyvaultName(name string) *SecretProviderClassBuilder {
	b.spc.Spec.Parameters["keyvaultName"] = name
	return b
}

// WithTenantID sets the tenant ID parameter
func (b *SecretProviderClassBuilder) WithTenantID(tenantID string) *SecretProviderClassBuilder {
	b.spc.Spec.Parameters["tenantId"] = tenantID
	return b
}

// WithObjects sets the objects array in the spec
func (b *SecretProviderClassBuilder) WithObjects(objects string) *SecretProviderClassBuilder {
	b.spc.Spec.Parameters["objects"] = objects
	return b
}

// WithSecretObjects sets the secretObjects array in the spec
func (b *SecretProviderClassBuilder) WithSecretObjects(secretObjects []*secretsstorev1.SecretObject) *SecretProviderClassBuilder {
	b.spc.Spec.SecretObjects = secretObjects
	return b
}

// Build returns the constructed SecretProviderClass
func (b *SecretProviderClassBuilder) Build() *secretsstorev1.SecretProviderClass {
	return b.spc
}

// ServiceAccountBuilder provides a fluent API for building test ServiceAccount resources
type ServiceAccountBuilder struct {
	sa *corev1.ServiceAccount
}

// NewServiceAccount creates a new ServiceAccount builder with sensible defaults
func NewServiceAccount(namespace, name string) *ServiceAccountBuilder {
	return &ServiceAccountBuilder{
		sa: &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Annotations: make(map[string]string),
			},
		},
	}
}

// WithAnnotation adds an annotation to the ServiceAccount
func (b *ServiceAccountBuilder) WithAnnotation(key, value string) *ServiceAccountBuilder {
	b.sa.Annotations[key] = value
	return b
}

// WithAzureClientID adds the azure-client-id annotation
func (b *ServiceAccountBuilder) WithAzureClientID(clientID string) *ServiceAccountBuilder {
	b.sa.Annotations["azure.workload.identity/client-id"] = clientID
	return b
}

// WithAzureTenantID adds the azure-tenant-id annotation
func (b *ServiceAccountBuilder) WithAzureTenantID(tenantID string) *ServiceAccountBuilder {
	b.sa.Annotations["azure.workload.identity/tenant-id"] = tenantID
	return b
}

// Build returns the constructed ServiceAccount
func (b *ServiceAccountBuilder) Build() *corev1.ServiceAccount {
	return b.sa
}

// VaultSecretBuilder provides a fluent API for building test Azure vault secrets
type VaultSecretBuilder struct {
	secret azure.VaultSecret
}

// NewVaultSecret creates a new VaultSecret builder
func NewVaultSecret(name string) *VaultSecretBuilder {
	return &VaultSecretBuilder{
		secret: azure.VaultSecret{
			Name: name,
			Tags: make(map[string]*string),
		},
	}
}

// WithTag adds a tag to the vault secret
func (b *VaultSecretBuilder) WithTag(key, value string) *VaultSecretBuilder {
	b.secret.Tags[key] = Ptr(value)
	return b
}

// WithServiceTag adds the service tag
func (b *VaultSecretBuilder) WithServiceTag(service string) *VaultSecretBuilder {
	b.secret.Tags["service"] = Ptr(service)
	return b
}

// WithEnvironmentTag adds the environment tag
func (b *VaultSecretBuilder) WithEnvironmentTag(env string) *VaultSecretBuilder {
	b.secret.Tags["environment"] = Ptr(env)
	return b
}

// WithSecretObjectTag adds the secret-object tag
func (b *VaultSecretBuilder) WithSecretObjectTag() *VaultSecretBuilder {
	b.secret.Tags["secret-object"] = Ptr("true")
	return b
}

// Build returns the constructed VaultSecret
func (b *VaultSecretBuilder) Build() azure.VaultSecret {
	return b.secret
}

// VaultCertificateBuilder provides a fluent API for building test Azure vault certificates
type VaultCertificateBuilder struct {
	cert azure.VaultCertificate
}

// NewVaultCertificate creates a new VaultCertificate builder
func NewVaultCertificate(name string) *VaultCertificateBuilder {
	return &VaultCertificateBuilder{
		cert: azure.VaultCertificate{
			Name: name,
			Tags: make(map[string]*string),
		},
	}
}

// WithTag adds a tag to the vault certificate
func (b *VaultCertificateBuilder) WithTag(key, value string) *VaultCertificateBuilder {
	b.cert.Tags[key] = Ptr(value)
	return b
}

// WithServiceTag adds the service tag
func (b *VaultCertificateBuilder) WithServiceTag(service string) *VaultCertificateBuilder {
	b.cert.Tags["service"] = Ptr(service)
	return b
}

// WithEnvironmentTag adds the environment tag
func (b *VaultCertificateBuilder) WithEnvironmentTag(env string) *VaultCertificateBuilder {
	b.cert.Tags["environment"] = Ptr(env)
	return b
}

// WithCertObjectTag adds the cert-object tag
func (b *VaultCertificateBuilder) WithCertObjectTag() *VaultCertificateBuilder {
	b.cert.Tags["cert-object"] = Ptr("true")
	return b
}

// Build returns the constructed VaultCertificate
func (b *VaultCertificateBuilder) Build() azure.VaultCertificate {
	return b.cert
}

// Common Test Fixtures

// MinimalValidSPC returns a minimal valid SecretProviderClass with required annotations
func MinimalValidSPC() *secretsstorev1.SecretProviderClass {
	return NewSecretProviderClass("default", "test-spc").
		WithServiceAccount("test-sa").
		Build()
}

// SPCWithTagFiltering returns a SecretProviderClass with tag filtering enabled
func SPCWithTagFiltering(service, environment string) *secretsstorev1.SecretProviderClass {
	builder := NewSecretProviderClass("default", "test-spc").
		WithServiceAccount("test-sa").
		WithRespectTags(true)

	if service != "" {
		builder.WithLabel("service", service)
	}
	if environment != "" {
		builder.WithLabel("environment", environment)
	}

	return builder.Build()
}

// SPCWithoutServiceAccount returns a SecretProviderClass without the service-account annotation
func SPCWithoutServiceAccount() *secretsstorev1.SecretProviderClass {
	return NewSecretProviderClass("default", "test-spc").Build()
}

// SPCWithInvalidKeyvault returns a SecretProviderClass with an invalid keyvault name
func SPCWithInvalidKeyvault() *secretsstorev1.SecretProviderClass {
	return NewSecretProviderClass("default", "test-spc").
		WithServiceAccount("test-sa").
		WithKeyvaultName("").
		Build()
}

// DefaultServiceAccount returns a service account with Azure workload identity annotations
func DefaultServiceAccount() *corev1.ServiceAccount {
	return NewServiceAccount("default", "test-sa").
		WithAzureClientID("test-client-id").
		WithAzureTenantID("test-tenant-id").
		Build()
}

// VaultSecretsWithTags returns a set of vault secrets with various tag configurations
func VaultSecretsWithTags() []azure.VaultSecret {
	return []azure.VaultSecret{
		NewVaultSecret("secret1").
			WithServiceTag("app1").
			WithEnvironmentTag("prod").
			WithSecretObjectTag().
			Build(),
		NewVaultSecret("secret2").
			WithServiceTag("app1").
			WithEnvironmentTag("dev").
			Build(),
		NewVaultSecret("secret3").
			WithServiceTag("app2").
			Build(),
		NewVaultSecret("secret4").Build(), // No tags
	}
}

// VaultCertificatesWithTags returns a set of vault certificates with various tag configurations
func VaultCertificatesWithTags() []azure.VaultCertificate {
	return []azure.VaultCertificate{
		NewVaultCertificate("cert1").
			WithServiceTag("app1").
			WithEnvironmentTag("prod").
			WithCertObjectTag().
			Build(),
		NewVaultCertificate("cert2").
			WithServiceTag("app1").
			Build(),
		NewVaultCertificate("cert3").Build(), // No tags
	}
}
