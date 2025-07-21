// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"context"
)

// AuthRepository defines the contract for authentication operations
// Handles JWT validation and principal parsing for authorization
type AuthRepository interface {
		// ValidateToken validates a JWT token and returns principal information
	ValidateToken(ctx context.Context, token string) (*Principal, error)

	// ParsePrincipals parses principals from HTTP headers with delegation support
	ParsePrincipals(ctx context.Context, headers map[string]string) ([]Principal, error)

	// GetMetrics returns authentication repository metrics for monitoring
	GetMetrics() map[string]interface{}

	// GetAuthStatus retrieves authentication system status metrics
	GetAuthStatus() map[string]interface{}

	// HealthCheck verifies authentication service connectivity and health
	HealthCheck(ctx context.Context) error
}
