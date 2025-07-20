// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/auth"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockMessageHandler implements MessageHandler for testing
type MockMessageHandler struct {
	mock.Mock
}

func (m *MockMessageHandler) Handle(ctx context.Context, data []byte, subject string) error {
	args := m.Called(ctx, data, subject)
	return args.Error(0)
}

// MockMessageHandlerWithReply implements MessageHandlerWithReply for testing
type MockMessageHandlerWithReply struct {
	mock.Mock
}

func (m *MockMessageHandlerWithReply) HandleWithReply(ctx context.Context, data []byte, subject string, reply func([]byte) error) error {
	args := m.Called(ctx, data, subject, reply)
	return args.Error(0)
}

// Test helper functions
func setupTestLogger() *slog.Logger {
	return logging.NewLogger(true) // Enable debug mode
}

func TestNewMessagingRepository(t *testing.T) {
	logger := setupTestLogger()
	drainTimeout := 10 * time.Second

	t.Run("without_auth_repo", func(t *testing.T) {
		repo := NewMessagingRepository(nil, nil, logger, drainTimeout)

		assert.NotNil(t, repo)
		assert.NotNil(t, repo.logger)
		assert.Nil(t, repo.authRepo)
		assert.Equal(t, drainTimeout, repo.drainTimeout)
		assert.False(t, repo.isShuttingDown)
		assert.Equal(t, 0, len(repo.subscriptions))
	})
}

func TestMessagingRepository_ValidateToken_NoAuthRepo(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	ctx := context.Background()
	token := "test.jwt.token"

	principal, err := repo.ValidateToken(ctx, token)

	assert.Error(t, err)
	assert.Nil(t, principal)
	assert.Contains(t, err.Error(), constants.ErrAuthRepoNotConfigured)
}

func TestMessagingRepository_ParsePrincipals_NoAuthRepo(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	ctx := context.Background()
	headers := map[string]string{
		"Authorization": "Bearer token123",
		"X-User-ID":     "user123",
	}

	principals, err := repo.ParsePrincipals(ctx, headers)

	assert.Error(t, err)
	assert.Nil(t, principals)
	assert.Contains(t, err.Error(), constants.ErrAuthRepoNotConfigured)
}

func TestMessagingRepository_PublishDisconnected(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	ctx := context.Background()
	subject := "test.subject"
	data := []byte("test data")

	err := repo.Publish(ctx, subject, data)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NATS connection not available")
}

func TestMessagingRepository_HealthCheck_NilConnection(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	ctx := context.Background()

	err := repo.HealthCheck(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), constants.ErrHealthCheck)
	assert.Contains(t, err.Error(), "connection is nil")
}

func TestMessagingRepository_DrainWithTimeout_NilConnection(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	err := repo.DrainWithTimeout()

	assert.NoError(t, err) // Should not error with nil connection
}

func TestMessagingRepository_Close_NilConnection(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	err := repo.Close()

	assert.NoError(t, err)
}

func TestMessagingRepository_UtilityMethods_NilConnection(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	t.Run("get_connection", func(t *testing.T) {
		connection := repo.GetConnection()
		assert.Nil(t, connection)
	})

	t.Run("is_connected", func(t *testing.T) {
		isConnected := repo.IsConnected()
		assert.False(t, isConnected)
	})

	t.Run("get_subscription_count", func(t *testing.T) {
		count := repo.GetSubscriptionCount()
		assert.Equal(t, 0, count)
	})

	t.Run("get_metrics", func(t *testing.T) {
		metrics := repo.GetMetrics()

		assert.NotNil(t, metrics)
		assert.Contains(t, metrics, "connection_status")
		assert.Contains(t, metrics, "connected")
		assert.Contains(t, metrics, "total_subscriptions")
		assert.Contains(t, metrics, "active_subscriptions")
		assert.Contains(t, metrics, "subscription_health")
		assert.Contains(t, metrics, "is_shutting_down")
		assert.Contains(t, metrics, "drain_timeout")
		assert.Contains(t, metrics, "auth_repo_configured")

		// Verify specific values for nil connection
		assert.Equal(t, false, metrics["connected"])
		assert.Equal(t, 0, metrics["total_subscriptions"])
		assert.Equal(t, 0, metrics["active_subscriptions"])
		assert.Equal(t, false, metrics["auth_repo_configured"])
	})

	t.Run("get_connection_status", func(t *testing.T) {
		status := repo.GetConnectionStatus()

		assert.NotNil(t, status)
		assert.Equal(t, "messaging_repository", status["component"])
		assert.Contains(t, status, "status")
		assert.Contains(t, status, "connection_info")
		assert.Contains(t, status, "subscription_count")
		assert.Contains(t, status, "is_shutting_down")

		// Verify specific values for nil connection
		assert.Equal(t, 0, status["subscription_count"])
		assert.Equal(t, false, status["is_shutting_down"])
	})
}

