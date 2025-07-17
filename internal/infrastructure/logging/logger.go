package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
)

type contextKey string

const (
	requestLoggerKey contextKey = "request_logger"
	requestIDKey     contextKey = "request_id"
)

// NewRequestID generates a simple 16-character request ID
func NewRequestID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// NewLogger creates a logger with container-optimized defaults (JSON, Info level)
// Reads LOG_FORMAT and LOG_LEVEL from environment variables
func NewLogger() *slog.Logger {
	// Default to JSON format (container-friendly)
	format := getEnvOrDefault("LOG_FORMAT", "json")

	// Default to Info level (not too verbose)
	level := getEnvOrDefault("LOG_LEVEL", "info")

	opts := &slog.HandlerOptions{
		Level: parseLogLevel(level),
	}

	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// getEnvOrDefault returns environment variable value or default if not set
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// parseLogLevel converts string to slog.Level
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithRequestID creates context with request_id and enhanced logger
func WithRequestID(ctx context.Context, baseLogger *slog.Logger) (context.Context, *slog.Logger) {
	requestID := NewRequestID()

	// Create enhanced logger with request_id
	enhancedLogger := baseLogger.With("request_id", requestID)

	// Store both request_id and enhanced logger in context
	ctx = context.WithValue(ctx, requestIDKey, requestID)
	ctx = context.WithValue(ctx, requestLoggerKey, enhancedLogger)

	return ctx, enhancedLogger
}

// FromContext extracts the enhanced logger from context
func FromContext(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if logger, ok := ctx.Value(requestLoggerKey).(*slog.Logger); ok {
		return logger
	}
	return fallback
}

// WithComponent adds component field to logger
func WithComponent(logger *slog.Logger, component string) *slog.Logger {
	return logger.With("component", component)
}

// WithOperation adds operation field to logger
func WithOperation(logger *slog.Logger, operation string) *slog.Logger {
	return logger.With("operation", operation)
}

// WithFields adds multiple fields to logger
func WithFields(logger *slog.Logger, fields map[string]any) *slog.Logger {
	attrs := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		attrs = append(attrs, k, v)
	}
	return logger.With(attrs...)
}

// LogError logs an error with structured context
func LogError(logger *slog.Logger, msg string, err error, fields ...any) {
	attrs := []any{"error", err.Error()}
	attrs = append(attrs, fields...)
	logger.Error(msg, attrs...)
}

// GetRequestID extracts request_id from context
func GetRequestID(ctx context.Context) string {
	if requestID, ok := ctx.Value(requestIDKey).(string); ok {
		return requestID
	}
	return ""
}
