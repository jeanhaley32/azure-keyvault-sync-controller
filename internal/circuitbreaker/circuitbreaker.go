package circuitbreaker

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the circuit breaker is in open state
var ErrCircuitOpen = errors.New("circuit breaker is open")

// CircuitBreaker implements the circuit breaker pattern to protect against
// cascading failures when calling external services (Azure API).
//
// States:
// - closed: Normal operation, calls pass through
// - open: Failures exceeded threshold, all calls fail fast
// - half-open: Testing if service recovered, single call allowed
type CircuitBreaker struct {
	maxFailures  int
	resetTimeout time.Duration
	clock        Clock

	failures     int
	lastFailTime time.Time
	state        string // "closed", "open", "half-open"
	mu           sync.RWMutex
}

// NewCircuitBreaker creates a new circuit breaker with the specified parameters.
//
// maxFailures: Number of consecutive failures before opening the circuit
// resetTimeout: Duration to wait before attempting to close an open circuit
func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		clock:        NewRealClock(),
		state:        "closed",
	}
}

// NewCircuitBreakerWithClock creates a circuit breaker with a custom clock for testing
func NewCircuitBreakerWithClock(maxFailures int, resetTimeout time.Duration, clock Clock) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		clock:        clock,
		state:        "closed",
	}
}

// Call executes the provided function through the circuit breaker.
// Returns an error if the circuit is open or if the function fails.
func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check if circuit is open
	if cb.state == "open" {
		timeSinceFail := cb.clock.Since(cb.lastFailTime)
		if timeSinceFail > cb.resetTimeout {
			// Transition to half-open state
			slog.Debug("Circuit breaker transitioning to half-open",
				"timeSinceFail", timeSinceFail,
				"resetTimeout", cb.resetTimeout)
			cb.state = "half-open"
			cb.failures = 0
		} else {
			// Circuit still open, fail fast
			return fmt.Errorf("%w (will retry in %v)",
				ErrCircuitOpen, cb.resetTimeout-timeSinceFail)
		}
	}

	// Execute function
	err := fn()

	if err != nil {
		cb.failures++
		cb.lastFailTime = cb.clock.Now()

		if cb.failures >= cb.maxFailures {
			previousState := cb.state
			cb.state = "open"
			slog.Warn("Circuit breaker opened",
				"previousState", previousState,
				"failures", cb.failures,
				"maxFailures", cb.maxFailures,
				"resetTimeout", cb.resetTimeout)
		} else {
			slog.Debug("Circuit breaker recorded failure",
				"failures", cb.failures,
				"maxFailures", cb.maxFailures,
				"state", cb.state)
		}
		return err
	}

	// Success - reset circuit if in half-open state
	if cb.state == "half-open" {
		slog.Info("Circuit breaker closed after successful test",
			"previousFailures", cb.failures)
		cb.state = "closed"
	}
	cb.failures = 0
	return nil
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Failures returns the current failure count.
func (cb *CircuitBreaker) Failures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}

// Reset manually resets the circuit breaker to closed state.
// This should only be used for testing or administrative override.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	slog.Info("Circuit breaker manually reset")
	cb.state = "closed"
	cb.failures = 0
}
