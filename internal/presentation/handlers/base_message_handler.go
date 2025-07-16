package handlers

import "log"

// BaseMessageHandler contains shared functionality for NATS message handlers
// Following DRY principle to eliminate code duplication
type BaseMessageHandler struct{}

// RespondError handles error responses following deployment handler pattern
// Shared implementation used by all message handlers
func (h *BaseMessageHandler) RespondError(reply func([]byte) error, err error, msg string) {
	// Log the error (presentation layer concern)
	log.Printf("Error: %s - %v", msg, err)

	// Send error response to NATS (presentation layer concern)
	if reply != nil {
		if replyErr := reply([]byte(msg)); replyErr != nil {
			log.Printf("Failed to send error reply: %v", replyErr)
		}
	}
}

// RespondSuccess handles success responses following deployment handler pattern
// Shared implementation used by all message handlers
func (h *BaseMessageHandler) RespondSuccess(reply func([]byte) error, subject string) {
	if reply != nil {
		if replyErr := reply([]byte("OK")); replyErr != nil {
			log.Printf("Failed to send success reply for subject %s: %v", subject, replyErr)
		}
	}
}
