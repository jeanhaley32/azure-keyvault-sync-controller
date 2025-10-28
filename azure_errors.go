package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// IsAzureThrottled checks if an error is due to Azure API throttling (429 Too Many Requests).
// Azure Key Vault has rate limits of 2000 requests per 10 seconds for secrets.
func IsAzureThrottled(err error) bool {
	if err == nil {
		return false
	}

	// Check for ResponseError which is the standard Azure SDK error type
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) {
		// Check for 429 status code
		if responseErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
	}

	// Fallback: Check error message for throttling indicators
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "429") ||
		strings.Contains(errMsg, "too many requests") ||
		strings.Contains(errMsg, "throttled") ||
		strings.Contains(errMsg, "rate limit")
}

// ExtractRetryAfter attempts to extract the Retry-After duration from an Azure error.
// Returns the suggested wait duration, or a default of 10 seconds if not found.
//
// Azure includes a Retry-After header in 429 responses that indicates how long
// to wait before retrying the request.
func ExtractRetryAfter(err error) time.Duration {
	const defaultRetry = 10 * time.Second

	if err == nil {
		return defaultRetry
	}

	// Check for ResponseError which contains HTTP headers
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) {
		// Try to get Retry-After header from the raw response
		if responseErr.RawResponse != nil {
			retryAfterHeader := responseErr.RawResponse.Header.Get("Retry-After")
			if retryAfterHeader != "" {
				// Retry-After can be either seconds (integer) or HTTP-date
				// Try parsing as seconds first
				if seconds, err := strconv.Atoi(retryAfterHeader); err == nil {
					return time.Duration(seconds) * time.Second
				}

				// Try parsing as HTTP-date
				if httpDate, err := time.Parse(http.TimeFormat, retryAfterHeader); err == nil {
					duration := time.Until(httpDate)
					if duration > 0 {
						return duration
					}
				}
			}
		}
	}

	// Default to 10 seconds if we can't extract Retry-After
	return defaultRetry
}

// IsAzureAuthError checks if an error is related to Azure authentication/authorization.
// These errors should not trigger the circuit breaker since they indicate a configuration
// problem rather than a transient failure.
func IsAzureAuthError(err error) bool {
	if err == nil {
		return false
	}

	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) {
		// 401 Unauthorized, 403 Forbidden
		if responseErr.StatusCode == http.StatusUnauthorized ||
			responseErr.StatusCode == http.StatusForbidden {
			return true
		}
	}

	// Check error message for auth-related keywords
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "unauthorized") ||
		strings.Contains(errMsg, "forbidden") ||
		strings.Contains(errMsg, "authentication") ||
		strings.Contains(errMsg, "permission")
}
