// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package handlers provides HTTP request handlers for the indexer service API.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/services"
)

// HealthHandler handles Kubernetes health check requests
type HealthHandler struct {
	indexerService *services.IndexerService
	simpleResponse bool
}

// NewHealthHandler creates a new health handler with the indexer service
func NewHealthHandler(indexerService *services.IndexerService, simpleResponse bool) *HealthHandler {
	return &HealthHandler{
		indexerService: indexerService,
		simpleResponse: simpleResponse,
	}
}

// HandleLiveness handles Kubernetes liveness probe requests
func (h *HealthHandler) HandleLiveness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := h.indexerService.CheckLiveness(ctx)

	h.writeHealthResponse(w, status, http.StatusOK) // Liveness always returns 200
}

// HandleReadiness handles Kubernetes readiness probe requests
func (h *HealthHandler) HandleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := h.indexerService.CheckReadiness(ctx)

	var statusCode int
	switch status.Status {
	case "healthy":
		statusCode = http.StatusOK
	case "degraded", "unhealthy":
		statusCode = http.StatusServiceUnavailable
	default:
		statusCode = http.StatusInternalServerError
	}

	h.writeHealthResponse(w, status, statusCode)
}

// HandleHealthCheck handles general health check requests
func (h *HealthHandler) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := h.indexerService.CheckHealth(ctx)

	var statusCode int
	switch status.Status {
	case "healthy":
		statusCode = http.StatusOK
	case "degraded":
		statusCode = http.StatusOK // Accept degraded for general health
	case "unhealthy":
		statusCode = http.StatusServiceUnavailable
	default:
		statusCode = http.StatusInternalServerError
	}

	h.writeHealthResponse(w, status, statusCode)
}

// writeHealthResponse writes the health status as JSON or simple response
func (h *HealthHandler) writeHealthResponse(w http.ResponseWriter, status *services.HealthStatus, statusCode int) {
	if h.simpleResponse {
		// Simple response for K8s compatibility
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(statusCode)
		if statusCode == http.StatusOK {
			fmt.Fprintf(w, "OK\n")
		} else {
			fmt.Fprintf(w, "UNHEALTHY\n")
		}
		return
	}

	// Default JSON response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(status); err != nil {
		// Fallback to simple response if JSON encoding fails
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// RegisterRoutes registers the health check routes
func (h *HealthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/livez", h.HandleLiveness)
	mux.HandleFunc("/readyz", h.HandleReadiness)
	mux.HandleFunc("/health", h.HandleHealthCheck)
}
