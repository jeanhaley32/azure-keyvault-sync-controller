package testutil

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	spcfake "sigs.k8s.io/secrets-store-csi-driver/pkg/client/clientset/versioned/fake"
)

// K8sTestEnvironment provides a complete mock Kubernetes environment for testing
type K8sTestEnvironment struct {
	KubeClient *fake.Clientset
	SPCClient  *spcfake.Clientset
	Namespace  string
}

// NewK8sTestEnvironment creates a new Kubernetes test environment with mock clients
func NewK8sTestEnvironment() *K8sTestEnvironment {
	return &K8sTestEnvironment{
		KubeClient: fake.NewSimpleClientset(),
		SPCClient:  spcfake.NewSimpleClientset(),
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
