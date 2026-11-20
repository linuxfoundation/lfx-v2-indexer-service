// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

func TestIndexingMessageHandler_Handle(t *testing.T) {
	for _, tt := range []struct {
		name, subject string
		data          []byte
		failure       error
	}{
		{"current resource", constants.ProjectSubject, []byte(`{"action":"created"}`), nil},
		{"legacy-named resource", constants.IndexPrefix + "v1_meeting", []byte(`{"action":"updated"}`), nil},
		{"processing failure", constants.ProjectSubject, []byte(`{}`), fmt.Errorf("invalid message")},
		{"unsupported subject", "unsupported.subject", nil, fmt.Errorf("invalid subject")},
		{"nil data", constants.ProjectSubject, nil, fmt.Errorf("invalid JSON")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			processor := &mocks.MockIndexingMessageProcessor{}
			ctx, _ := logging.WithRequestID(context.Background(), slog.Default())
			processor.On("ProcessIndexingMessage", ctx, tt.data, tt.subject).Return(tt.failure).Once()
			handler := NewIndexingMessageHandler(processor)
			require.NotNil(t, handler)
			err := handler.Handle(ctx, tt.data, tt.subject)
			if tt.failure != nil {
				require.ErrorIs(t, err, tt.failure)
			} else {
				require.NoError(t, err)
			}
			processor.AssertExpectations(t)
		})
	}
}

func TestIndexingMessageHandler_HandleWithReply(t *testing.T) {
	for _, tt := range []struct {
		name                               string
		failure                            error
		noReply, replyFailure, withContext bool
	}{
		{name: "success"}, {name: "success with context", withContext: true},
		{name: "processing failure", failure: fmt.Errorf("invalid message")},
		{name: "failure with context", failure: fmt.Errorf("invalid message"), withContext: true},
		{name: "success without reply", noReply: true},
		{name: "failure without reply", failure: fmt.Errorf("invalid message"), noReply: true},
		{name: "success reply failed", replyFailure: true},
		{name: "error reply failed", failure: fmt.Errorf("invalid message"), replyFailure: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			processor := &mocks.MockIndexingMessageProcessor{}
			ctx := context.Background()
			if tt.withContext {
				ctx, _ = logging.WithRequestID(ctx, slog.Default())
			}
			data := []byte(`{"action":"updated"}`)
			processor.On("ProcessIndexingMessage", mock.Anything, data, constants.ProjectSubject).Return(tt.failure).Once()
			var reply func([]byte) error
			var responses []string
			if !tt.noReply {
				reply = func(body []byte) error {
					responses = append(responses, string(body))
					if tt.replyFailure {
						return fmt.Errorf("reply unavailable")
					}
					return nil
				}
			}
			err := NewIndexingMessageHandler(processor).HandleWithReply(ctx, data, constants.ProjectSubject, reply)
			if tt.failure != nil {
				require.ErrorIs(t, err, tt.failure)
			} else {
				require.NoError(t, err)
			}
			if tt.noReply {
				require.Empty(t, responses)
			} else if tt.failure != nil {
				require.Equal(t, []string{"ERROR: error processing indexing message"}, responses)
			} else {
				require.Equal(t, []string{"OK"}, responses)
			}
			processor.AssertExpectations(t)
		})
	}
}
