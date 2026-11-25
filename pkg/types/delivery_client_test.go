// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func stubIndexer(t *testing.T, reply string, delay time.Duration) (*nats.Conn, chan []byte) {
	t.Helper()
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	require.NoError(t, err)
	go s.Start()
	require.True(t, s.ReadyForConnections(10*time.Second))
	t.Cleanup(s.Shutdown)

	responder, err := nats.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(responder.Close)
	received := make(chan []byte, 1)
	_, err = responder.Subscribe(constants.AllIndexerIndexSubjects, func(msg *nats.Msg) {
		received <- msg.Data
		time.Sleep(delay)
		_ = msg.Respond([]byte(reply))
	})
	require.NoError(t, err)
	require.NoError(t, responder.Flush())

	producer, err := nats.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(producer.Close)
	return producer, received
}

func TestSendIndexRequest(t *testing.T) {
	tests := []struct {
		name    string
		reply   string
		delay   time.Duration
		want    Outcome
		wantErr string
	}{
		{"ok", "OK", 0, Success, ""},
		{"transient", "ERROR: transient: fga: openfga unavailable", 0, Retry, "ERROR: transient: fga: openfga unavailable"},
		{"rejected", "ERROR: rejected: index: invalid request JSON", 0, Drop, "ERROR: rejected: index: invalid request JSON"},
		{"unclassified error", "ERROR: something else", 0, Retry, "ERROR: something else"},
		{"timeout", "OK", time.Second, Retry, "no reply within"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nc, received := stubIndexer(t, tt.reply, tt.delay)
			req := IndexRequest{IndexerMessageEnvelope: *fixtureAuditEnvelope(constants.ActionUpdated), Sync: true}

			outcome, err := SendIndexRequest(context.Background(), nc, "fixture_resource", req, 200*time.Millisecond)
			require.Equal(t, tt.want, outcome)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}

			var sent map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(<-received, &sent))
			require.JSONEq(t, `true`, string(sent["sync"]))
		})
	}
}

func TestSendIndexRequest_NoResponderIsRetry(t *testing.T) {
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	require.NoError(t, err)
	go s.Start()
	require.True(t, s.ReadyForConnections(10*time.Second))
	t.Cleanup(s.Shutdown)
	nc, err := nats.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	outcome, err := SendIndexRequest(context.Background(), nc, "fixture_resource", IndexRequest{}, time.Second)
	require.Equal(t, Retry, outcome)
	require.Error(t, err)
}

func TestSendIndexRequest_InvalidObjectTypeIsDrop(t *testing.T) {
	for _, objectType := range []string{"", "a.b", "*", ">"} {
		outcome, err := SendIndexRequest(context.Background(), nil, objectType, IndexRequest{}, time.Second)
		require.Equal(t, Drop, outcome, objectType)
		require.Error(t, err)
	}
}

func TestOutcomeString(t *testing.T) {
	require.Equal(t, "success", Success.String())
	require.Equal(t, "retry", Retry.String())
	require.Equal(t, "drop", Drop.String())
}
