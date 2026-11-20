// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package messaging

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/mocks"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// TestConsumeWithJetStream_RetiresProtocolFilter exercises the real server's
// stream-first upgrade order, acknowledged durable progress and stored backlog.
func TestConsumeWithJetStream_RetiresProtocolFilter(t *testing.T) {
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	require.NoError(t, err)
	go s.Start()
	require.True(t, s.ReadyForConnections(10*time.Second))
	t.Cleanup(s.Shutdown)
	nc, err := natsgo.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const retiredSubject = "lfx.v1.index.project"
	const retiredFilter = "lfx.v1.index.>"
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: constants.StreamNameIndexEvents, Subjects: []string{constants.AllSubjects, retiredFilter}, Storage: jetstream.MemoryStorage})
	require.NoError(t, err)
	previous, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Name: constants.ConsumerNameIndexer, Durable: constants.ConsumerNameIndexer, FilterSubjects: []string{constants.AllSubjects, retiredFilter}, AckPolicy: jetstream.AckExplicitPolicy})
	require.NoError(t, err)
	acknowledged, err := js.Publish(ctx, constants.IndexPrefix+"v1_meeting", []byte("acknowledged-fixture"))
	require.NoError(t, err)
	first, err := previous.Next(jetstream.FetchMaxWait(time.Second))
	require.NoError(t, err)
	metadata, err := first.Metadata()
	require.NoError(t, err)
	require.Equal(t, acknowledged.Sequence, metadata.Sequence.Stream)
	require.NoError(t, first.DoubleAck(ctx), "establish server-confirmed durable progress before the upgrade")
	progress, err := previous.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, acknowledged.Sequence, progress.AckFloor.Stream)
	require.Positive(t, progress.AckFloor.Consumer)
	retired, err := js.Publish(ctx, retiredSubject, []byte("retired-fixture"))
	require.NoError(t, err)
	_, err = js.Publish(ctx, constants.IndexPrefix+"v1_meeting", []byte("supported-fixture"))
	require.NoError(t, err)
	// The chart applies its Stream at sync-wave -1, before the new Deployment.
	stream, err = js.UpdateStream(ctx, jetstream.StreamConfig{Name: constants.StreamNameIndexEvents, Subjects: []string{constants.AllSubjects}, Storage: jetstream.MemoryStorage})
	require.NoError(t, err, "stream subject removal must permit the existing durable consumer")
	repo := NewMessagingRepository(nc, mocks.NewMockAuthRepository(), slog.New(slog.NewJSONHandler(io.Discard, nil)), time.Second, constants.DefaultPendingMsgLimit, constants.DefaultPendingBytesLimit, 1, 2*time.Second, 1)
	t.Cleanup(func() { require.NoError(t, repo.DrainWithTimeout()) })
	type delivery struct{ subject, payload string }
	received := make(chan delivery, 2)
	require.NoError(t, repo.ConsumeWithJetStream(ctx, constants.StreamNameIndexEvents, []string{constants.AllSubjects}, func(_ context.Context, data []byte, subject string) error {
		received <- delivery{subject: subject, payload: string(data)}
		return nil
	}))
	select {
	case message := <-received:
		require.Equal(t, constants.IndexPrefix+"v1_meeting", message.subject)
		require.Equal(t, "supported-fixture", message.payload, "an already acknowledged message must not replay after migration")
	case <-ctx.Done():
		t.Fatal("supported message was not processed after narrowing the durable filter")
	}
	consumer, err := stream.Consumer(ctx, constants.ConsumerNameIndexer)
	require.NoError(t, err)
	info, err := consumer.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{constants.AllSubjects}, info.Config.FilterSubjects)
	require.Equal(t, jetstream.AckExplicitPolicy, info.Config.AckPolicy)
	require.Equal(t, 5, info.Config.MaxDeliver)
	require.True(t, info.Created.Equal(progress.Created), "migration must update, not recreate, the durable")
	require.GreaterOrEqual(t, info.AckFloor.Stream, progress.AckFloor.Stream)
	require.GreaterOrEqual(t, info.AckFloor.Consumer, progress.AckFloor.Consumer)
	retained, err := stream.GetMsg(ctx, retired.Sequence)
	require.NoError(t, err, "retiring the protocol must not purge or recreate the stream")
	require.Equal(t, retiredSubject, retained.Subject)
	select {
	case message := <-received:
		t.Fatalf("unexpected additional delivery on %s", message.subject)
	case <-time.After(50 * time.Millisecond):
	}
}
