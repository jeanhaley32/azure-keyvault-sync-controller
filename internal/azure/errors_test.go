package azure

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
)

// TestIsAzureThrottled tests throttling detection
func TestIsAzureThrottled(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		expectThrottled bool
	}{
		{
			name:            "nil error",
			err:             nil,
			expectThrottled: false,
		},
		{
			name: "429 status code",
			err: &azcore.ResponseError{
				StatusCode: http.StatusTooManyRequests,
			},
			expectThrottled: true,
		},
		{
			name: "200 status code",
			err: &azcore.ResponseError{
				StatusCode: http.StatusOK,
			},
			expectThrottled: false,
		},
		{
			name: "500 status code",
			err: &azcore.ResponseError{
				StatusCode: http.StatusInternalServerError,
			},
			expectThrottled: false,
		},
		{
			name:            "error message with '429'",
			err:             errors.New("received 429 from Azure"),
			expectThrottled: true,
		},
		{
			name:            "error message with 'too many requests'",
			err:             errors.New("too many requests to key vault"),
			expectThrottled: true,
		},
		{
			name:            "error message with 'Too Many Requests' (mixed case)",
			err:             errors.New("Error: Too Many Requests"),
			expectThrottled: true,
		},
		{
			name:            "error message with 'throttled'",
			err:             errors.New("request was throttled by Azure"),
			expectThrottled: true,
		},
		{
			name:            "error message with 'THROTTLED' (uppercase)",
			err:             errors.New("REQUEST THROTTLED"),
			expectThrottled: true,
		},
		{
			name:            "error message with 'rate limit'",
			err:             errors.New("rate limit exceeded"),
			expectThrottled: true,
		},
		{
			name:            "error message with 'Rate Limit' (mixed case)",
			err:             errors.New("Rate Limit Exceeded"),
			expectThrottled: true,
		},
		{
			name:            "unrelated error",
			err:             errors.New("connection timeout"),
			expectThrottled: false,
		},
		{
			name:            "generic error",
			err:             errors.New("something went wrong"),
			expectThrottled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAzureThrottled(tt.err)
			assert.Equal(t, tt.expectThrottled, result)
		})
	}
}

// TestExtractRetryAfter tests Retry-After header extraction
func TestExtractRetryAfter(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		expectedDefault bool
		minDuration     time.Duration
		maxDuration     time.Duration
	}{
		{
			name:            "nil error returns default",
			err:             nil,
			expectedDefault: true,
			minDuration:     10 * time.Second,
			maxDuration:     10 * time.Second,
		},
		{
			name: "ResponseError without Retry-After",
			err: &azcore.ResponseError{
				StatusCode:  http.StatusTooManyRequests,
				RawResponse: &http.Response{Header: http.Header{}},
			},
			expectedDefault: true,
			minDuration:     10 * time.Second,
			maxDuration:     10 * time.Second,
		},
		{
			name: "ResponseError with Retry-After as seconds (integer)",
			err: func() error {
				resp := &http.Response{Header: http.Header{}}
				resp.Header.Set("Retry-After", "30")
				return &azcore.ResponseError{
					StatusCode:  http.StatusTooManyRequests,
					RawResponse: resp,
				}
			}(),
			expectedDefault: false,
			minDuration:     30 * time.Second,
			maxDuration:     30 * time.Second,
		},
		{
			name: "ResponseError with Retry-After as seconds (large value)",
			err: func() error {
				resp := &http.Response{Header: http.Header{}}
				resp.Header.Set("Retry-After", "120")
				return &azcore.ResponseError{
					StatusCode:  http.StatusTooManyRequests,
					RawResponse: resp,
				}
			}(),
			expectedDefault: false,
			minDuration:     120 * time.Second,
			maxDuration:     120 * time.Second,
		},
		{
			name: "ResponseError with Retry-After as HTTP-date (future)",
			err: func() error {
				resp := &http.Response{Header: http.Header{}}
				futureTime := time.Now().Add(15 * time.Second)
				resp.Header.Set("Retry-After", futureTime.Format(http.TimeFormat))
				return &azcore.ResponseError{
					StatusCode:  http.StatusTooManyRequests,
					RawResponse: resp,
				}
			}(),
			expectedDefault: false,
			minDuration:     10 * time.Second, // Allow some timing variance
			maxDuration:     20 * time.Second,
		},
		{
			name: "ResponseError with invalid Retry-After",
			err: func() error {
				resp := &http.Response{Header: http.Header{}}
				resp.Header.Set("Retry-After", "invalid")
				return &azcore.ResponseError{
					StatusCode:  http.StatusTooManyRequests,
					RawResponse: resp,
				}
			}(),
			expectedDefault: true,
			minDuration:     10 * time.Second,
			maxDuration:     10 * time.Second,
		},
		{
			name: "ResponseError with nil RawResponse",
			err: &azcore.ResponseError{
				StatusCode:  http.StatusTooManyRequests,
				RawResponse: nil,
			},
			expectedDefault: true,
			minDuration:     10 * time.Second,
			maxDuration:     10 * time.Second,
		},
		{
			name:            "non-ResponseError",
			err:             errors.New("generic error"),
			expectedDefault: true,
			minDuration:     10 * time.Second,
			maxDuration:     10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration := ExtractRetryAfter(tt.err)

			if tt.expectedDefault {
				assert.Equal(t, 10*time.Second, duration)
			} else {
				assert.GreaterOrEqual(t, duration, tt.minDuration)
				assert.LessOrEqual(t, duration, tt.maxDuration)
			}
		})
	}
}

