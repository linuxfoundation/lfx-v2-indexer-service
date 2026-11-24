// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package container

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/application"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/config"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/presentation/handlers"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func TestContainer_SetupNATSSubscriptions_SupportedFilterOnly(t *testing.T) {
	for _, subject := range []string{constants.AllSubjects, constants.IndexPrefix + "v1_meeting"} {
		t.Run(subject, func(t *testing.T) {
			repository := mocks.NewMockMessagingRepository()
			processor := application.NewMessageProcessor(nil, repository, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			c := &Container{
				Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
				Config:                 &config.AppConfig{NATS: config.NATSConfig{IndexingSubject: subject, IndexerIndexSubject: constants.AllIndexerIndexSubjects, Queue: constants.DefaultQueue, WorkerCount: 1, RequestTimeout: time.Second}},
				MessagingRepository:    repository,
				MessageProcessor:       processor,
				IndexingMessageHandler: handlers.NewIndexingMessageHandler(processor),
			}
			c.initializeDelivery()
			require.NoError(t, c.SetupNATSSubscriptions(context.Background()))
			require.Len(t, repository.JetStreamFilters, 1)
			require.Equal(t, []string{subject}, repository.JetStreamFilters[constants.StreamNameIndexEvents])
			require.Contains(t, repository.QueueDispatchers[constants.AllIndexerIndexSubjects], constants.DefaultQueue,
				"the request/reply subject must be a core-NATS queue subscription, not a JetStream filter")
		})
	}
}

func TestContainer_InitializeDelivery_FGAWriterNotConfigured(t *testing.T) {
	repository := mocks.NewMockMessagingRepository()
	processor := application.NewMessageProcessor(nil, repository, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c := &Container{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config:              &config.AppConfig{NATS: config.NATSConfig{IndexerIndexSubject: constants.AllIndexerIndexSubjects, Queue: constants.DefaultQueue, WorkerCount: 1, RequestTimeout: time.Second}},
		MessagingRepository: repository,
		MessageProcessor:    processor,
	}
	c.initializeDelivery()
	_, err := c.FGAWriter.Apply(context.Background(), "fixture_resource", []byte(`{}`))
	require.ErrorIs(t, err, contracts.ErrFGANotConfigured)

	require.NoError(t, processor.StartDeliverySubscription(context.Background()))
	replies := make(chan string, 1)
	payload := []byte(`{"fga":{"object_type":"fixture_resource","operation":"delete_access","data":{"uid":"r1"}}}`)
	require.NoError(t, repository.SimulateDispatch(context.Background(), constants.AllIndexerIndexSubjects, constants.DefaultQueue,
		constants.IndexerIndexPrefix+"fixture_resource", payload, func(body []byte) error {
			replies <- string(body)
			return nil
		}))
	select {
	case reply := <-replies:
		require.Equal(t, "ERROR: transient: fga: "+contracts.ErrFGANotConfigured.Error(), reply)
	case <-time.After(5 * time.Second):
		t.Fatal("no reply")
	}
	c.DeliveryDispatcher.Wait()
}

func TestContainer_InitializeDelivery_FGAWriterFromEnv(t *testing.T) {
	t.Setenv("OPENFGA_API_URL", "http://127.0.0.1:1")
	t.Setenv("OPENFGA_STORE_ID", "store-under-test")
	repository := mocks.NewMockMessagingRepository()
	processor := application.NewMessageProcessor(nil, repository, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c := &Container{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config:              &config.AppConfig{NATS: config.NATSConfig{IndexerIndexSubject: constants.AllIndexerIndexSubjects, Queue: constants.DefaultQueue, WorkerCount: 1, RequestTimeout: time.Second}},
		MessagingRepository: repository,
		MessageProcessor:    processor,
		NATSConnection:      auditTestNATS(t),
	}
	c.initializeDelivery()
	require.NotNil(t, c.fgaWriter)
	require.Same(t, c.fgaWriter, c.FGAWriter)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.FGAWriter.Apply(ctx, "fixture_resource", []byte(`{"object_type":"fixture_resource","operation":"delete_access","data":{"uid":"r1"}}`))
	require.Error(t, err)
	require.NotErrorIs(t, err, contracts.ErrFGANotConfigured)
	c.fgaWriter.Close()
}
