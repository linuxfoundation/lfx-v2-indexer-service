// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockIndexingMessageProcessor implements the presentation handler's narrow port.
type MockIndexingMessageProcessor struct{ mock.Mock }

// ProcessIndexingMessage returns the configured message processing result.
func (m *MockIndexingMessageProcessor) ProcessIndexingMessage(ctx context.Context, data []byte, subject string) error {
	return m.Called(ctx, data, subject).Error(0)
}
