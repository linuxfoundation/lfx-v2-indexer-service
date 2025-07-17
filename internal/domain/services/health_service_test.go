package services

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/logging"
	"github.com/stretchr/testify/assert"
)

func TestHealthService_CacheWorksCorrectly(t *testing.T) {
	callCount := 0
	mockRepo := &TestTransactionRepo{
		healthCheckFunc: func(ctx context.Context) error {
			callCount++
			return nil
		},
	}

	logger, _ := logging.TestLogger(t)
	healthService := NewHealthService(
		mockRepo,
		&TestMessageRepo{},
		&TestAuthRepo{},
		logger,
		5*time.Second,
		100*time.Millisecond, // Very short cache duration for testing
	)

	// First call should execute health check
	status1 := healthService.CheckReadiness(context.Background())
	assert.Equal(t, "healthy", status1.Status)
	assert.Equal(t, 1, callCount, "Health check should be called once")

	// Second call within cache duration should use cache
	status2 := healthService.CheckReadiness(context.Background())
	assert.Equal(t, "healthy", status2.Status)
	assert.Equal(t, 1, callCount, "Health check should still be called only once (cached)")

	// Wait for cache to expire
	time.Sleep(150 * time.Millisecond)

	// Third call after cache expiry should execute health check again
	status3 := healthService.CheckReadiness(context.Background())
	assert.Equal(t, "healthy", status3.Status)
	assert.Equal(t, 2, callCount, "Health check should be called twice after cache expiry")
}

func TestHealthService_ClearCache(t *testing.T) {
	mockRepo := &TestTransactionRepo{
		healthCheckFunc: func(ctx context.Context) error {
			return nil
		},
	}

	logger, _ := logging.TestLogger(t)
	healthService := NewHealthService(
		mockRepo,
		&TestMessageRepo{},
		&TestAuthRepo{},
		logger,
		5*time.Second,
		100*time.Millisecond, // Very short cache duration for testing
	)

	// Populate cache
	_ = healthService.CheckReadiness(context.Background())
	_ = healthService.CheckLiveness(context.Background())
	_ = healthService.CheckHealth(context.Background())

	// Verify cache is populated
	assert.NotNil(t, healthService.lastReadiness)
	assert.NotNil(t, healthService.lastLiveness)
	assert.NotNil(t, healthService.lastHealth)

	// Clear cache
	healthService.ClearCache()

	// Verify cache is cleared
	assert.Nil(t, healthService.lastReadiness)
	assert.Nil(t, healthService.lastLiveness)
	assert.Nil(t, healthService.lastHealth)
}

// Test repositories for health service testing

type TestTransactionRepo struct {
	healthCheckFunc func(context.Context) error
}

func (t *TestTransactionRepo) Index(ctx context.Context, index string, docID string, body io.Reader) error {
	return nil
}

func (t *TestTransactionRepo) Search(ctx context.Context, index string, query map[string]any) ([]map[string]any, error) {
	return nil, nil
}

func (t *TestTransactionRepo) Update(ctx context.Context, index string, docID string, body io.Reader) error {
	return nil
}

func (t *TestTransactionRepo) Delete(ctx context.Context, index string, docID string) error {
	return nil
}

func (t *TestTransactionRepo) BulkIndex(ctx context.Context, operations []repositories.BulkOperation) error {
	return nil
}

func (t *TestTransactionRepo) HealthCheck(ctx context.Context) error {
	if t.healthCheckFunc != nil {
		return t.healthCheckFunc(ctx)
	}
	return nil
}

func (t *TestTransactionRepo) UpdateWithOptimisticLock(ctx context.Context, index, docID string, body io.Reader, params *repositories.OptimisticUpdateParams) error {
	return nil
}

func (t *TestTransactionRepo) SearchWithVersions(ctx context.Context, index string, query map[string]any) ([]repositories.VersionedDocument, error) {
	return nil, nil
}

type TestMessageRepo struct{}

func (t *TestMessageRepo) Subscribe(ctx context.Context, subject string, handler repositories.MessageHandler) error {
	return nil
}

func (t *TestMessageRepo) QueueSubscribe(ctx context.Context, subject string, queue string, handler repositories.MessageHandler) error {
	return nil
}

func (t *TestMessageRepo) QueueSubscribeWithReply(ctx context.Context, subject string, queue string, handler repositories.MessageHandlerWithReply) error {
	return nil
}

func (t *TestMessageRepo) Publish(ctx context.Context, subject string, data []byte) error {
	return nil
}

func (t *TestMessageRepo) Close() error {
	return nil
}

func (t *TestMessageRepo) HealthCheck(ctx context.Context) error {
	return nil
}

type TestAuthRepo struct{}

func (t *TestAuthRepo) ValidateToken(ctx context.Context, token string) (*entities.Principal, error) {
	return nil, nil
}

func (t *TestAuthRepo) ParsePrincipals(ctx context.Context, headers map[string]string) ([]entities.Principal, error) {
	return nil, nil
}

func (t *TestAuthRepo) HealthCheck(ctx context.Context) error {
	return nil
}
