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
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

const auditDeliveryReceiptsIndex = "resource-audit-deliveries-v1"

// AuditDeliveryReceiptRepository keeps delivery identity separate from
// mutation history. It stores no resource payload, actor or transport header.
type AuditDeliveryReceiptRepository struct {
	client  *opensearch.Client
	timeout time.Duration
}

// NewAuditDeliveryReceiptRepository creates an adapter for the private receipts index.
func NewAuditDeliveryReceiptRepository(client *opensearch.Client, timeout time.Duration) *AuditDeliveryReceiptRepository {
	return &AuditDeliveryReceiptRepository{client: client, timeout: timeout}
}

// CreateOrVerify refuses reuse of a publisher/event ID with changed content.
// A successful receipt does not assert that the current projection write has
// succeeded; the workflow retries that write after any partial failure.
func (r *AuditDeliveryReceiptRepository) CreateOrVerify(ctx context.Context, receipt contracts.AuditDeliveryReceipt) error {
	if r == nil || r.client == nil || r.timeout <= 0 || !types.ValidAuditPublisherName(receipt.Publisher) ||
		!historyEventPattern.MatchString(receipt.EventID) || len(receipt.Fingerprint) != 64 ||
		(receipt.Origin != types.AuditOriginMutation && receipt.Origin != types.AuditOriginReindex &&
			receipt.Origin != types.AuditOriginBackfill && receipt.Origin != types.AuditOriginSyncEcho) {
		return fmt.Errorf("projection receipt is not configured")
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode projection receipt: %w", err)
	}
	id := receipt.Publisher + ":" + receipt.EventID
	requestCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	res, err := (opensearchapi.IndexRequest{
		Index: auditDeliveryReceiptsIndex, DocumentID: id,
		Body: bytes.NewReader(encoded), OpType: "create", Refresh: "false",
	}).Do(requestCtx, r.client)
	if err != nil {
		return fmt.Errorf("create projection receipt: transport failure")
	}
	if err := drainAuditWriteResponse(res.Body); err != nil {
		return err
	}
	if res.StatusCode == http.StatusConflict {
		return r.verify(ctx, id, receipt)
	}
	if res.IsError() {
		return fmt.Errorf("create projection receipt: OpenSearch status %d", res.StatusCode)
	}
	return nil
}

// verify reads the minimal existing receipt after a create conflict, rejecting
// erased identities and changed-content/origin reuse before downstream writes.
func (r *AuditDeliveryReceiptRepository) verify(ctx context.Context, id string, receipt contracts.AuditDeliveryReceipt) error {
	requestCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	res, err := (opensearchapi.GetRequest{
		Index: auditDeliveryReceiptsIndex, DocumentID: id,
		SourceIncludes: []string{"publisher", "event_id", "origin", "fingerprint", "erased"},
	}).Do(requestCtx, r.client)
	if err != nil {
		return fmt.Errorf("verify projection receipt: transport failure")
	}
	defer func() { _ = res.Body.Close() }()
	if res.IsError() {
		return fmt.Errorf("verify projection receipt: OpenSearch status %d", res.StatusCode)
	}
	var found struct {
		Source struct {
			contracts.AuditDeliveryReceipt
			Erased bool `json:"erased"`
		} `json:"_source"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&found); err != nil {
		return fmt.Errorf("decode projection receipt")
	}
	if found.Source.Erased || found.Source.Publisher != receipt.Publisher || found.Source.EventID != receipt.EventID ||
		found.Source.Origin != receipt.Origin ||
		found.Source.Fingerprint != receipt.Fingerprint {
		return fmt.Errorf("projection event identity reused or erased")
	}
	return nil
}
