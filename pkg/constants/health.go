package constants

import "time"

// Health check statuses
const (
	StatusHealthy   = "healthy"
	StatusDegraded  = "degraded"
	StatusUnhealthy = "unhealthy"
)

// Health components (for detailed health reporting)
const (
	ComponentOpenSearch = "opensearch"
	ComponentNATS       = "nats"
	ComponentAuth       = "auth"
	ComponentService    = "service"
	ComponentContainer  = "container"
)

// Health endpoints
const (
	HealthPath    = "/health"
	ReadinessPath = "/ready"
	LivenessPath  = "/live"
)

// Health timeouts and caching
const (
	HealthCheckTimeout = 5 * time.Second
	CacheDuration      = 5 * time.Second
)
