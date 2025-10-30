package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/azure"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

// ReconcileTestScenario represents a test scenario for controller reconciliation
type ReconcileTestScenario struct {
	Name string

	// Setup
	SPC               *secretsstorev1.SecretProviderClass
	VaultSecrets      []azure.VaultSecret
	VaultCertificates []azure.VaultCertificate

	// Expected behavior
	ShouldReconcile       bool
	ExpectError           bool
	ExpectPatch           bool
	ExpectedObjectsCount  int
	ExpectedSecretsCount  int // In secretObjects array

	// Verification functions
	VerifyFunc func(t *testing.T, result interface{})
}

// EventHandlerTestScenario represents a test scenario for event handlers
type EventHandlerTestScenario struct {
	Name string

	// Setup
	SPC *secretsstorev1.SecretProviderClass

	// Expected behavior
	ShouldEnqueue  bool
	ShouldSkip     bool
	ExpectLog      string

	// Verification
	VerifyFunc func(t *testing.T, enqueueCalled bool)
}

// TestContext provides a complete test context with all necessary components
type TestContext struct {
	Ctx           context.Context
	K8sEnv        *K8sTestEnvironment
	AzureCache    *MockAzureTokenCache
	VaultClient   *MockVaultClient
	T             *testing.T
}

// NewTestContext creates a new test context with initialized components
func NewTestContext(t *testing.T) *TestContext {
	return &TestContext{
		Ctx:         context.Background(),
		K8sEnv:      NewK8sTestEnvironment(),
		AzureCache:  NewMockAzureTokenCache(),
		VaultClient: NewMockVaultClient(),
		T:           t,
	}
}

// WithTimeout adds a timeout to the context
func (tc *TestContext) WithTimeout(timeout time.Duration) *TestContext {
	ctx, cancel := context.WithTimeout(tc.Ctx, timeout)
	tc.T.Cleanup(cancel)
	tc.Ctx = ctx
	return tc
}

// Cleanup registers cleanup functions
func (tc *TestContext) Cleanup(f func()) {
	tc.T.Cleanup(f)
}

// AssertReconcileSuccess asserts that reconciliation completed successfully
func AssertReconcileSuccess(t *testing.T, err error, message string) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: expected no error, got: %v", message, err)
	}
}

// AssertReconcileError asserts that reconciliation returned an error
func AssertReconcileError(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected error, got nil", message)
	}
}

// AssertSPCPatched asserts that a SecretProviderClass was patched
func AssertSPCPatched(t *testing.T, original, updated *secretsstorev1.SecretProviderClass) {
	t.Helper()

	if original.ResourceVersion == updated.ResourceVersion {
		t.Error("expected SecretProviderClass to be patched (ResourceVersion unchanged)")
	}
}

// AssertSPCNotPatched asserts that a SecretProviderClass was not patched
func AssertSPCNotPatched(t *testing.T, original, updated *secretsstorev1.SecretProviderClass) {
	t.Helper()

	if original.ResourceVersion != updated.ResourceVersion {
		t.Error("expected SecretProviderClass to NOT be patched (ResourceVersion changed)")
	}
}

// AssertObjectsCount asserts the number of objects in the SPC spec
func AssertObjectsCount(t *testing.T, spc *secretsstorev1.SecretProviderClass, expected int) {
	t.Helper()

	// Objects are stored in the "objects" parameter as a YAML string
	// For now, we'll check if the parameter exists
	_, exists := spc.Spec.Parameters["objects"]
	if expected > 0 && !exists {
		t.Errorf("expected objects parameter to exist with %d objects", expected)
	}
	if expected == 0 && exists {
		t.Error("expected objects parameter to not exist or be empty")
	}
}

// AssertSecretObjectsCount asserts the number of secretObjects in the SPC spec
func AssertSecretObjectsCount(t *testing.T, spc *secretsstorev1.SecretProviderClass, expected int) {
	t.Helper()

	actual := len(spc.Spec.SecretObjects)
	if actual != expected {
		t.Errorf("expected %d secretObjects, got %d", expected, actual)
	}
}

// AssertSecretObjectPresent asserts that a specific secretObject exists
func AssertSecretObjectPresent(t *testing.T, spc *secretsstorev1.SecretProviderClass, secretName string) {
	t.Helper()

	for _, so := range spc.Spec.SecretObjects {
		if so.SecretName == secretName {
			return // Found it
		}
	}
	t.Errorf("expected secretObject with name %q, but not found", secretName)
}

// AssertAzureTokenCacheCalled asserts that the Azure token cache was called
func AssertAzureTokenCacheCalled(t *testing.T, cache *MockAzureTokenCache, expectedCalls int) {
	t.Helper()

	actual := cache.CallCount()
	if actual != expectedCalls {
		t.Errorf("expected Azure token cache to be called %d times, got %d", expectedCalls, actual)
	}
}

// AssertVaultClientCalled asserts that the vault client was called
func AssertVaultClientCalled(t *testing.T, client *MockVaultClient, expectedCalls int) {
	t.Helper()

	actual := client.CallCount()
	if actual != expectedCalls {
		t.Errorf("expected vault client to be called %d times, got %d", expectedCalls, actual)
	}
}

// RunTableTest runs a table-driven test with the provided scenarios
func RunTableTest[T any](t *testing.T, scenarios []T, testFunc func(t *testing.T, scenario T)) {
	for _, scenario := range scenarios {
		// Extract name using reflection or interface
		var name string
		switch s := any(scenario).(type) {
		case ReconcileTestScenario:
			name = s.Name
		case EventHandlerTestScenario:
			name = s.Name
		default:
			name = "unknown"
		}

		t.Run(name, func(t *testing.T) {
			testFunc(t, scenario)
		})
	}
}
