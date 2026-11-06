// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// AuditWorkflow coordinates delivery identity, history-first writes, and a
// guarded current write. The consumer rejects v2 by default; enablement
// requires approved policies and the private index setup.
type AuditWorkflow struct {
	admission *services.AuditAdmission
	history   contracts.HistoryRepository
	receipts  contracts.AuditDeliveryReceiptRepository
	current   contracts.AuditProjection
	notifier  contracts.MessagePublisher
	cleanup   contracts.AuditedCleanup
	now       func() time.Time
}

// NewAuditWorkflow requires all dependencies; nil cannot ACK an event.
func NewAuditWorkflow(admission *services.AuditAdmission, history contracts.HistoryRepository, receipts contracts.AuditDeliveryReceiptRepository, current contracts.AuditProjection, notifier contracts.MessagePublisher, cleanup contracts.AuditedCleanup) (*AuditWorkflow, error) {
	if admission == nil || history == nil || receipts == nil || current == nil || notifier == nil || cleanup == nil {
		return nil, fmt.Errorf("audit workflow requires admission, history, delivery receipts, guarded current projection, notifier and cleanup")
	}
	return &AuditWorkflow{admission: admission, history: history, receipts: receipts, current: current, notifier: notifier, cleanup: cleanup, now: time.Now}, nil
}

// Process appends an authorized mutation before attempting the current
// projection. A retry of a partial write reuses the publisher's event ID;
// history conflicts stop processing before touching current search state.
// Projection-only operations never append to history but still require trusted
// admission and the same revision guard.
func (w *AuditWorkflow) Process(ctx context.Context, payload []byte, subject string) error {
	objectType, ok := strings.CutPrefix(subject, constants.IndexPrefix)
	if !ok {
		return fmt.Errorf("invalid audited subject")
	}
	envelope, err := w.admission.Admit(ctx, payload, objectType)
	if err != nil {
		return err
	}
	fingerprint, err := contracts.AuditFingerprint(envelope, objectType)
	if err != nil {
		return err
	}
	if err := w.receipts.CreateOrVerify(ctx, contracts.AuditDeliveryReceipt{
		Publisher: envelope.Audit.Publisher, EventID: envelope.Audit.EventID,
		Origin: envelope.Audit.Origin, Fingerprint: fingerprint,
	}); err != nil {
		return fmt.Errorf("reserve audited delivery identity: %w", err)
	}
	if envelope.Audit.Origin == types.AuditOriginMutation {
		record, err := w.admission.NewRecord(envelope, objectType, w.now())
		if err != nil {
			return err
		}
		if _, err := w.history.CreateOrVerify(ctx, record); err != nil {
			// An erased ID contains no trustworthy action/resource proof.
			// Never allow it to drive a current write, even if it claims delete.
			return fmt.Errorf("append audit history: %w", err)
		}
	}
	body, applied, err := w.current.ApplyIfNewer(ctx, envelope, objectType, fingerprint)
	if err != nil {
		return fmt.Errorf("apply audited current projection: %w", err)
	}
	w.cleanup.CheckAuditedItem(objectType + ":" + envelope.IndexingConfig.ObjectID)
	if applied {
		if body == nil {
			return fmt.Errorf("guarded current projection returned no body")
		}
		event := contracts.IndexingEvent{
			DocumentID: body.ObjectRef, ObjectID: body.ObjectID, ObjectType: body.ObjectType,
			Action: envelope.Action, Body: body, Timestamp: w.now().UTC(),
			EventID: envelope.Audit.Publisher + ":" + envelope.Audit.EventID,
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode post-indexing event: %w", err)
		}
		// Existing domain events are best effort after a successful current
		// write. Replay that only restores history or returns a guarded no-op
		// sends no new event.
		_ = w.notifier.Publish(ctx, constants.BuildEventSubject(body.ObjectType, envelope.Action), encoded)
	}
	return nil
}
