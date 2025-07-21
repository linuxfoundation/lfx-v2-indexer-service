// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package contracts defines the interfaces and contracts for the domain layer of the LFX indexer service.
package contracts

import (
	"context"
)

// MessageHandler defines the interface for handling messages
type MessageHandler interface {
	Handle(ctx context.Context, data []byte, subject string) error
}

// MessageHandlerWithReply defines the interface for handling messages with reply support
type MessageHandlerWithReply interface {
	HandleWithReply(ctx context.Context, data []byte, subject string, reply func([]byte) error) error
}

// MessagingRepository defines the interface for NATS message operations and authentication
type MessagingRepository interface {
	// NATS message operations

	// Subscribe subscribes to NATS messages
	Subscribe(ctx context.Context, subject string, handler MessageHandler) error

	// QueueSubscribe subscribes to NATS messages with queue group for load balancing
	QueueSubscribe(ctx context.Context, subject string, queue string, handler MessageHandler) error

	// QueueSubscribeWithReply subscribes to NATS messages with queue group and reply support
	QueueSubscribeWithReply(ctx context.Context, subject string, queue string, handler MessageHandlerWithReply) error

	// Publish publishes a message to NATS
	Publish(ctx context.Context, subject string, data []byte) error

	// Close closes the NATS connection
	Close() error

	// HealthCheck checks the health of the NATS connection
	HealthCheck(ctx context.Context) error

	// DrainWithTimeout performs graceful NATS connection drain with timeout
	DrainWithTimeout() error

	// Authentication operations (from AuthRepository)

	// ValidateToken validates a JWT token and returns principal information
	ValidateToken(ctx context.Context, token string) (*Principal, error)

	// ParsePrincipals parses principals from HTTP headers with delegation support
	ParsePrincipals(ctx context.Context, headers map[string]string) ([]Principal, error)
}
