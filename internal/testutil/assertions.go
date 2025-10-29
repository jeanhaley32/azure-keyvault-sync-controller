package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// AssertNoError is a helper that fails the test if err is not nil
func AssertNoError(t *testing.T, err error, msgAndArgs ...interface{}) {
	t.Helper()
	assert.NoError(t, err, msgAndArgs...)
}

// AssertError is a helper that fails the test if err is nil
func AssertError(t *testing.T, err error, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Error(t, err, msgAndArgs...)
}

// AssertEqual is a helper that fails the test if expected != actual
func AssertEqual(t *testing.T, expected, actual interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Equal(t, expected, actual, msgAndArgs...)
}

// AssertNotEqual is a helper that fails the test if expected == actual
func AssertNotEqual(t *testing.T, expected, actual interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	assert.NotEqual(t, expected, actual, msgAndArgs...)
}

// AssertContains is a helper that fails the test if s does not contain substring
func AssertContains(t *testing.T, s, substring string, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Contains(t, s, substring, msgAndArgs...)
}

// AssertNotEmpty is a helper that fails the test if the value is empty
func AssertNotEmpty(t *testing.T, obj interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	assert.NotEmpty(t, obj, msgAndArgs...)
}

// AssertEmpty is a helper that fails the test if the value is not empty
func AssertEmpty(t *testing.T, obj interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Empty(t, obj, msgAndArgs...)
}

// AssertNil is a helper that fails the test if the value is not nil
func AssertNil(t *testing.T, obj interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Nil(t, obj, msgAndArgs...)
}

// AssertNotNil is a helper that fails the test if the value is nil
func AssertNotNil(t *testing.T, obj interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	assert.NotNil(t, obj, msgAndArgs...)
}
