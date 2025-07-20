// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants

import "time"

// Service identity
const (
	ServiceName    = "lfx-indexer-service"
	ServiceVersion = "2.0.0"
	Component      = "indexer"
)

// Performance limits and timeouts
const (
	MaxMessageSize    = 10 * 1024 * 1024 // 10MB max message size
	ProcessingTimeout = 30 * time.Second // Max time to process a message
	ShutdownTimeout   = 30 * time.Second // Max time for graceful shutdown
	JanitorCheckDelay = 1 * time.Second  // Delay before janitor processes item
	JanitorQueueSize  = 50               // Janitor queue capacity
)

// Default configuration values
const (
	DefaultIndex       = "resources"
	DefaultPort        = 8080
	DefaultLogLevel    = "info"
	DefaultLogFormat   = "json"
	DefaultBindAddress = "*"
)
