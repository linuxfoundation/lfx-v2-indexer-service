// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
	"github.com/stretchr/testify/assert"
)

func TestHealthHandler_HandleReadiness_Healthy(t *testing.T) {
	// Setup
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := services.NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)
	handler := NewHealthHandler(indexerService, false)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/health/readiness", nil)
	w := httptest.NewRecorder()

	// Execute
	handler.HandleReadiness(w, req)

	// Verify
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"healthy"`)
}

func TestHealthHandler_HandleReadiness_Unhealthy(t *testing.T) {
	// Setup - simulate storage failure
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	mockStorageRepo.HealthError = assert.AnError

	logger, _ := logging.TestLogger(t)
	indexerService := services.NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)
	handler := NewHealthHandler(indexerService, false)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/health/readiness", nil)
	w := httptest.NewRecorder()

	// Execute
	handler.HandleReadiness(w, req)

	// Verify
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"unhealthy"`)
}

func TestHealthHandler_HandleLiveness_AlwaysHealthy(t *testing.T) {
	// Setup - even with all dependencies failing
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	mockStorageRepo.HealthError = assert.AnError
	mockMessagingRepo.HealthError = assert.AnError

	logger, _ := logging.TestLogger(t)
	indexerService := services.NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)
	handler := NewHealthHandler(indexerService, false)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/health/liveness", nil)
	w := httptest.NewRecorder()

	// Execute
	handler.HandleLiveness(w, req)

	// Verify - liveness should always return 200
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"healthy"`)
}

func TestHealthHandler_HandleHealthCheck_Simple(t *testing.T) {
	// Setup with simple response enabled
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := services.NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)
	handler := NewHealthHandler(indexerService, true) // Simple response

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	// Execute
	handler.HandleHealthCheck(w, req)

	// Verify
	assert.Equal(t, http.StatusOK, w.Code)

	// Simple response should just be "OK" text
	responseBody := w.Body.String()
	assert.Equal(t, "OK\n", responseBody)
}

func TestHealthHandler_HandleHealthCheck_Detailed(t *testing.T) {
	// Setup with detailed response
	mockStorageRepo := mocks.NewMockStorageRepository()
	mockMessagingRepo := mocks.NewMockMessagingRepository()
	logger, _ := logging.TestLogger(t)

	indexerService := services.NewIndexerService(
		mockStorageRepo,
		mockMessagingRepo,
		logger,
	)
	handler := NewHealthHandler(indexerService, false) // Detailed response

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	// Execute
	handler.HandleHealthCheck(w, req)

	// Verify
	assert.Equal(t, http.StatusOK, w.Code)

	// Detailed response should contain checks
	responseBody := w.Body.String()
	assert.Contains(t, responseBody, `"status":"healthy"`)
	assert.Contains(t, responseBody, `"checks"`)
	assert.Contains(t, responseBody, `"opensearch"`)
	assert.Contains(t, responseBody, `"nats"`)
}