func TestMessagingRepository_ClassifyNATSError(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	tests := []struct {
		name     string
		error    error
		expected string
	}{
		{"nil_error", nil, "none"},
		{"connection_closed", fmt.Errorf("connection closed"), "connection_closed"},
		{"no_responders", fmt.Errorf("no responders available"), "no_responders"},
		{"timeout", fmt.Errorf("operation timeout"), "timeout"},
		{"invalid_subject", fmt.Errorf("invalid subject name"), "invalid_subject"},
		{"permission_denied", fmt.Errorf("permission denied"), "permission_denied"},
		{"slow_consumer", fmt.Errorf("slow consumer detected"), "slow_consumer"},
		{"connection_refused", fmt.Errorf("connection refused"), "connection_refused"},
		{"reconnecting", fmt.Errorf("connection reconnecting"), "reconnecting"},
		{"unknown_error", fmt.Errorf("some unknown error"), "unknown_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := repo.classifyNATSError(tt.error)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMessagingRepository_GenerateMessageID(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	// Test that generateMessageID returns unique IDs
	id1 := repo.generateMessageID()
	time.Sleep(1 * time.Microsecond) // Ensure time difference
	id2 := repo.generateMessageID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2)
	assert.Contains(t, id1, "msg_")
	assert.Contains(t, id2, "msg_")
}

func TestMessagingRepository_GetConnectionInfo(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	info := repo.getConnectionInfo()

	assert.NotNil(t, info)
	assert.Equal(t, "nil", info["status"])
	assert.Equal(t, false, info["connected"])
	assert.Equal(t, []string{}, info["servers"])
	assert.Equal(t, 0, info["subscriptions"])
}

func TestMessagingRepository_LogConnectionState(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	// This should not panic or error
	repo.logConnectionState("test_context")

	// Test with different contexts
	repo.logConnectionState("initialization")
	repo.logConnectionState("before_subscribe")
	repo.logConnectionState("after_subscribe")
}

func TestMessagingRepository_LogSubscriptionHealth(t *testing.T) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	// This should not panic or error with empty subscriptions
	repo.logSubscriptionHealth()
}

// Performance benchmarks
func BenchmarkMessagingRepository_GenerateMessageID(b *testing.B) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = repo.generateMessageID()
	}
}

func BenchmarkMessagingRepository_ClassifyNATSError(b *testing.B) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	testError := fmt.Errorf("connection closed")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = repo.classifyNATSError(testError)
	}
}

func BenchmarkMessagingRepository_GetConnectionInfo(b *testing.B) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = repo.getConnectionInfo()
	}
}

func BenchmarkMessagingRepository_GetMetrics(b *testing.B) {
	logger := setupTestLogger()
	repo := NewMessagingRepository(nil, nil, logger, 5*time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = repo.GetMetrics()
	}
}

