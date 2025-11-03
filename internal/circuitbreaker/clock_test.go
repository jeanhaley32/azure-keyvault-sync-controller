package circuitbreaker

import (
	"sync"
	"time"
)

// fakeClock implements Clock for deterministic testing
type fakeClock struct {
	mu      sync.RWMutex
	current time.Time
}

// newFakeClock creates a new fake clock starting at the given time
func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{
		current: start,
	}
}

func (c *fakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

func (c *fakeClock) Since(t time.Time) time.Duration {
	return c.Now().Sub(t)
}

// Advance moves the clock forward by the given duration
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = c.current.Add(d)
}
