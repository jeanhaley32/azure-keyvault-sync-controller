package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration parameters for the controller
type Config struct {
	// Logging configuration
	LogLevel  string
	LogFormat string

	// Controller behavior
	SyncInterval             time.Duration
	WorkerCount              int
	TokenExpirationSeconds   int64
	TokenRenewalThreshold    float64
	AzureTokenRenewalThreshold float64

	// Server configuration
	HealthCheckPort int

	// Kubernetes API rate limiting
	KubernetesQPS   float32
	KubernetesBurst int

	// Azure circuit breaker configuration
	AzureCircuitBreakerThreshold int
	AzureCircuitBreakerTimeout   time.Duration

	// Azure configuration
	TokenAudience string
	KeyVaultScope string
}

// LoadConfig loads configuration from environment variables with defaults
func LoadConfig() (*Config, error) {
	cfg := &Config{
		// Logging defaults
		LogLevel:  getEnv("LOG_LEVEL", "INFO"),
		LogFormat: getEnv("LOG_FORMAT", "text"),

		// Controller defaults
		SyncInterval:             parseDurationEnv("SYNC_INTERVAL", 5*time.Minute),
		WorkerCount:              parseIntEnv("WORKER_COUNT", 5),
		TokenExpirationSeconds:   parseInt64Env("TOKEN_EXPIRATION_SECONDS", 3600),
		TokenRenewalThreshold:    parseFloatEnv("TOKEN_RENEWAL_THRESHOLD", 0.8),
		AzureTokenRenewalThreshold: parseFloatEnv("AZURE_TOKEN_RENEWAL_THRESHOLD", 0.8),

		// Server defaults
		HealthCheckPort: parseIntEnv("HEALTH_CHECK_PORT", 8080),

		// Kubernetes API rate limiting defaults
		KubernetesQPS:   parseFloat32Env("KUBERNETES_QPS", 10.0),
		KubernetesBurst: parseIntEnv("KUBERNETES_BURST", 20),

		// Azure circuit breaker defaults
		AzureCircuitBreakerThreshold: parseIntEnv("AZURE_CIRCUIT_BREAKER_THRESHOLD", 5),
		AzureCircuitBreakerTimeout:   parseDurationEnv("AZURE_CIRCUIT_BREAKER_TIMEOUT", 1*time.Minute),

		// Azure defaults (typically don't need to change these)
		TokenAudience: getEnv("TOKEN_AUDIENCE", "api://AzureADTokenExchange"),
		KeyVaultScope: getEnv("KEYVAULT_SCOPE", "https://vault.azure.net/.default"),
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// Validate checks that all configuration values are valid
func (c *Config) Validate() error {
	// Validate log level
	validLogLevels := []string{"DEBUG", "INFO", "WARN", "WARNING", "ERROR"}
	if !contains(validLogLevels, strings.ToUpper(c.LogLevel)) {
		return fmt.Errorf("invalid LOG_LEVEL '%s' (must be one of: %s)",
			c.LogLevel, strings.Join(validLogLevels, ", "))
	}

	// Validate log format
	validFormats := []string{"text", "json"}
	if !contains(validFormats, strings.ToLower(c.LogFormat)) {
		return fmt.Errorf("invalid LOG_FORMAT '%s' (must be: text or json)", c.LogFormat)
	}

	// Validate sync interval
	if c.SyncInterval <= 0 {
		return fmt.Errorf("SYNC_INTERVAL must be positive, got: %v", c.SyncInterval)
	}
	if c.SyncInterval < 30*time.Second {
		return fmt.Errorf("SYNC_INTERVAL must be at least 30s to avoid excessive API calls, got: %v",
			c.SyncInterval)
	}

	// Validate worker count
	if c.WorkerCount < 1 {
		return fmt.Errorf("WORKER_COUNT must be at least 1, got: %d", c.WorkerCount)
	}
	if c.WorkerCount > 100 {
		return fmt.Errorf("WORKER_COUNT must be at most 100, got: %d", c.WorkerCount)
	}

	// Validate token expiration
	if c.TokenExpirationSeconds < 60 {
		return fmt.Errorf("TOKEN_EXPIRATION_SECONDS must be at least 60, got: %d",
			c.TokenExpirationSeconds)
	}
	if c.TokenExpirationSeconds > 86400 {
		return fmt.Errorf("TOKEN_EXPIRATION_SECONDS must be at most 86400 (24 hours), got: %d",
			c.TokenExpirationSeconds)
	}

	// Validate renewal thresholds
	if c.TokenRenewalThreshold <= 0 || c.TokenRenewalThreshold >= 1 {
		return fmt.Errorf("TOKEN_RENEWAL_THRESHOLD must be between 0 and 1, got: %f",
			c.TokenRenewalThreshold)
	}
	if c.AzureTokenRenewalThreshold <= 0 || c.AzureTokenRenewalThreshold >= 1 {
		return fmt.Errorf("AZURE_TOKEN_RENEWAL_THRESHOLD must be between 0 and 1, got: %f",
			c.AzureTokenRenewalThreshold)
	}

	// Validate health check port
	if c.HealthCheckPort < 1 || c.HealthCheckPort > 65535 {
		return fmt.Errorf("HEALTH_CHECK_PORT must be between 1-65535, got: %d", c.HealthCheckPort)
	}

	// Validate Kubernetes API rate limits
	if c.KubernetesQPS <= 0 || c.KubernetesQPS > 100 {
		return fmt.Errorf("KUBERNETES_QPS must be between 0-100, got: %f", c.KubernetesQPS)
	}
	if c.KubernetesBurst < 1 || c.KubernetesBurst > 200 {
		return fmt.Errorf("KUBERNETES_BURST must be between 1-200, got: %d", c.KubernetesBurst)
	}

	// Validate Azure circuit breaker configuration
	if c.AzureCircuitBreakerThreshold < 3 || c.AzureCircuitBreakerThreshold > 10 {
		return fmt.Errorf("AZURE_CIRCUIT_BREAKER_THRESHOLD must be between 3-10, got: %d",
			c.AzureCircuitBreakerThreshold)
	}
	if c.AzureCircuitBreakerTimeout < 30*time.Second || c.AzureCircuitBreakerTimeout > 5*time.Minute {
		return fmt.Errorf("AZURE_CIRCUIT_BREAKER_TIMEOUT must be between 30s-5m, got: %v",
			c.AzureCircuitBreakerTimeout)
	}

	// Validate Azure configuration
	if c.TokenAudience == "" {
		return fmt.Errorf("TOKEN_AUDIENCE cannot be empty")
	}
	if c.KeyVaultScope == "" {
		return fmt.Errorf("KEYVAULT_SCOPE cannot be empty")
	}

	return nil
}

// Helper functions for parsing environment variables

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseIntEnv(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func parseInt64Env(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func parseFloatEnv(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func parseFloat32Env(key string, defaultValue float32) float32 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseFloat(value, 32); err == nil {
			return float32(parsed)
		}
	}
	return defaultValue
}

func parseDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
