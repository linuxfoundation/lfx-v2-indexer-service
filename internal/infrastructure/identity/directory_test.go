// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func testNATS(t *testing.T) *natsgo.Conn {
	t.Helper()
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	require.NoError(t, err)
	go s.Start()
	require.True(t, s.ReadyForConnections(5*time.Second))
	t.Cleanup(s.Shutdown)
	nc, err := natsgo.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return nc
}

func TestDirectory_LookupIdentity(t *testing.T) {
	tests := []struct {
		name      string
		reply     string
		wantFound bool
		wantEmail string
		wantErr   bool
	}{
		{"primary email only", `{"success":true,"data":{"primary_email":"user-1@example.com","alternate_emails":[{"email":"other@example.com"}]}}`, true, "user-1@example.com", false},
		{"missing email", `{"success":true,"data":{}}`, true, "", false},
		{"null email", `{"success":true,"data":{"primary_email":null}}`, true, "", false},
		{"empty email", `{"success":true,"data":{"primary_email":""}}`, true, "", false},
		{"not found", `{"success":false,"error":"user not found"}`, false, "", false},
		{"unavailable", `{"success":false,"error":"auth_service_unavailable"}`, false, "", true},
		{"private error", `{"success":false,"error":"private-response-token"}`, false, "", true},
		{"missing success", `{}`, false, "", true},
		{"null success", `{"success":null}`, false, "", true},
		{"missing data", `{"success":true}`, false, "", true},
		{"inconsistent", `{"success":true,"error":"private-response-token","data":{}}`, false, "", true},
		{"not found with data", `{"success":false,"error":"user not found","data":{}}`, false, "", true},
		{"invalid email type", `{"success":true,"data":{"primary_email":3}}`, false, "", true},
		{"invalid JSON", `{`, false, "", true},
		{"multiple replies", `{"success":true,"data":{}}{}`, false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nc := testNATS(t)
			sub, err := nc.Subscribe(constants.AuthUserEmailsRead, func(msg *natsgo.Msg) {
				assert.JSONEq(t, `{"user":{"auth_token":"testuser01"}}`, string(msg.Data))
				assert.NoError(t, msg.Respond([]byte(tt.reply)))
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = sub.Unsubscribe() })
			require.NoError(t, nc.Flush())
			d, err := NewDirectory(nc, time.Second)
			require.NoError(t, err)
			result, err := d.LookupIdentity(context.Background(), "testuser01")
			if tt.wantErr {
				assert.EqualError(t, err, "transient: identity lookup unavailable")
				assert.Equal(t, contracts.IdentityLookup{}, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantFound, result.Found)
				if tt.wantEmail == "" {
					assert.Nil(t, result.Email)
				} else {
					require.NotNil(t, result.Email)
					assert.Equal(t, tt.wantEmail, *result.Email)
				}
			}
		})
	}
}

func TestDirectory_TransientTransportFailures(t *testing.T) {
	for _, subscribe := range []bool{false, true} {
		t.Run(map[bool]string{false: "no responders", true: "timeout"}[subscribe], func(t *testing.T) {
			nc := testNATS(t)
			if subscribe {
				sub, err := nc.Subscribe(constants.AuthUserEmailsRead, func(*natsgo.Msg) {})
				require.NoError(t, err)
				t.Cleanup(func() { _ = sub.Unsubscribe() })
				require.NoError(t, nc.Flush())
			}
			d, err := NewDirectory(nc, 20*time.Millisecond)
			require.NoError(t, err)
			_, err = d.LookupIdentity(context.Background(), "testuser01")
			assert.EqualError(t, err, "transient: identity lookup unavailable")
			if subscribe {
				assert.True(t, errors.Is(err, context.DeadlineExceeded))
			} else {
				assert.ErrorIs(t, err, natsgo.ErrNoResponders)
			}
		})
	}
}

func TestNewDirectory(t *testing.T) {
	_, err := NewDirectory(nil, time.Second)
	assert.Error(t, err)
	nc := testNATS(t)
	_, err = NewDirectory(nc, 0)
	assert.Error(t, err)
	d, err := NewDirectory(nc, time.Second)
	require.NoError(t, err)
	_, err = d.LookupIdentity(context.Background(), "user:testuser01")
	assert.EqualError(t, err, "invalid identity principal")
}
