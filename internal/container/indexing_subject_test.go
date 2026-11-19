// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package container

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/application"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/presentation/handlers"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func TestContainer_SetupNATSSubscriptions_SupportedFilterOnly(t *testing.T) {
	for _, subject := range []string{constants.AllSubjects, constants.IndexPrefix + "v1_meeting"} {
		t.Run(subject, func(t *testing.T) {
			repository := mocks.NewMockMessagingRepository()
			processor := &application.MessageProcessor{}
			c := &Container{Config: &config.AppConfig{NATS: config.NATSConfig{IndexingSubject: subject}}, MessagingRepository: repository, MessageProcessor: processor, IndexingMessageHandler: handlers.NewIndexingMessageHandler(processor)}
			require.NoError(t, c.SetupNATSSubscriptions(context.Background()))
			require.Len(t, repository.JetStreamFilters, 1)
			require.Equal(t, []string{subject}, repository.JetStreamFilters[constants.StreamNameIndexEvents])
		})
	}
}
