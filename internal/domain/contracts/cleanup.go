// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package contracts defines the interfaces and contracts for the domain layer of the LFX indexer service.
package contracts

import "context"

// AuditedCleanup queues reconciliation that waits for the guarded canonical
// document to become searchable before considering legacy duplicates clean.
type AuditedCleanup interface {
	CheckAuditedItem(objectRef string)
}

// CleanupRepository defines the contract for cleanup/janitor operations
// Handles background cleanup of resources and maintains data consistency
type CleanupRepository interface {
	AuditedCleanup
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