// Integration tests that require a real NATS connection
func TestMessagingRepository_IntegrationWithNATS(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Try to connect to a local NATS server
	conn, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skipf("Skipping integration test: no NATS server available: %v", err)
	}
	defer conn.Close()

	logger := setupTestLogger()
	repo := NewMessagingRepository(conn, nil, logger, 5*time.Second)
	defer repo.Close()

	ctx := context.Background()

	t.Run("successful_publish", func(t *testing.T) {
		subject := "test.publish.subject"
		data := []byte("test message data")

		err := repo.Publish(ctx, subject, data)
		assert.NoError(t, err)
	})

	t.Run("successful_subscribe", func(t *testing.T) {
		subject := "test.subscribe.subject"

		mockHandler := &MockMessageHandler{}
		mockHandler.On("Handle", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		err := repo.Subscribe(ctx, subject, mockHandler)
		assert.NoError(t, err)
		assert.Equal(t, 1, repo.GetSubscriptionCount())
	})

	t.Run("successful_queue_subscribe", func(t *testing.T) {
		subject := "test.queue.subject"
		queue := "test.queue"

		mockHandler := &MockMessageHandler{}
		mockHandler.On("Handle", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		err := repo.QueueSubscribe(ctx, subject, queue, mockHandler)
		assert.NoError(t, err)
		assert.Equal(t, 2, repo.GetSubscriptionCount()) // Previous test + this one
	})

	t.Run("successful_queue_subscribe_with_reply", func(t *testing.T) {
		subject := "test.reply.subject"
		queue := "test.reply.queue"

		mockHandler := &MockMessageHandlerWithReply{}
		mockHandler.On("HandleWithReply", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		err := repo.QueueSubscribeWithReply(ctx, subject, queue, mockHandler)
		assert.NoError(t, err)
		assert.Equal(t, 3, repo.GetSubscriptionCount()) // Previous tests + this one
	})

	t.Run("healthy_connection", func(t *testing.T) {
		err := repo.HealthCheck(ctx)
		assert.NoError(t, err)
	})

	t.Run("connection_utilities", func(t *testing.T) {
		assert.True(t, repo.IsConnected())
		assert.Equal(t, conn, repo.GetConnection())
		assert.Greater(t, repo.GetSubscriptionCount(), 0)

		metrics := repo.GetMetrics()
		assert.Equal(t, true, metrics["connected"])
		assert.Greater(t, metrics["total_subscriptions"], 0)

		status := repo.GetConnectionStatus()
		assert.Equal(t, "connected", status["status"])
	})

	t.Run("publish_and_receive", func(t *testing.T) {
		subject := "test.integration.pubsub"
		testData := []byte("integration test message")
		messageReceived := make(chan bool, 1)

		mockHandler := &MockMessageHandler{}
		mockHandler.On("Handle", mock.Anything, mock.MatchedBy(func(data []byte) bool {
			return string(data) == string(testData)
		}), subject).Run(func(args mock.Arguments) {
			messageReceived <- true
		}).Return(nil)

		// Subscribe first
		err := repo.Subscribe(ctx, subject, mockHandler)
		require.NoError(t, err)

		// Give subscription time to be established
		time.Sleep(100 * time.Millisecond)

		// Publish message
		err = repo.Publish(ctx, subject, testData)
		require.NoError(t, err)

		// Wait for message to be received
		select {
		case <-messageReceived:
			// Success
		case <-time.After(2 * time.Second):
			t.Fatal("Message not received within timeout")
		}

		mockHandler.AssertExpectations(t)
	})

	t.Run("drain_and_close", func(t *testing.T) {
		err := repo.DrainWithTimeout()
		assert.NoError(t, err)

		err = repo.Close()
		assert.NoError(t, err)

		assert.Equal(t, 0, repo.GetSubscriptionCount())
	})
}

// Test with auth repository
func TestMessagingRepository_WithAuthRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping auth integration test in short mode")
	}

	logger := setupTestLogger()

	// Create a real auth repository for testing
	audiences := []string{"test-audience"}
	authRepo, err := auth.NewAuthRepository("test-issuer", audiences, "https://test.auth0.com/.well-known/jwks.json", 1*time.Minute, logger)
	if err != nil {
		t.Skipf("Skipping auth test: %v", err)
	}

	repo := NewMessagingRepository(nil, authRepo, logger, 5*time.Second)
	ctx := context.Background()

	t.Run("validate_token_with_auth_repo", func(t *testing.T) {
		// This should call the auth repo but fail due to invalid token
		principal, err := repo.ValidateToken(ctx, "invalid.jwt.token")

		assert.Error(t, err)
		assert.Nil(t, principal)
		// Should not contain the "not configured" error
		assert.NotContains(t, err.Error(), constants.ErrAuthRepoNotConfigured)
	})

	t.Run("parse_principals_with_auth_repo", func(t *testing.T) {
		headers := map[string]string{
			"Authorization": "Bearer invalid.token",
		}

		// This should call the auth repo but likely fail due to invalid token
		principals, err := repo.ParsePrincipals(ctx, headers)

		// We expect either success (with empty/nil principals) or a specific auth error
		if err != nil {
			assert.Nil(t, principals)
			// Should not contain the "not configured" error
			assert.NotContains(t, err.Error(), constants.ErrAuthRepoNotConfigured)
		} else {
			// Success case - principals can be nil/empty if no valid tokens were found
			// The important thing is that we didn't get a "not configured" error
			// Check that auth repo was called (no error returned)
			assert.NoError(t, err)
			if principals != nil {
				assert.Len(t, principals, 0) // should be empty due to invalid token
			}
		}
	})

	t.Run("health_check_with_auth_repo", func(t *testing.T) {
		err := repo.HealthCheck(ctx)

		// Health check should fail due to nil connection, not auth issues
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "connection is nil")
	})

	t.Run("metrics_with_auth_repo", func(t *testing.T) {
		metrics := repo.GetMetrics()

		assert.NotNil(t, metrics)
		assert.Equal(t, true, metrics["auth_repo_configured"])
	})
}

// Test runner setup
func TestMain(m *testing.M) {
	// Setup test environment
	os.Setenv("LOG_LEVEL", "debug")

	// Run tests
	code := m.Run()

	// Cleanup
	os.Exit(code)
}
