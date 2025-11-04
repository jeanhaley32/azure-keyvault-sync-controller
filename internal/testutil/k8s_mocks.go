package testutil

import (
	"context"

	akvv1alpha1 "github.com/jeanhaley32/azure-keyvault-sync-controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	spcfake "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned/fake"
)

// K8sTestEnvironment provides a complete mock Kubernetes environment for testing
type K8sTestEnvironment struct {
	KubeClient *fake.Clientset
	SPCClient  *spcfake.Clientset
	CtrlClient client.Client // Controller-runtime client for CRD access
	Namespace  string
}

// NewK8sTestEnvironment creates a new Kubernetes test environment with mock clients
func NewK8sTestEnvironment() *K8sTestEnvironment {
	// Setup scheme with CRD types and core Kubernetes types
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)             // Core types (Secrets, ServiceAccounts, etc.)
	_ = akvv1alpha1.AddToScheme(scheme)        // AzureKeyVaultSync CRD
	_ = secretsstorev1.AddToScheme(scheme)     // SecretProviderClass CRD

	return &K8sTestEnvironment{
		KubeClient: fake.NewSimpleClientset(),
		SPCClient:  spcfake.NewSimpleClientset(),
		CtrlClient: fakeclient.NewClientBuilder().WithScheme(scheme).Build(),
		Namespace:  "default",
	}
}

// WithNamespace sets the default namespace for the test environment
func (e *K8sTestEnvironment) WithNamespace(ns string) *K8sTestEnvironment {
	e.Namespace = ns
	return e
}

// WithServiceAccount adds a service account to the mock cluster
func (e *K8sTestEnvironment) WithServiceAccount(sa *corev1.ServiceAccount) *K8sTestEnvironment {
	_, err := e.KubeClient.CoreV1().ServiceAccounts(sa.Namespace).Create(
		context.Background(),
		sa,
		metav1.CreateOptions{},
	)
	if err != nil {
		panic("failed to create service account in test environment: " + err.Error())
	}
	return e
}

// WithSecretProviderClass adds a SecretProviderClass to the mock cluster
func (e *K8sTestEnvironment) WithSecretProviderClass(spc *secretsstorev1.SecretProviderClass) *K8sTestEnvironment {
	_, err := e.SPCClient.SecretsstoreV1().SecretProviderClasses(spc.Namespace).Create(
		context.Background(),
		spc,
		metav1.CreateOptions{},
	)
	if err != nil {
		panic("failed to create SecretProviderClass in test environment: " + err.Error())
	}
	return e
}

// WithObjects adds multiple Kubernetes objects to the mock cluster
func (e *K8sTestEnvironment) WithObjects(objects ...runtime.Object) *K8sTestEnvironment {
	for _, obj := range objects {
		switch v := obj.(type) {
		case *corev1.ServiceAccount:
			e.WithServiceAccount(v)
		case *secretsstorev1.SecretProviderClass:
			e.WithSecretProviderClass(v)
		default:
			panic("unsupported object type in WithObjects")
		}
	}
	return e
}

// GetSecretProviderClass retrieves a SecretProviderClass from the mock cluster
func (e *K8sTestEnvironment) GetSecretProviderClass(namespace, name string) (*secretsstorev1.SecretProviderClass, error) {
	return e.SPCClient.SecretsstoreV1().SecretProviderClasses(namespace).Get(
		context.Background(),
		name,
		metav1.GetOptions{},
	)
}

// GetServiceAccount retrieves a service account from the mock cluster
func (e *K8sTestEnvironment) GetServiceAccount(namespace, name string) (*corev1.ServiceAccount, error) {
	return e.KubeClient.CoreV1().ServiceAccounts(namespace).Get(
		context.Background(),
		name,
		metav1.GetOptions{},
	)
}

// ListSecretProviderClasses lists all SecretProviderClasses in a namespace
func (e *K8sTestEnvironment) ListSecretProviderClasses(namespace string) (*secretsstorev1.SecretProviderClassList, error) {
	return e.SPCClient.SecretsstoreV1().SecretProviderClasses(namespace).List(
		context.Background(),
		metav1.ListOptions{},
	)
}

// UpdateSecretProviderClass updates a SecretProviderClass in the mock cluster
func (e *K8sTestEnvironment) UpdateSecretProviderClass(spc *secretsstorev1.SecretProviderClass) (*secretsstorev1.SecretProviderClass, error) {
	return e.SPCClient.SecretsstoreV1().SecretProviderClasses(spc.Namespace).Update(
		context.Background(),
		spc,
		metav1.UpdateOptions{},
	)
}

// DeleteSecretProviderClass deletes a SecretProviderClass from the mock cluster
func (e *K8sTestEnvironment) DeleteSecretProviderClass(namespace, name string) error {
	return e.SPCClient.SecretsstoreV1().SecretProviderClasses(namespace).Delete(
		context.Background(),
		name,
		metav1.DeleteOptions{},
	)
}

// CreateAzureKeyVaultSync creates an AzureKeyVaultSync CRD in the mock cluster
func (e *K8sTestEnvironment) CreateAzureKeyVaultSync(akv *akvv1alpha1.AzureKeyVaultSync) error {
	return e.CtrlClient.Create(context.Background(), akv)
}

// GetAzureKeyVaultSync retrieves an AzureKeyVaultSync CRD from the mock cluster
func (e *K8sTestEnvironment) GetAzureKeyVaultSync(namespace, name string) (*akvv1alpha1.AzureKeyVaultSync, error) {
	akv := &akvv1alpha1.AzureKeyVaultSync{}
	err := e.CtrlClient.Get(context.Background(), client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, akv)
	return akv, err
}