// TestIsAzureAuthError tests authentication/authorization error detection
func TestIsAzureAuthError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectAuth  bool
	}{
		{
			name:       "nil error",
			err:        nil,
			expectAuth: false,
		},
		{
			name: "401 Unauthorized",
			err: &azcore.ResponseError{
				StatusCode: http.StatusUnauthorized,
			},
			expectAuth: true,
		},
		{
			name: "403 Forbidden",
			err: &azcore.ResponseError{
				StatusCode: http.StatusForbidden,
			},
			expectAuth: true,
		},
		{
			name: "404 Not Found",
			err: &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
			},
			expectAuth: false,
		},
		{
			name: "429 Too Many Requests",
			err: &azcore.ResponseError{
				StatusCode: http.StatusTooManyRequests,
			},
			expectAuth: false,
		},
		{
			name: "500 Internal Server Error",
			err: &azcore.ResponseError{
				StatusCode: http.StatusInternalServerError,
			},
			expectAuth: false,
		},
		{
			name:       "error message with 'unauthorized'",
			err:        errors.New("unauthorized access to vault"),
			expectAuth: true,
		},
		{
			name:       "error message with 'Unauthorized' (mixed case)",
			err:        errors.New("Unauthorized: invalid credentials"),
			expectAuth: true,
		},
		{
			name:       "error message with 'forbidden'",
			err:        errors.New("forbidden: insufficient permissions"),
			expectAuth: true,
		},
		{
			name:       "error message with 'FORBIDDEN' (uppercase)",
			err:        errors.New("ACCESS FORBIDDEN"),
			expectAuth: true,
		},
		{
			name:       "error message with 'authentication'",
			err:        errors.New("authentication failed"),
			expectAuth: true,
		},
		{
			name:       "error message with 'Authentication' (mixed case)",
			err:        errors.New("Authentication Error"),
			expectAuth: true,
		},
		{
			name:       "error message with 'permission'",
			err:        errors.New("permission denied"),
			expectAuth: true,
		},
		{
			name:       "error message with 'Permission' (mixed case)",
			err:        errors.New("Permission Denied"),
			expectAuth: true,
		},
		{
			name:       "unrelated error",
			err:        errors.New("connection timeout"),
			expectAuth: false,
		},
		{
			name:       "generic error",
			err:        errors.New("something went wrong"),
			expectAuth: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAzureAuthError(tt.err)
			assert.Equal(t, tt.expectAuth, result)
		})
	}
}

