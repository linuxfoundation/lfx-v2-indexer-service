package handlers

import (
	"encoding/json"
	"net/http"
)

// HealthHandler handles Kubernetes health check requests
type HealthHandler struct {
}

// NewHealthHandler creates a new health handler
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// HandleLiveness handles Kubernetes liveness probe requests
func (h *HealthHandler) HandleLiveness(w http.ResponseWriter, r *http.Request) {
	// Simple liveness check - always return OK if the service is running
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","probe":"liveness"}`))
}

// HandleReadiness handles Kubernetes readiness probe requests
func (h *HealthHandler) HandleReadiness(w http.ResponseWriter, r *http.Request) {
	// Simple readiness check - always return OK if the service is running
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]interface{}{
		"status": "ready",
		"probe":  "readiness",
	}
	json.NewEncoder(w).Encode(response)
}

// HandleHealthCheck handles general health check requests
func (h *HealthHandler) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	// Simple health check - always return OK if the service is running
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]interface{}{
		"status": "healthy",
		"probe":  "health",
	}
	json.NewEncoder(w).Encode(response)
}

// RegisterRoutes registers the health check routes
func (h *HealthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/livez", h.HandleLiveness)
	mux.HandleFunc("/readyz", h.HandleReadiness)
	mux.HandleFunc("/health", h.HandleHealthCheck)
}
