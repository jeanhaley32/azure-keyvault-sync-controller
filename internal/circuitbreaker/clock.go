package circuitbreaker

import "time"

// Clock provides an interface for time operations to enable deterministic testing
type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
}

// realClock implements Clock using actual time operations
type realClock struct{}

// NewRealClock returns a Clock that uses real system time
func NewRealClock() Clock {
	return &realClock{}
}

func (c *realClock) Now() time.Time {
	return time.Now()
}

func (c *realClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}
