package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear all environment variables
	clearEnv()

	cfg, err := LoadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, cfg)

	// Verify defaults
	assert.Equal(t, "INFO", cfg.LogLevel)
	assert.Equal(t, "text", cfg.LogFormat)
	assert.Equal(t, 5*time.Minute, cfg.SyncInterval)
	assert.Equal(t, 5, cfg.WorkerCount)
	assert.Equal(t, int64(3600), cfg.TokenExpirationSeconds)
	assert.Equal(t, 0.8, cfg.TokenRenewalThreshold)
	assert.Equal(t, 0.8, cfg.AzureTokenRenewalThreshold)
	assert.Equal(t, 8080, cfg.HealthCheckPort)
	assert.Equal(t, ":9090", cfg.MetricsBindAddress)
	assert.Equal(t, float32(10.0), cfg.KubernetesQPS)
	assert.Equal(t, 20, cfg.KubernetesBurst)
	assert.Equal(t, 5, cfg.AzureCircuitBreakerThreshold)
	assert.Equal(t, 1*time.Minute, cfg.AzureCircuitBreakerTimeout)
	assert.Equal(t, "api://AzureADTokenExchange", cfg.TokenAudience)
	assert.Equal(t, "https://vault.azure.net/.default", cfg.KeyVaultScope)
}

func TestLoadConfig_CustomValues(t *testing.T) {
	clearEnv()

	// Set custom environment variables
    _ = os.Setenv("LOG_LEVEL", "DEBUG")
    _ = os.Setenv("LOG_FORMAT", "json")
    _ = os.Setenv("SYNC_INTERVAL", "10m")
    _ = os.Setenv("WORKER_COUNT", "10")
    _ = os.Setenv("TOKEN_EXPIRATION_SECONDS", "7200")
    _ = os.Setenv("TOKEN_RENEWAL_THRESHOLD", "0.9")
    _ = os.Setenv("AZURE_TOKEN_RENEWAL_THRESHOLD", "0.85")
    _ = os.Setenv("HEALTH_CHECK_PORT", "9090")
    _ = os.Setenv("METRICS_BIND_ADDRESS", ":8081")
    _ = os.Setenv("KUBERNETES_QPS", "20.0")
    _ = os.Setenv("KUBERNETES_BURST", "50")
    _ = os.Setenv("AZURE_CIRCUIT_BREAKER_THRESHOLD", "7")
    _ = os.Setenv("AZURE_CIRCUIT_BREAKER_TIMEOUT", "2m")
    _ = os.Setenv("TOKEN_AUDIENCE", "custom://audience")
    _ = os.Setenv("KEYVAULT_SCOPE", "https://custom.vault.azure.net/.default")
	defer clearEnv()

	cfg, err := LoadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, cfg)

	assert.Equal(t, "DEBUG", cfg.LogLevel)
	assert.Equal(t, "json", cfg.LogFormat)
	assert.Equal(t, 10*time.Minute, cfg.SyncInterval)
	assert.Equal(t, 10, cfg.WorkerCount)
	assert.Equal(t, int64(7200), cfg.TokenExpirationSeconds)
	assert.Equal(t, 0.9, cfg.TokenRenewalThreshold)
	assert.Equal(t, 0.85, cfg.AzureTokenRenewalThreshold)
	assert.Equal(t, 9090, cfg.HealthCheckPort)
	assert.Equal(t, ":8081", cfg.MetricsBindAddress)
	assert.Equal(t, float32(20.0), cfg.KubernetesQPS)
	assert.Equal(t, 50, cfg.KubernetesBurst)
	assert.Equal(t, 7, cfg.AzureCircuitBreakerThreshold)
	assert.Equal(t, 2*time.Minute, cfg.AzureCircuitBreakerTimeout)
	assert.Equal(t, "custom://audience", cfg.TokenAudience)
	assert.Equal(t, "https://custom.vault.azure.net/.default", cfg.KeyVaultScope)
}

