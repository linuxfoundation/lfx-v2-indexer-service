// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package logging provides structured logging functionality for the LFX indexer service.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"strconv"

	slogotel "github.com/remychantenay/slog-otel"
)

type contextKey string

const (
	requestLoggerKey contextKey = "request_logger"
	requestIDKey     contextKey = "request_id"
)

// NewRequestID generates a simple 16-character request ID
func NewRequestID() string {
	bytes := make([]byte, 8)
	_, _ = rand.Read(bytes) // Ignore error - crypto/rand.Read only fails on system issues
	return hex.EncodeToString(bytes)
}

// NewLogger creates a logger with container-optimized defaults (JSON, Info level)
// Reads LOG_FORMAT and LOG_LEVEL from environment variables
// Optional debug parameter overrides environment LOG_LEVEL and enables AddSource
func NewLogger(debug ...bool) *slog.Logger {
	// Default to JSON format (container-friendly)
	format := GetEnvOrDefault("LOG_FORMAT", "json")

	opts := &slog.HandlerOptions{}

	// Check if debug override is provided
	if len(debug) > 0 && debug[0] {
		opts.Level = slog.LevelDebug
		opts.AddSource = true // Enable source location in debug mode
	} else {
		// Use configured log level from environment
		level := GetEnvOrDefault("LOG_LEVEL", "info")
		opts.Level = parseLogLevel(level)
	}

	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	// Wrap with slog-otel handler to add trace_id and span_id from context
	otelHandler := slogotel.OtelHandler{Next: handler}

	logger := slog.New(otelHandler)
	slog.SetDefault(logger) // Set as default for any slog.Default() calls
	return logger
}

// GetEnvOrDefault returns environment variable value or default if not set
func GetEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// GetEnvBool returns environment variable as boolean or default if not set/invalid
func GetEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
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
