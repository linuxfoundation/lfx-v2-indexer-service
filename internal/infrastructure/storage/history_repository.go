// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// HistoryRepository writes only to the independently provisioned history index.
// It is not part of the current-resource bulk indexer or janitor. The opt-in
// audited consumer uses it after publisher policy, mapping and credential
// checks pass at startup; it does not provide an operator-facing repair API.
type HistoryRepository struct {
	client  *opensearch.Client
	timeout time.Duration
}

const historyIndex = "resource-history-v1"

var (
	historyEventPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// NewHistoryRepository constructs a create-only history adapter. It does not
// create an index or enable audited ingestion.
func NewHistoryRepository(client *opensearch.Client) *HistoryRepository {
	return NewHistoryRepositoryWithTimeout(client, 30*time.Second)
}

// NewHistoryRepositoryWithTimeout bounds each OpenSearch request and body read.
func NewHistoryRepositoryWithTimeout(client *opensearch.Client, timeout time.Duration) *HistoryRepository {
	return &HistoryRepository{client: client, timeout: timeout}
}

// CreateOrVerify appends a mutation or confirms an earlier identical append.
// A 409 is never sufficient evidence of success by itself: the original event
// must be read back and its immutable fingerprint compared before proceeding.
func (r *HistoryRepository) CreateOrVerify(ctx context.Context, record *contracts.HistoryRecord) (bool, error) {
	if r == nil || r.client == nil || record == nil || !types.ValidAuditPublisherName(record.Publisher) ||
		!historyEventPattern.MatchString(record.EventID) || record.Fingerprint == "" {
		return false, fmt.Errorf("history repository or record is not configured")
	}
	if record.Audit.Origin != types.AuditOriginMutation {
		return false, fmt.Errorf("projection-only origins must not create history")
	}
	// Publisher and event ID are contract-validated tokens. The separator is
	// outside the publisher alphabet, preventing cross-publisher collisions.
	id := record.Publisher + ":" + record.EventID
	body, err := json.Marshal(record)
	if err != nil {
		return false, fmt.Errorf("encode history record: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	res, err := (opensearchapi.IndexRequest{
		Index: historyIndex, DocumentID: id, Body: bytes.NewReader(body), OpType: "create", Refresh: "false",
	}).Do(requestCtx, r.client)
	if err != nil {
		// Transport errors can echo the request URL, including an opaque event
		// ID that may identify a person. Keep the error out of general logs.
		return false, fmt.Errorf("create history document: transport failure")
	}
	if err := drainAuditWriteResponse(res.Body); err != nil {
		return false, err
	}
	if res.StatusCode == http.StatusConflict {
		return r.verify(ctx, id, record)
	}
	if res.IsError() {
		// OpenSearch error bodies can include indexed personal data. Do not log
		// or return them (or the user-linked document ID).
		return false, fmt.Errorf("create history document: OpenSearch status %d", res.StatusCode)
	}
	return true, nil
}

// verify distinguishes an identical retained mutation from identity misuse or a
// permanent erasure fence without fetching the potentially large private audit.
func (r *HistoryRepository) verify(ctx context.Context, id string, record *contracts.HistoryRecord) (bool, error) {
	requestCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	res, err := (opensearchapi.GetRequest{
		Index: historyIndex, DocumentID: id,
		SourceIncludes: []string{"publisher", "event_id", "fingerprint", "erased", "object_id", "object_type", "resource_revision", "action"},
	}).Do(requestCtx, r.client)
	if err != nil {
		return false, fmt.Errorf("verify existing history document: transport failure")
	}
	defer func() { _ = res.Body.Close() }()
	if res.IsError() {
		return false, fmt.Errorf("verify existing history document: OpenSearch status %d", res.StatusCode)
	}
	var found struct {
		Source struct {
			contracts.HistoryRecord
			Erased bool `json:"erased"`
		} `json:"_source"`
	}
	// OpenSearch filters out the large private snapshots before this bounded
	// response. Valid messages cannot overflow a replay check due to escaping.
	decoder := json.NewDecoder(io.LimitReader(res.Body, 8192))
	if err := decoder.Decode(&found); err != nil {
		return false, fmt.Errorf("decode existing history document")
	}
	if found.Source.Erased {
		return false, &contracts.HistoryErasedError{}
	}
	if found.Source.Publisher != record.Publisher || found.Source.EventID != record.EventID ||
		found.Source.Fingerprint != record.Fingerprint ||
		found.Source.ObjectID != record.ObjectID || found.Source.ObjectType != record.ObjectType ||
		found.Source.ResourceRevision != record.ResourceRevision || found.Source.Action != record.Action {
		return false, fmt.Errorf("history event identity reused with different content")
	}
	return false, nil
}

// Erase replaces one retained record with a minimal permanent fence in the
// same document slot. A create-only replay can no longer resurrect erased data,
// even if it races the erasure or arrives years later. Erasure is an explicit
// exception to append-only ingestion, never an age-based purge.
func (r *HistoryRepository) Erase(ctx context.Context, publisher, eventID string, erasedAt time.Time) error {
	if r == nil || r.client == nil || !types.ValidAuditPublisherName(publisher) ||
		!historyEventPattern.MatchString(eventID) || erasedAt.IsZero() {
		return fmt.Errorf("invalid history erasure request")
	}
	body, err := json.Marshal(struct {
		Publisher string    `json:"publisher"`
		EventID   string    `json:"event_id"`
		Erased    bool      `json:"erased"`
		ErasedAt  time.Time `json:"erased_at"`
	}{publisher, eventID, true, erasedAt.UTC()})
	if err != nil {
		return fmt.Errorf("encode history erasure fence: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	res, err := (opensearchapi.IndexRequest{
		Index: historyIndex, DocumentID: publisher + ":" + eventID,
		Body: bytes.NewReader(body), Refresh: "wait_for",
	}).Do(requestCtx, r.client)
	if err != nil {
		return fmt.Errorf("write history erasure fence: transport failure")
	}
	if err := drainAuditWriteResponse(res.Body); err != nil {
		return err
	}
	if res.IsError() {
		return fmt.Errorf("write history erasure fence: OpenSearch status %d", res.StatusCode)
	}
	return nil
}