// TestResponseErrorWrapping tests that wrapped ResponseErrors are detected
func TestResponseErrorWrapping(t *testing.T) {
	// Create a wrapped ResponseError
	baseErr := &azcore.ResponseError{
		StatusCode: http.StatusTooManyRequests,
	}
	wrappedErr := errors.New("failed to list secrets: " + baseErr.Error())

	// The current implementation won't detect wrapped ResponseErrors
	// because it uses errors.As which requires the error chain to be proper
	// This test documents current behavior
	assert.False(t, IsAzureThrottled(wrappedErr))

	// But it should still detect via message matching
	throttledWithMessage := errors.New("failed to list: 429 Too Many Requests")
	assert.True(t, IsAzureThrottled(throttledWithMessage))
}

// TestExtractRetryAfterPastDate tests Retry-After with a past date
func TestExtractRetryAfterPastDate(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	pastTime := time.Now().Add(-30 * time.Second)
	resp.Header.Set("Retry-After", pastTime.Format(http.TimeFormat))

	err := &azcore.ResponseError{
		StatusCode:  http.StatusTooManyRequests,
		RawResponse: resp,
	}

	// Past dates should return default (not negative duration)
	duration := ExtractRetryAfter(err)
	assert.Equal(t, 10*time.Second, duration)
}

// TestExtractRetryAfterZeroSeconds tests Retry-After with 0 seconds
func TestExtractRetryAfterZeroSeconds(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "0")

	err := &azcore.ResponseError{
		StatusCode:  http.StatusTooManyRequests,
		RawResponse: resp,
	}

	duration := ExtractRetryAfter(err)
	assert.Equal(t, 0*time.Second, duration)
}

// TestIsAzureThrottledCaseInsensitivity tests case insensitivity of message matching
func TestIsAzureThrottledCaseInsensitivity(t *testing.T) {
	testCases := []string{
		"429",
		"TOO MANY REQUESTS",
		"Too Many Requests",
		"too many requests",
		"THROTTLED",
		"Throttled",
		"throttled",
		"RATE LIMIT",
		"Rate Limit",
		"rate limit",
	}

	for _, msg := range testCases {
		err := errors.New(msg)
		assert.True(t, IsAzureThrottled(err), "should detect throttling for: %s", msg)
	}
}

// TestIsAzureAuthErrorCaseInsensitivity tests case insensitivity of message matching
func TestIsAzureAuthErrorCaseInsensitivity(t *testing.T) {
	testCases := []string{
		"UNAUTHORIZED",
		"Unauthorized",
		"unauthorized",
		"FORBIDDEN",
		"Forbidden",
		"forbidden",
		"AUTHENTICATION",
		"Authentication",
		"authentication",
		"PERMISSION",
		"Permission",
		"permission",
	}

	for _, msg := range testCases {
		err := errors.New(msg)
		assert.True(t, IsAzureAuthError(err), "should detect auth error for: %s", msg)
	}
}

// TestErrorDetectionCombinations tests combinations of error conditions
func TestErrorDetectionCombinations(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectThrottle bool
		expectAuth     bool
	}{
		{
			name: "401 is auth error, not throttle",
			err: &azcore.ResponseError{
				StatusCode: http.StatusUnauthorized,
			},
			expectThrottle: false,
			expectAuth:     true,
		},
		{
			name: "429 is throttle, not auth error",
			err: &azcore.ResponseError{
				StatusCode: http.StatusTooManyRequests,
			},
			expectThrottle: true,
			expectAuth:     false,
		},
		{
			name:           "message with both keywords prefers first match",
			err:            errors.New("429 unauthorized"),
			expectThrottle: true,
			expectAuth:     true, // Both match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectThrottle, IsAzureThrottled(tt.err))
			assert.Equal(t, tt.expectAuth, IsAzureAuthError(tt.err))
		})
	}
}
