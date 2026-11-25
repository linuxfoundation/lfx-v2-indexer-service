// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func TestIndexRequestFlattensEnvelope(t *testing.T) {
	req := IndexRequest{
		IndexerMessageEnvelope: *fixtureAuditEnvelope(constants.ActionUpdated),
		FGA:                    json.RawMessage(`{"object_type":"fixture_resource","operation":"delete_access","data":{"uid":"r1"}}`),
		Sync:                   true,
	}
	payload, err := json.Marshal(req)
	require.NoError(t, err)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &fields))
	for _, key := range []string{"schema_version", "audit", "action", "data", "fga", "sync"} {
		require.Contains(t, fields, key)
	}
	require.NotContains(t, fields, "IndexerMessageEnvelope")

	var decoded IndexRequest
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.True(t, decoded.Sync)
	require.JSONEq(t, string(req.FGA), string(decoded.FGA))
	require.Equal(t, req.Audit.EventID, decoded.Audit.EventID)
}

func TestIndexRequestOmitsUnsetFields(t *testing.T) {
	payload, err := json.Marshal(IndexRequest{IndexerMessageEnvelope: *fixtureAuditEnvelope(constants.ActionUpdated)})
	require.NoError(t, err)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &fields))
	require.NotContains(t, fields, "fga")
	require.NotContains(t, fields, "sync")
}

// The lfx.index.* path must keep refusing fga and sync: they are only applied
// on lfx.indexer.index.*.
func TestDecodeV2EnvelopeRejectsIndexRequestFields(t *testing.T) {
	for name, req := range map[string]IndexRequest{
		"fga":  {FGA: json.RawMessage(`{"object_type":"fixture_resource","operation":"delete_access","data":{"uid":"r1"}}`)},
		"sync": {Sync: true},
	} {
		t.Run(name, func(t *testing.T) {
			req.IndexerMessageEnvelope = *fixtureAuditEnvelope(constants.ActionUpdated)
			payload, err := json.Marshal(req)
			require.NoError(t, err)

			_, err = DecodeV2Envelope(payload, "fixture_resource", fixtureAuditValidator)
			require.ErrorContains(t, err, "invalid version 2 JSON envelope")
		})
	}
}
