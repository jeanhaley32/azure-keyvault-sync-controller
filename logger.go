package main

import (
	"log/slog"
	"os"
	"strings"
)

var logger *slog.Logger

// InitLogger initializes the global structured logger
func InitLogger() {
	// Get log level from environment variable (default: INFO)
	logLevel := getLogLevel(os.Getenv("LOG_LEVEL"))

	// Get log format from environment variable (default: text)
	logFormat := strings.ToLower(os.Getenv("LOG_FORMAT"))

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
		"level", logLevel.String(),
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
		InitLogger()
	}
	return logger
}
