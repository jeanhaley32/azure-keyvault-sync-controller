package logger

import (
	"log/slog"
	"os"
	"strings"

	"github.com/jeanhaley32/azure-keyvault-sync-controller/internal/config"
)

var logger *slog.Logger

// InitLogger initializes the global structured logger
func InitLogger(cfg *config.Config) {
	// Get log level from configuration
	logLevel := getLogLevel(cfg.LogLevel)

	// Get log format from configuration
	logFormat := strings.ToLower(cfg.LogFormat)

	var handler slog.Handler

	if logFormat == "json" {
		// JSON format for production
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		})
	} else {
		// Text format for development (easier to read)
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		})
	}

	logger = slog.New(handler)
	slog.SetDefault(logger)

	logger.Info("Logger initialized",
		"configuredLevel", logLevel.String(),
		"format", logFormat)
}

// getLogLevel converts string to slog.Level
func getLogLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Logger returns the global logger instance
func Logger() *slog.Logger {
	if logger == nil {
		panic("logger not initialized - call InitLogger first")
	}
	return logger
}
