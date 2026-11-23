// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package messaging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func startCoreNATS(t *testing.T) *natsgo.Conn {
	t.Helper()
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	require.NoError(t, err)
	go s.Start()
	require.True(t, s.ReadyForConnections(10*time.Second))
	t.Cleanup(s.Shutdown)
	nc, err := natsgo.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return nc
}

func newDispatchRepo(t *testing.T, nc *natsgo.Conn) *MessagingRepository {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewMessagingRepository(nc, nil, logger, 5*time.Second, constants.DefaultPendingMsgLimit, constants.DefaultPendingBytesLimit, constants.DefaultWorkerCount, constants.DefaultAckWait, 0)
}

func TestQueueSubscribeDispatch_CallsDispatchInDeliveryOrder(t *testing.T) {
	nc := startCoreNATS(t)
	repo := newDispatchRepo(t, nc)

	var mu sync.Mutex
	var seen []string
	err := repo.QueueSubscribeDispatch(context.Background(), "lfx.indexer.index.>", "q", func(_ context.Context, data []byte, subject string, reply func([]byte) error) {
		mu.Lock()
		seen = append(seen, subject+"="+string(data))
		mu.Unlock()
		_ = reply([]byte("OK"))
	})
	require.NoError(t, err)

	publisher := startPublisher(t, nc)
	for i := range 50 {
		require.NoError(t, publisher.Publish("lfx.indexer.index.fixture", fmt.Appendf(nil, "%d", i)))
	}
	require.NoError(t, publisher.Flush())

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 50
	}, 5*time.Second, 10*time.Millisecond)
	for i, got := range seen {
		require.Equal(t, fmt.Sprintf("lfx.indexer.index.fixture=%d", i), got)
	}
}

func TestQueueSubscribeDispatch_ReplyReachesRequesterOnce(t *testing.T) {
	nc := startCoreNATS(t)
	repo := newDispatchRepo(t, nc)

	errs := make(chan error, 2)
	err := repo.QueueSubscribeDispatch(context.Background(), "lfx.indexer.index.>", "q", func(_ context.Context, data []byte, _ string, reply func([]byte) error) {
		go func() {
			errs <- reply(append([]byte("echo:"), data...))
			errs <- reply([]byte("second"))
		}()
	})
	require.NoError(t, err)

	msg, err := startPublisher(t, nc).Request("lfx.indexer.index.fixture", []byte("hi"), 2*time.Second)
	require.NoError(t, err)
	require.Equal(t, "echo:hi", string(msg.Data))
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
}

func TestQueueSubscribeDispatch_DrainWaitsForReply(t *testing.T) {
	nc := startCoreNATS(t)
	repo := newDispatchRepo(t, nc)

	received := make(chan func([]byte) error, 1)
	err := repo.QueueSubscribeDispatch(context.Background(), "lfx.indexer.index.>", "q", func(_ context.Context, _ []byte, _ string, reply func([]byte) error) {
		received <- reply
	})
	require.NoError(t, err)

	publisher := startPublisher(t, nc)
	require.NoError(t, publisher.Publish("lfx.indexer.index.fixture", []byte("x")))
	require.NoError(t, publisher.Flush())
	reply := <-received

	drained := make(chan struct{})
	go func() {
		_ = repo.DrainWithTimeout()
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("drain returned before the dispatched message replied")
	case <-time.After(300 * time.Millisecond):
	}
	_ = reply(nil)
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not return after the reply")
	}
}

func TestQueueSubscribeDispatch_RefusedWhileShuttingDown(t *testing.T) {
	nc := startCoreNATS(t)
	repo := newDispatchRepo(t, nc)
	require.NoError(t, repo.DrainWithTimeout())

	err := repo.QueueSubscribeDispatch(context.Background(), "lfx.indexer.index.>", "q", func(context.Context, []byte, string, func([]byte) error) {})
	require.ErrorContains(t, err, "shutting down")
}

func startPublisher(t *testing.T, subscriber *natsgo.Conn) *natsgo.Conn {
	t.Helper()
	nc, err := natsgo.Connect(subscriber.ConnectedUrl())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return nc
}
