// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package logging

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	// Test with default values
	logger := NewLogger()
	if logger == nil {
		t.Fatal("Expected logger to be created, got nil")
	}
}

func TestNewLoggerWithEnvironment(t *testing.T) {
	// Test with custom environment variables
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("LOG_FORMAT", "text")
	defer func() {
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("LOG_FORMAT")
	}()

	logger := NewLogger()
	if logger == nil {
		t.Fatal("Expected logger to be created, got nil")
	}
}

func TestWithRequestID(t *testing.T) {
	logger, buf := TestLogger(t)

	ctx, enhancedLogger := WithRequestID(context.Background(), logger)

	// Log a test message
	enhancedLogger.Info("test message", "key", "value")

	// Verify request_id is present
	output := buf.String()
	if !strings.Contains(output, "request_id") {
		t.Errorf("Expected log to contain request_id, got: %s", output)
	}

	// Verify we can extract the logger from context
	loggerFromContext := FromContext(ctx, logger)
	if loggerFromContext == nil {
		t.Fatal("Expected to get logger from context")
	}
}

func TestWithComponent(t *testing.T) {
	logger, buf := TestLogger(t)

	componentLogger := WithComponent(logger, "test_component")
	componentLogger.Info("test message")

	AssertLogContains(t, buf, "test_component")
	AssertLogContains(t, buf, "test message")
}

func TestContextFlow(t *testing.T) {
	logger, buf := TestLogger(t)

	// Simulate the complete flow
	ctx := context.Background()

	// Step 1: NATS entry point creates request context
	ctx, requestLogger := WithRequestID(ctx, logger)
	requestLogger.Info("NATS message received", "subject", "test.subject")

	// Step 2: Service extracts logger from context and adds component
	serviceLogger := WithComponent(FromContext(ctx, logger), "test_service")
	serviceLogger.Info("Processing started", "operation", "test_op")

	// Verify both logs have request_id
	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) < 2 {
		t.Fatalf("Expected at least 2 log lines, got %d", len(lines))
	}

	// Both lines should contain the same request_id
	for _, line := range lines {
		if !strings.Contains(line, "request_id") {
			t.Errorf("Expected line to contain request_id: %s", line)
		}
	}

	// Second line should have component
	if !strings.Contains(lines[1], "test_service") {
		t.Errorf("Expected second line to contain component: %s", lines[1])
	}
}
