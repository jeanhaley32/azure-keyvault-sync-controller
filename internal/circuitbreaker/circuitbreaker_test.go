package circuitbreaker

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(5, 1*time.Minute)

	assert.NotNil(t, cb)
	assert.Equal(t, "closed", cb.State())
	assert.Equal(t, 0, cb.Failures())
}

func TestCircuitBreaker_ClosedState(t *testing.T) {
	cb := NewCircuitBreaker(3, 1*time.Minute)

	// Successful calls should pass through
	err := cb.Call(func() error {
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "closed", cb.State())
	assert.Equal(t, 0, cb.Failures())
}

func TestCircuitBreaker_FailureCountIncrement(t *testing.T) {
	cb := NewCircuitBreaker(5, 1*time.Minute)

	// First failure
	err := cb.Call(func() error {
		return errors.New("failure 1")
	})
	assert.Error(t, err)
	assert.Equal(t, "closed", cb.State())
	assert.Equal(t, 1, cb.Failures())

	// Second failure
	err = cb.Call(func() error {
		return errors.New("failure 2")
	})
	assert.Error(t, err)
	assert.Equal(t, "closed", cb.State())
	assert.Equal(t, 2, cb.Failures())
}

func TestCircuitBreaker_OpenAfterMaxFailures(t *testing.T) {
	maxFailures := 3
	cb := NewCircuitBreaker(maxFailures, 1*time.Minute)

	// Trigger maxFailures
	for i := 0; i < maxFailures; i++ {
		err := cb.Call(func() error {
			return errors.New("service unavailable")
		})
		assert.Error(t, err)
	}

	// Circuit should be open now
	assert.Equal(t, "open", cb.State())
	assert.Equal(t, maxFailures, cb.Failures())

	// Next call should fail fast without executing function
	functionCalled := false
	err := cb.Call(func() error {
		functionCalled = true
		return nil
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker is open")
	assert.False(t, functionCalled, "function should not be called when circuit is open")
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	maxFailures := 3
	resetTimeout := 100 * time.Millisecond
	clock := newFakeClock(time.Now())
	cb := NewCircuitBreakerWithClock(maxFailures, resetTimeout, clock)

	// Open the circuit
	for i := 0; i < maxFailures; i++ {
		_ = cb.Call(func() error {
			return errors.New("fail")
		})
	}
	assert.Equal(t, "open", cb.State())

	// Advance time past reset timeout
	clock.Advance(resetTimeout + 10*time.Millisecond)

	// Next call should transition to half-open
	functionCalled := false
	err := cb.Call(func() error {
		functionCalled = true
		return nil // Success
	})

	assert.NoError(t, err)
	assert.True(t, functionCalled, "function should be called in half-open state")
	assert.Equal(t, "closed", cb.State(), "circuit should close after successful half-open call")
	assert.Equal(t, 0, cb.Failures())
}

func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	maxFailures := 2
	resetTimeout := 50 * time.Millisecond
	clock := newFakeClock(time.Now())
	cb := NewCircuitBreakerWithClock(maxFailures, resetTimeout, clock)

	// Open the circuit
	for i := 0; i < maxFailures; i++ {
		_ = cb.Call(func() error {
			return errors.New("fail")
		})
	}
	assert.Equal(t, "open", cb.State())

	// Advance time past reset timeout
	clock.Advance(resetTimeout + 10*time.Millisecond)

	// Try to call but fail multiple times - need maxFailures to reopen
	for i := 0; i < maxFailures; i++ {
		err := cb.Call(func() error {
			return errors.New("still broken")
		})
		assert.Error(t, err)
	}

	assert.Equal(t, "open", cb.State(), "circuit should reopen after maxFailures in half-open state")
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(5, 1*time.Minute)

	// Record some failures
    for i := 0; i < 3; i++ {
        _ = cb.Call(func() error {
			return errors.New("fail")
		})
	}
	assert.Equal(t, 3, cb.Failures())

	// Successful call should reset failure count
	err := cb.Call(func() error {
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 0, cb.Failures())
	assert.Equal(t, "closed", cb.State())
}

func TestCircuitBreaker_OpenStateFastFail(t *testing.T) {
	cb := NewCircuitBreaker(2, 1*time.Second)

	// Open the circuit
    _ = cb.Call(func() error { return errors.New("fail") })
    _ = cb.Call(func() error { return errors.New("fail") })
	assert.Equal(t, "open", cb.State())

	// Multiple calls should all fail fast
	for i := 0; i < 5; i++ {
		start := time.Now()
		err := cb.Call(func() error {
			time.Sleep(100 * time.Millisecond) // This should never execute
			return nil
		})
		duration := time.Since(start)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "circuit breaker is open")
		// Should fail immediately, not wait for the sleep
		assert.Less(t, duration, 50*time.Millisecond, "should fail fast without executing function")
	}
}

func TestCircuitBreaker_RemainingTimeInError(t *testing.T) {
	resetTimeout := 2 * time.Second
	cb := NewCircuitBreaker(2, resetTimeout)

	// Open the circuit
    _ = cb.Call(func() error { return errors.New("fail") })
    _ = cb.Call(func() error { return errors.New("fail") })

	// Immediately try to call - error should indicate remaining time
	err := cb.Call(func() error {
		return nil
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "will retry in")
}

func TestCircuitBreaker_ManualReset(t *testing.T) {
	cb := NewCircuitBreaker(2, 1*time.Minute)

	// Open the circuit
    _ = cb.Call(func() error { return errors.New("fail") })
    _ = cb.Call(func() error { return errors.New("fail") })
	assert.Equal(t, "open", cb.State())
	assert.Equal(t, 2, cb.Failures())

	// Manually reset
	cb.Reset()

	assert.Equal(t, "closed", cb.State())
	assert.Equal(t, 0, cb.Failures())

	// Should be able to call successfully now
	err := cb.Call(func() error {
		return nil
	})
	assert.NoError(t, err)
}

func TestCircuitBreaker_ConcurrentCalls(t *testing.T) {
	cb := NewCircuitBreaker(10, 500*time.Millisecond)
	var wg sync.WaitGroup
	numGoroutines := 50
	successCount := 0
	failureCount := 0
	var mu sync.Mutex

	// Launch concurrent goroutines
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Alternate between success and failure
            err := cb.Call(func() error {
				time.Sleep(10 * time.Millisecond) // Simulate work
				if id%2 == 0 {
					return nil
				}
				return errors.New("intermittent failure")
			})

			mu.Lock()
			if err == nil {
				successCount++
			} else {
				failureCount++
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Verify counts (should total to numGoroutines)
	assert.Equal(t, numGoroutines, successCount+failureCount)

	// Circuit breaker should still be functional
	state := cb.State()
	assert.Contains(t, []string{"closed", "open", "half-open"}, state)
}

func TestCircuitBreaker_RaceCondition(t *testing.T) {
	// This test is specifically for running with -race flag
	cb := NewCircuitBreaker(5, 100*time.Millisecond)
	var wg sync.WaitGroup

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.State()
			cb.Failures()
		}()
	}

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
            _ = cb.Call(func() error {
				if id%3 == 0 {
					return errors.New("fail")
				}
				return nil
			})
		}(i)
	}

	wg.Wait()

	// If we get here without race detector errors, test passes
	assert.NotNil(t, cb)
}

