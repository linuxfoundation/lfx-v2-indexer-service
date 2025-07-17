package logging

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

// TestLogger creates a logger that captures output for testing
func TestLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	return logger, &buf
}

// TestContext creates a context with request_id for testing
func TestContext(t *testing.T, logger *slog.Logger) context.Context {
	ctx, _ := WithRequestID(context.Background(), logger)
	return ctx
}

// AssertLogContains checks if log output contains expected text
func AssertLogContains(t *testing.T, buf *bytes.Buffer, expected string) {
	if !bytes.Contains(buf.Bytes(), []byte(expected)) {
		t.Errorf("Expected log to contain %q, got: %s", expected, buf.String())
	}
}
