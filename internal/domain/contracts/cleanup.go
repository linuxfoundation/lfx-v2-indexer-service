// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import "context"

// CleanupRepository defines the contract for cleanup/janitor operations
// Handles background cleanup of resources and maintains data consistency
type CleanupRepository interface {
	// CheckItem queues a resource for the janitor to check
	CheckItem(objectRef string)

	// StartItemLoop starts the background cleanup processing loop
	StartItemLoop(ctx context.Context)

	// Shutdown gracefully stops the cleanup repository
	Shutdown()

	// GetMetrics returns cleanup repository metrics for monitoring
	GetMetrics() map[string]interface{}

	// IsRunning returns whether the cleanup repository is currently running
	IsRunning() bool
}
