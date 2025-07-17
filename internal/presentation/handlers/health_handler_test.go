package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler_HandleReadiness_HealthyDependencies(t *testing.T) {
	// Create mock repositories that are all healthy
	mockTransactionRepo := &MockTransactionRepo{}
	mockMessageRepo := &MockMessageRepo{}
	mockAuthRepo := &MockAuthRepo{}

	// Create health service
	healthService := services.NewHealthService(
		mockTransactionRepo,
		mockMessageRepo,
		mockAuthRepo,
		5*time.Second,
		1*time.Second,
	)

	// Create handler
	handler := NewHealthHandler(healthService)

	// Create request
	req := httptest.NewRequest("GET", "/readyz", nil)
	recorder := httptest.NewRecorder()

	// Handle request
	handler.HandleReadiness(recorder, req)

	// Assert response
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	// Parse response
	var response services.HealthStatus
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify the response contains actual health check data
	assert.Equal(t, "healthy", response.Status)
	assert.Contains(t, response.Checks, "opensearch")
	assert.Contains(t, response.Checks, "nats")
	assert.Contains(t, response.Checks, "auth")
	assert.Equal(t, 0, response.ErrorCount)
}

func TestHealthHandler_HandleReadiness_UnhealthyDependencies(t *testing.T) {
	// Create mock repositories with failures
	mockTransactionRepo := &MockTransactionRepo{healthError: assert.AnError}
	mockMessageRepo := &MockMessageRepo{}
	mockAuthRepo := &MockAuthRepo{}

	// Create health service
	healthService := services.NewHealthService(
		mockTransactionRepo,
		mockMessageRepo,
		mockAuthRepo,
		5*time.Second,
		1*time.Second,
	)

	// Create handler
	handler := NewHealthHandler(healthService)

	// Create request
	req := httptest.NewRequest("GET", "/readyz", nil)
	recorder := httptest.NewRecorder()

	// Handle request
	handler.HandleReadiness(recorder, req)

	// Assert response - should return 503 Service Unavailable
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	// Parse response
	var response services.HealthStatus
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify the response contains actual health check data
	assert.Equal(t, "degraded", response.Status) // Only OpenSearch failed
	assert.Contains(t, response.Checks, "opensearch")
	assert.Equal(t, "unhealthy", response.Checks["opensearch"].Status)
	assert.Equal(t, "healthy", response.Checks["nats"].Status)
	assert.Equal(t, 1, response.ErrorCount)
}

func TestHealthHandler_HandleLiveness_AlwaysHealthy(t *testing.T) {
	// Create mock repositories (can be unhealthy)
	mockTransactionRepo := &MockTransactionRepo{healthError: assert.AnError}
	mockMessageRepo := &MockMessageRepo{healthError: assert.AnError}
	mockAuthRepo := &MockAuthRepo{healthError: assert.AnError}

	// Create health service
	healthService := services.NewHealthService(
		mockTransactionRepo,
		mockMessageRepo,
		mockAuthRepo,
		5*time.Second,
		1*time.Second,
	)

	// Create handler
	handler := NewHealthHandler(healthService)

	// Create request
	req := httptest.NewRequest("GET", "/livez", nil)
	recorder := httptest.NewRecorder()

	// Handle request
	handler.HandleLiveness(recorder, req)

	// Assert response - liveness should always return 200
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

	// Parse response
	var response services.HealthStatus
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify liveness is always healthy (service-level check)
	assert.Equal(t, "healthy", response.Status)
	assert.Contains(t, response.Checks, "service")
	assert.Equal(t, "healthy", response.Checks["service"].Status)
}

// Mock implementations for testing

type MockTransactionRepo struct {
	healthError error
}

func (m *MockTransactionRepo) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	return nil
}

func (m *MockTransactionRepo) Search(ctx context.Context, index string, query map[string]any) ([]map[string]any, error) {
	return nil, nil
}

func (m *MockTransactionRepo) Update(ctx context.Context, index string, docID string, body io.Reader) error {
	return nil
}

func (m *MockTransactionRepo) Delete(ctx context.Context, index string, docID string) error {
	return nil
}

func (m *MockTransactionRepo) BulkIndex(ctx context.Context, operations []repositories.BulkOperation) error {
	return nil
}

func (m *MockTransactionRepo) HealthCheck(ctx context.Context) error {
	return m.healthError
}

func (m *MockTransactionRepo) UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *repositories.OptimisticUpdateParams) error {
	return nil
}

func (m *MockTransactionRepo) SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]repositories.VersionedDocument, error) {
	return nil, nil
}

type MockMessageRepo struct {
	healthError error
}

func (m *MockMessageRepo) Subscribe(ctx context.Context, subject string, handler repositories.MessageHandler) error {
	return nil
}

func (m *MockMessageRepo) QueueSubscribe(ctx context.Context, subject string, queue string, handler repositories.MessageHandler) error {
	return nil
}

func (m *MockMessageRepo) QueueSubscribeWithReply(ctx context.Context, subject string, queue string, handler repositories.MessageHandlerWithReply) error {
	return nil
}

func (m *MockMessageRepo) Publish(ctx context.Context, subject string, data []byte) error {
	return nil
}

func (m *MockMessageRepo) Close() error {
	return nil
}

func (m *MockMessageRepo) HealthCheck(ctx context.Context) error {
	return m.healthError
}

type MockAuthRepo struct {
	healthError error
}

func (m *MockAuthRepo) ValidateToken(ctx context.Context, token string) (*entities.Principal, error) {
	return nil, nil
}

func (m *MockAuthRepo) ParsePrincipals(ctx context.Context, headers map[string]string) ([]entities.Principal, error) {
	return nil, nil
}

func (m *MockAuthRepo) HealthCheck(ctx context.Context) error {
	return m.healthError
}
