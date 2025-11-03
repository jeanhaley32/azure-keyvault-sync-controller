package health

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// HealthChecker tracks the health state of the controller
type HealthChecker struct {
	mu              sync.RWMutex
	watchConnected  bool
	workersRunning  bool
	lastWatchUpdate time.Time
	startTime       time.Time
}

// NewHealthChecker creates a new health checker
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		watchConnected:  false,
		workersRunning:  false,
		lastWatchUpdate: time.Time{},
		startTime:       time.Now(),
	}
}

// SetWatchConnected marks the watch as connected/disconnected
func (h *HealthChecker) SetWatchConnected(connected bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.watchConnected = connected
	if connected {
		h.lastWatchUpdate = time.Now()
	}
}

// SetWorkersRunning marks workers as running/stopped
func (h *HealthChecker) SetWorkersRunning(running bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.workersRunning = running
}

// UpdateWatchActivity records watch activity (received event)
func (h *HealthChecker) UpdateWatchActivity() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastWatchUpdate = time.Now()
}

// IsReady returns true if controller is ready to serve traffic
func (h *HealthChecker) IsReady() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Controller is ready if watch is connected and workers are running
	return h.watchConnected && h.workersRunning
}

// GetStatus returns detailed health status
func (h *HealthChecker) GetStatus() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	status := map[string]interface{}{
		"watch_connected":   h.watchConnected,
		"workers_running":   h.workersRunning,
		"uptime_seconds":    time.Since(h.startTime).Seconds(),
	}

	if !h.lastWatchUpdate.IsZero() {
		status["last_watch_update"] = h.lastWatchUpdate.Format(time.RFC3339)
		status["seconds_since_watch_update"] = time.Since(h.lastWatchUpdate).Seconds()
	}

	return status
}

// HealthzHandler handles /healthz liveness probe
// Returns 200 OK if the process is alive
func HealthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
    if _, err := w.Write([]byte("ok")); err != nil {
        // Best-effort write for liveness probe
        slog.Debug("healthz write failed", "error", err)
    }
}

// ReadyzHandler handles /readyz readiness probe
// Returns 200 OK if controller is ready, 503 Service Unavailable if not
func ReadyzHandler(checker *HealthChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if checker.IsReady() {
			w.WriteHeader(http.StatusOK)
            if _, err := w.Write([]byte("ready")); err != nil {
                // Best-effort write for readiness probe
                slog.Debug("readyz write failed", "error", err)
            }
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			// Include status details in response for debugging
			status := checker.GetStatus()
            if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"ready":  false,
				"status": status,
            }); err != nil {
                slog.Debug("readyz encode failed", "error", err)
            }
		}
	}
}

// StatusHandler handles /status endpoint for debugging
// Returns detailed controller status
func StatusHandler(checker *HealthChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := checker.GetStatus()
		status["ready"] = checker.IsReady()
        if err := json.NewEncoder(w).Encode(status); err != nil {
            slog.Debug("status encode failed", "error", err)
        }
	}
}

// StartHealthCheckServer starts the HTTP server for health checks
// Returns the server instance for graceful shutdown control
func StartHealthCheckServer(addr string, checker *HealthChecker) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", HealthzHandler)
	mux.HandleFunc("/readyz", ReadyzHandler(checker))
	mux.HandleFunc("/status", StatusHandler(checker))

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in background and return instance immediately
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Health check server failed", "error", err)
		}
	}()

	return server, nil
}
