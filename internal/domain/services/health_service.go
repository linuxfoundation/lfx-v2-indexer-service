package services

import (
	"context"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/repositories"
)

// HealthService coordinates health checks across all dependencies with inline caching
type HealthService struct {
	transactionRepo repositories.TransactionRepository
	messageRepo     repositories.MessageRepository
	authRepo        repositories.AuthRepository
	timeout         time.Duration

	// Inline cache fields - much simpler than separate HealthCache!
	mu            sync.RWMutex
	cacheDuration time.Duration
	lastReadiness *cachedResult
	lastLiveness  *cachedResult
	lastHealth    *cachedResult
}

// HealthStatus represents the overall health status of the service
type HealthStatus struct {
	Status     string           `json:"status"` // "healthy", "degraded", "unhealthy"
	Timestamp  time.Time        `json:"timestamp"`
	Duration   time.Duration    `json:"duration"`
	Checks     map[string]Check `json:"checks"`
	ErrorCount int              `json:"error_count,omitempty"`
}

// Check represents the health status of an individual component
type Check struct {
	Status    string        `json:"status"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
}

// cachedResult holds a cached health status with timestamp
type cachedResult struct {
	status    *HealthStatus
	timestamp time.Time
}

// NewHealthService creates a new health service with the specified dependencies and cache settings
func NewHealthService(
	transactionRepo repositories.TransactionRepository,
	messageRepo repositories.MessageRepository,
	authRepo repositories.AuthRepository,
	timeout time.Duration,
	cacheDuration time.Duration,
) *HealthService {
	return &HealthService{
		transactionRepo: transactionRepo,
		messageRepo:     messageRepo,
		authRepo:        authRepo,
		timeout:         timeout,
		cacheDuration:   cacheDuration,
	}
}

// CheckReadiness performs readiness checks with inline caching
func (s *HealthService) CheckReadiness(ctx context.Context) *HealthStatus {
	// Simple inline cache check
	s.mu.RLock()
	if s.lastReadiness != nil && time.Since(s.lastReadiness.timestamp) < s.cacheDuration {
		cached := s.lastReadiness.status
		s.mu.RUnlock()
		return cached
	}
	s.mu.RUnlock()

	// Perform actual health check
	status := s.performReadinessCheck(ctx)

	// Simple inline cache update
	s.mu.Lock()
	s.lastReadiness = &cachedResult{status: status, timestamp: time.Now()}
	s.mu.Unlock()

	return status
}

// CheckLiveness performs liveness checks with inline caching
func (s *HealthService) CheckLiveness(ctx context.Context) *HealthStatus {
	// Simple inline cache check
	s.mu.RLock()
	if s.lastLiveness != nil && time.Since(s.lastLiveness.timestamp) < s.cacheDuration {
		cached := s.lastLiveness.status
		s.mu.RUnlock()
		return cached
	}
	s.mu.RUnlock()

	// Liveness is simpler - just check if the service itself is responsive
	status := &HealthStatus{
		Status:    "healthy",
		Timestamp: time.Now(),
		Duration:  0,
		Checks: map[string]Check{
			"service": {
				Status:    "healthy",
				Timestamp: time.Now(),
				Duration:  0,
			},
		},
	}

	// Simple inline cache update
	s.mu.Lock()
	s.lastLiveness = &cachedResult{status: status, timestamp: time.Now()}
	s.mu.Unlock()

	return status
}

// CheckHealth performs comprehensive health checks with inline caching
func (s *HealthService) CheckHealth(ctx context.Context) *HealthStatus {
	// Simple inline cache check
	s.mu.RLock()
	if s.lastHealth != nil && time.Since(s.lastHealth.timestamp) < s.cacheDuration {
		cached := s.lastHealth.status
		s.mu.RUnlock()
		return cached
	}
	s.mu.RUnlock()

	// Health check can be more lenient than readiness
	status := s.performReadinessCheck(ctx)

	// For general health, we might accept degraded state as "healthy"
	if status.Status == "degraded" {
		status.Status = "healthy" // Accept degraded for general health
	}

	// Simple inline cache update
	s.mu.Lock()
	s.lastHealth = &cachedResult{status: status, timestamp: time.Now()}
	s.mu.Unlock()

	return status
}

// performReadinessCheck does the actual readiness health checking logic
func (s *HealthService) performReadinessCheck(ctx context.Context) *HealthStatus {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	status := &HealthStatus{
		Timestamp: time.Now(),
		Checks:    make(map[string]Check),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// Check all dependencies in parallel
	dependencies := []struct {
		name string
		repo interface{ HealthCheck(context.Context) error }
	}{
		{"opensearch", s.transactionRepo},
		{"nats", s.messageRepo},
		{"auth", s.authRepo},
	}

	for _, dep := range dependencies {
		wg.Add(1)
		go func(name string, repo interface{ HealthCheck(context.Context) error }) {
			defer wg.Done()

			start := time.Now()
			err := repo.HealthCheck(ctx)
			duration := time.Since(start)

			check := Check{
				Duration:  duration,
				Timestamp: start,
			}

			if err != nil {
				check.Status = "unhealthy"
				check.Error = err.Error()
				mu.Lock()
				status.ErrorCount++
				mu.Unlock()
			} else {
				check.Status = "healthy"
			}

			mu.Lock()
			status.Checks[name] = check
			mu.Unlock()
		}(dep.name, dep.repo)
	}

	wg.Wait()
	status.Duration = time.Since(status.Timestamp)

	// Determine overall status
	if status.ErrorCount == 0 {
		status.Status = "healthy"
	} else if status.ErrorCount == len(dependencies) {
		status.Status = "unhealthy"
	} else {
		status.Status = "degraded"
	}

	return status
}

// ClearCache clears all cached health statuses (useful for testing)
func (s *HealthService) ClearCache() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastReadiness = nil
	s.lastLiveness = nil
	s.lastHealth = nil
}