func TestCircuitBreaker_MultipleHalfOpenAttempts(t *testing.T) {
	maxFailures := 2
	resetTimeout := 50 * time.Millisecond
	clock := newFakeClock(time.Now())
	cb := NewCircuitBreakerWithClock(maxFailures, resetTimeout, clock)

	// Open the circuit
	for i := 0; i < maxFailures; i++ {
		_ = cb.Call(func() error { return errors.New("fail") })
	}
	assert.Equal(t, "open", cb.State())

	// First half-open attempts fail (need maxFailures to reopen)
	clock.Advance(resetTimeout + 10*time.Millisecond)
	for i := 0; i < maxFailures; i++ {
		err := cb.Call(func() error {
			return errors.New("still failing")
		})
		assert.Error(t, err)
	}
	assert.Equal(t, "open", cb.State())

	// Second half-open attempt succeeds
	clock.Advance(resetTimeout + 10*time.Millisecond)
	err := cb.Call(func() error {
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "closed", cb.State())
}

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	resetTimeout := 100 * time.Millisecond
	clock := newFakeClock(time.Now())
	cb := NewCircuitBreakerWithClock(2, resetTimeout, clock)

	// Start: closed
	assert.Equal(t, "closed", cb.State())

	// closed → closed (success)
	_ = cb.Call(func() error { return nil })
	assert.Equal(t, "closed", cb.State())

	// closed → closed (first failure)
	_ = cb.Call(func() error { return errors.New("fail 1") })
	assert.Equal(t, "closed", cb.State())

	// closed → open (second failure)
	_ = cb.Call(func() error { return errors.New("fail 2") })
	assert.Equal(t, "open", cb.State())

	// open → open (fast fail before timeout)
	_ = cb.Call(func() error { return nil })
	assert.Equal(t, "open", cb.State())

	// Advance time past timeout
	clock.Advance(150 * time.Millisecond)

	// open → half-open → closed (success after timeout)
	_ = cb.Call(func() error { return nil })
	assert.Equal(t, "closed", cb.State())
}

func TestCircuitBreaker_ExactThreshold(t *testing.T) {
	maxFailures := 3
	cb := NewCircuitBreaker(maxFailures, 1*time.Minute)

	// Should stay closed for maxFailures - 1
	for i := 0; i < maxFailures-1; i++ {
        _ = cb.Call(func() error { return errors.New("fail") })
		assert.Equal(t, "closed", cb.State(), "circuit should still be closed")
	}

	// Exactly maxFailures should open it
    _ = cb.Call(func() error { return errors.New("fail") })
	assert.Equal(t, "open", cb.State(), "circuit should be open after exactly maxFailures")
}

func TestCircuitBreaker_ZeroFailuresAfterSuccess(t *testing.T) {
	cb := NewCircuitBreaker(5, 1*time.Minute)

	// Accumulate failures
    _ = cb.Call(func() error { return errors.New("fail 1") })
    _ = cb.Call(func() error { return errors.New("fail 2") })
    _ = cb.Call(func() error { return errors.New("fail 3") })
	assert.Equal(t, 3, cb.Failures())

	// Success should reset to zero
    _ = cb.Call(func() error { return nil })
	assert.Equal(t, 0, cb.Failures())

	// New failure should start counting from 0 again
    _ = cb.Call(func() error { return errors.New("fail 4") })
	assert.Equal(t, 1, cb.Failures())
}
