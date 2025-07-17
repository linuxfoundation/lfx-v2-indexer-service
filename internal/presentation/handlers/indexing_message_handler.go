package handlers

import (
	"context"

	"github.com/linuxfoundation/lfx-indexer-service/internal/application/usecases"
)

// IndexingMessageHandler handles V2 indexing messages from NATS
// Following clean architecture: only handles presentation concerns (NATS protocol)
type IndexingMessageHandler struct {
	BaseMessageHandler       // Embedded for shared NATS response functionality
	messageProcessingUseCase *usecases.MessageProcessingUseCase
}

// NewIndexingMessageHandler creates a new indexing message handler
func NewIndexingMessageHandler(messageProcessingUseCase *usecases.MessageProcessingUseCase) *IndexingMessageHandler {
	return &IndexingMessageHandler{
		messageProcessingUseCase: messageProcessingUseCase,
	}
}

// Handle processes indexing messages (legacy interface for backwards compatibility)
func (h *IndexingMessageHandler) Handle(ctx context.Context, data []byte, subject string) error {
	// Delegate to use case - presentation layer only handles protocol concerns
	return h.messageProcessingUseCase.ProcessIndexingMessage(ctx, data, subject)
}

// HandleWithReply processes indexing messages with NATS reply support
func (h *IndexingMessageHandler) HandleWithReply(ctx context.Context, data []byte, subject string, reply func([]byte) error) error {
	// Process the message through application layer
	err := h.messageProcessingUseCase.ProcessIndexingMessage(ctx, data, subject)

	if err != nil {
		// Handle error response using shared base handler method with context
		h.RespondErrorWithContext(ctx, reply, err, "error processing indexing message", subject)
		return err
	}

	// Send success response using shared base handler method with context
	h.RespondSuccessWithContext(ctx, reply, subject)
	return nil
}

// V1IndexingMessageHandler handles V1 indexing messages from NATS
// Following clean architecture: only handles presentation concerns (NATS protocol)
type V1IndexingMessageHandler struct {
	BaseMessageHandler       // Embedded for shared NATS response functionality
	messageProcessingUseCase *usecases.MessageProcessingUseCase
}

// NewV1IndexingMessageHandler creates a new V1 indexing message handler
func NewV1IndexingMessageHandler(messageProcessingUseCase *usecases.MessageProcessingUseCase) *V1IndexingMessageHandler {
	return &V1IndexingMessageHandler{
		messageProcessingUseCase: messageProcessingUseCase,
	}
}

// Handle processes V1 indexing messages (legacy interface for backwards compatibility)
func (h *V1IndexingMessageHandler) Handle(ctx context.Context, data []byte, subject string) error {
	// Delegate to use case - presentation layer only handles protocol concerns
	return h.messageProcessingUseCase.ProcessV1IndexingMessage(ctx, data, subject)
}

// HandleWithReply processes V1 indexing messages with NATS reply support
func (h *V1IndexingMessageHandler) HandleWithReply(ctx context.Context, data []byte, subject string, reply func([]byte) error) error {
	// Process the message through application layer
	err := h.messageProcessingUseCase.ProcessV1IndexingMessage(ctx, data, subject)

	if err != nil {
		// Handle error response using shared base handler method with context
		h.RespondErrorWithContext(ctx, reply, err, "error processing V1 indexing message", subject)
		return err
	}

	// Send success response using shared base handler method with context
	h.RespondSuccessWithContext(ctx, reply, subject)
	return nil
}
