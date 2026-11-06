// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package contracts defines interfaces and data structures for domain contracts.
package contracts

import (
	"context"
)

// TokenValidator validates the fresh service credential carried by a message.
type TokenValidator interface {
	ValidateToken(ctx context.Context, token string) (*Principal, error)
}

// AuthRepository defines the contract for authentication operations
// Handles JWT validation and principal parsing for authorization
type AuthRepository interface {
	TokenValidator

	// ParsePrincipals parses principals from HTTP headers with delegation support
	ParsePrincipals(ctx context.Context, headers map[string]string) ([]Principal, error)

	// GetMetrics returns authentication repository metrics for monitoring
	GetMetrics() map[string]interface{}

	// GetAuthStatus retrieves authentication system status metrics
	GetAuthStatus() map[string]interface{}

	// HealthCheck verifies authentication service connectivity and health
	HealthCheck(ctx context.Context) error
}