func TestValidate_LogLevel(t *testing.T) {
	tests := []struct {
		name        string
		logLevel    string
		expectError bool
	}{
		{"valid DEBUG", "DEBUG", false},
		{"valid INFO", "INFO", false},
		{"valid WARN", "WARN", false},
		{"valid WARNING", "WARNING", false},
		{"valid ERROR", "ERROR", false},
		{"valid lowercase", "info", false}, // Should be normalized to uppercase
		{"invalid level", "TRACE", true},
		{"invalid level", "VERBOSE", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.LogLevel = tt.logLevel

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "LOG_LEVEL")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_LogFormat(t *testing.T) {
	tests := []struct {
		name        string
		logFormat   string
		expectError bool
	}{
		{"valid text", "text", false},
		{"valid json", "json", false},
		{"valid uppercase", "JSON", false}, // Should be normalized
		{"invalid format", "xml", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.LogFormat = tt.logFormat

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "LOG_FORMAT")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_SyncInterval(t *testing.T) {
	tests := []struct {
		name         string
		syncInterval time.Duration
		expectError  bool
		errorText    string
	}{
		{"valid 5 minutes", 5 * time.Minute, false, ""},
		{"valid 1 hour", 1 * time.Hour, false, ""},
		{"minimum 30 seconds", 30 * time.Second, false, ""},
		{"too short", 15 * time.Second, true, "at least 30s"},
		{"zero", 0, true, "must be positive"},
		{"negative", -1 * time.Minute, true, "must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.SyncInterval = tt.syncInterval

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "SYNC_INTERVAL")
				if tt.errorText != "" {
					assert.Contains(t, err.Error(), tt.errorText)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_WorkerCount(t *testing.T) {
	tests := []struct {
		name        string
		workerCount int
		expectError bool
		errorText   string
	}{
		{"valid 1", 1, false, ""},
		{"valid 5", 5, false, ""},
		{"valid 100", 100, false, ""},
		{"too low", 0, true, "at least 1"},
		{"negative", -1, true, "at least 1"},
		{"too high", 101, true, "at most 100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.WorkerCount = tt.workerCount

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "WORKER_COUNT")
				if tt.errorText != "" {
					assert.Contains(t, err.Error(), tt.errorText)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_TokenExpirationSeconds(t *testing.T) {
	tests := []struct {
		name        string
		expiration  int64
		expectError bool
		errorText   string
	}{
		{"valid 1 hour", 3600, false, ""},
		{"minimum 60", 60, false, ""},
		{"maximum 24 hours", 86400, false, ""},
		{"too low", 59, true, "at least 60"},
		{"too high", 86401, true, "at most 86400"},
		{"zero", 0, true, "at least 60"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.TokenExpirationSeconds = tt.expiration

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "TOKEN_EXPIRATION_SECONDS")
				if tt.errorText != "" {
					assert.Contains(t, err.Error(), tt.errorText)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_RenewalThresholds(t *testing.T) {
	tests := []struct {
		name        string
		threshold   float64
		expectError bool
	}{
		{"valid 0.8", 0.8, false},
		{"valid 0.5", 0.5, false},
		{"valid 0.9", 0.9, false},
		{"valid 0.01", 0.01, false},
		{"valid 0.99", 0.99, false},
		{"zero", 0.0, true},
		{"one", 1.0, true},
		{"negative", -0.1, true},
		{"greater than one", 1.1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_token", func(t *testing.T) {
			cfg := getValidConfig()
			cfg.TokenRenewalThreshold = tt.threshold

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "TOKEN_RENEWAL_THRESHOLD")
			} else {
				assert.NoError(t, err)
			}
		})

		t.Run(tt.name+"_azure", func(t *testing.T) {
			cfg := getValidConfig()
			cfg.AzureTokenRenewalThreshold = tt.threshold

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "AZURE_TOKEN_RENEWAL_THRESHOLD")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_HealthCheckPort(t *testing.T) {
	tests := []struct {
		name        string
		port        int
		expectError bool
	}{
		{"valid 8080", 8080, false},
		{"valid 80", 80, false},
		{"valid 443", 443, false},
		{"valid 65535", 65535, false},
		{"minimum 1", 1, false},
		{"zero", 0, true},
		{"negative", -1, true},
		{"too high", 65536, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.HealthCheckPort = tt.port

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "HEALTH_CHECK_PORT")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_MetricsBindAddress(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		expectError bool
	}{
		{"valid :9090", ":9090", false},
		{"valid :8080", ":8080", false},
		{"valid 0.0.0.0:9090", "0.0.0.0:9090", false},
		{"valid localhost:9090", "localhost:9090", false},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.MetricsBindAddress = tt.address

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "METRICS_BIND_ADDRESS")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_KubernetesQPS(t *testing.T) {
	tests := []struct {
		name        string
		qps         float32
		expectError bool
	}{
		{"valid 10.0", 10.0, false},
		{"valid 0.1", 0.1, false},
		{"valid 100", 100, false},
		{"zero", 0, true},
		{"negative", -1, true},
		{"too high", 101, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.KubernetesQPS = tt.qps

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "KUBERNETES_QPS")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_KubernetesBurst(t *testing.T) {
	tests := []struct {
		name        string
		burst       int
		expectError bool
	}{
		{"valid 20", 20, false},
		{"minimum 1", 1, false},
		{"maximum 200", 200, false},
		{"zero", 0, true},
		{"negative", -1, true},
		{"too high", 201, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.KubernetesBurst = tt.burst

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "KUBERNETES_BURST")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_AzureCircuitBreakerThreshold(t *testing.T) {
	tests := []struct {
		name        string
		threshold   int
		expectError bool
	}{
		{"valid 5", 5, false},
		{"minimum 3", 3, false},
		{"maximum 10", 10, false},
		{"too low", 2, true},
		{"too high", 11, true},
		{"zero", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.AzureCircuitBreakerThreshold = tt.threshold

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "AZURE_CIRCUIT_BREAKER_THRESHOLD")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_AzureCircuitBreakerTimeout(t *testing.T) {
	tests := []struct {
		name        string
		timeout     time.Duration
		expectError bool
	}{
		{"valid 1 minute", 1 * time.Minute, false},
		{"minimum 30 seconds", 30 * time.Second, false},
		{"maximum 5 minutes", 5 * time.Minute, false},
		{"too short", 29 * time.Second, true},
		{"too long", 6 * time.Minute, true},
		{"zero", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.AzureCircuitBreakerTimeout = tt.timeout

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "AZURE_CIRCUIT_BREAKER_TIMEOUT")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_AzureConfiguration(t *testing.T) {
	tests := []struct {
		name          string
		tokenAudience string
		keyVaultScope string
		expectError   bool
		errorText     string
	}{
		{"valid both", "api://AzureADTokenExchange", "https://vault.azure.net/.default", false, ""},
		{"empty audience", "", "https://vault.azure.net/.default", true, "TOKEN_AUDIENCE"},
		{"empty scope", "api://AzureADTokenExchange", "", true, "KEYVAULT_SCOPE"},
		{"both empty", "", "", true, "TOKEN_AUDIENCE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := getValidConfig()
			cfg.TokenAudience = tt.tokenAudience
			cfg.KeyVaultScope = tt.keyVaultScope

			err := cfg.Validate()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorText)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseIntEnv(t *testing.T) {
	clearEnv()
	defer clearEnv()

	// Test default
	assert.Equal(t, 42, parseIntEnv("TEST_INT", 42))

	// Test valid value
    _ = os.Setenv("TEST_INT", "100")
	assert.Equal(t, 100, parseIntEnv("TEST_INT", 42))

	// Test invalid value (should return default)
    _ = os.Setenv("TEST_INT", "invalid")
	assert.Equal(t, 42, parseIntEnv("TEST_INT", 42))
}

func TestParseFloat32Env(t *testing.T) {
	clearEnv()
	defer clearEnv()

	// Test default
	assert.Equal(t, float32(3.14), parseFloat32Env("TEST_FLOAT", 3.14))

	// Test valid value
    _ = os.Setenv("TEST_FLOAT", "2.71")
	assert.Equal(t, float32(2.71), parseFloat32Env("TEST_FLOAT", 3.14))

	// Test invalid value (should return default)
    _ = os.Setenv("TEST_FLOAT", "invalid")
	assert.Equal(t, float32(3.14), parseFloat32Env("TEST_FLOAT", 3.14))
}

func TestParseDurationEnv(t *testing.T) {
	clearEnv()
	defer clearEnv()

	// Test default
	assert.Equal(t, 5*time.Minute, parseDurationEnv("TEST_DURATION", 5*time.Minute))

	// Test valid value
    _ = os.Setenv("TEST_DURATION", "10m")
	assert.Equal(t, 10*time.Minute, parseDurationEnv("TEST_DURATION", 5*time.Minute))

	// Test invalid value (should return default)
    _ = os.Setenv("TEST_DURATION", "invalid")
	assert.Equal(t, 5*time.Minute, parseDurationEnv("TEST_DURATION", 5*time.Minute))
}

// Helper functions

func getValidConfig() *Config {
	return &Config{
		LogLevel:                     "INFO",
		LogFormat:                    "text",
		SyncInterval:                 5 * time.Minute,
		WorkerCount:                  5,
		TokenExpirationSeconds:       3600,
		TokenRenewalThreshold:        0.8,
		AzureTokenRenewalThreshold:   0.8,
		HealthCheckPort:              8080,
		MetricsBindAddress:           ":9090",
		KubernetesQPS:                10.0,
		KubernetesBurst:              20,
		AzureCircuitBreakerThreshold: 5,
		AzureCircuitBreakerTimeout:   1 * time.Minute,
		TokenAudience:                "api://AzureADTokenExchange",
		KeyVaultScope:                "https://vault.azure.net/.default",
	}
}

func clearEnv() {
	envVars := []string{
		"LOG_LEVEL", "LOG_FORMAT", "SYNC_INTERVAL", "WORKER_COUNT",
		"TOKEN_EXPIRATION_SECONDS", "TOKEN_RENEWAL_THRESHOLD",
		"AZURE_TOKEN_RENEWAL_THRESHOLD", "HEALTH_CHECK_PORT", "METRICS_BIND_ADDRESS",
		"KUBERNETES_QPS", "KUBERNETES_BURST",
		"AZURE_CIRCUIT_BREAKER_THRESHOLD", "AZURE_CIRCUIT_BREAKER_TIMEOUT",
		"TOKEN_AUDIENCE", "KEYVAULT_SCOPE",
		"TEST_INT", "TEST_FLOAT", "TEST_DURATION",
	}
    for _, env := range envVars {
        _ = os.Unsetenv(env)
    }
}
