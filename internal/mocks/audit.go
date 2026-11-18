// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mocks

import (
	"context"
	"fmt"
	"sync"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// MockAuditHistoryRepository models immutable fingerprints and erasure fences.
type MockAuditHistoryRepository struct {
	mu      sync.Mutex
	Records map[string]string
	Erased  map[string]bool
	Fail    bool
}

// CreateOrVerify supports storage failures, identical replay and identity misuse.
func (h *MockAuditHistoryRepository) CreateOrVerify(_ context.Context, record *contracts.HistoryRecord) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Fail {
		return false, fmt.Errorf("history unavailable")
	}
	key := record.Publisher + ":" + record.EventID
	if h.Erased[key] {
		return false, &contracts.HistoryErasedError{}
	}
	if previous, exists := h.Records[key]; exists {
		if previous != record.Fingerprint {
			return false, fmt.Errorf("identity conflict")
		}
		return false, nil
	}
	if h.Records == nil {
		h.Records = map[string]string{}
	}
	h.Records[key] = record.Fingerprint
	return true, nil
}

// MockAuditReceipts reserves identities across history and projection origins.
type MockAuditReceipts struct {
	mu         sync.Mutex
	Identities map[string]contracts.AuditDeliveryReceipt
}

// CreateOrVerify rejects a changed receipt under an already reserved identity.
func (r *MockAuditReceipts) CreateOrVerify(_ context.Context, receipt contracts.AuditDeliveryReceipt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := receipt.Publisher + ":" + receipt.EventID
	if previous, exists := r.Identities[key]; exists {
		if previous != receipt {
			return fmt.Errorf("audit delivery identity conflict")
		}
		return nil
	}
	if r.Identities == nil {
		r.Identities = map[string]contracts.AuditDeliveryReceipt{}
	}
	r.Identities[key] = receipt
	return nil
}

// MockAuditProjection models mutation revision ordering, failure and metadata-only writes.
type MockAuditProjection struct {
	mu                      sync.Mutex
	Revision                uint64
	Failure, MetadataOnly   bool
	Writes                  int
	LastID, LastFingerprint string
}

// ApplyIfNewer prevents late mutations and rejects divergent equal revisions.
func (c *MockAuditProjection) ApplyIfNewer(_ context.Context, e *types.IndexerMessageEnvelope, objectType, fingerprint string) (*contracts.TransactionBody, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Failure {
		return nil, false, fmt.Errorf("current unavailable")
	}
	if c.MetadataOnly {
		return nil, false, nil
	}
	if e.Audit.ResourceRevision > c.Revision {
		c.Revision, c.LastID, c.LastFingerprint = e.Audit.ResourceRevision, e.Audit.EventID, fingerprint
		c.Writes++
		body, err := contracts.AuditProjectionBody(e, objectType)
		return body, true, err
	}
	if e.Audit.ResourceRevision == c.Revision && (e.Audit.EventID != c.LastID || fingerprint != c.LastFingerprint) {
		return nil, false, fmt.Errorf("equal revision conflict")
	}
	return nil, false, nil
}

// MockAuditNotifier records post-index attempts without a broker connection.
type MockAuditNotifier struct {
	mu      sync.Mutex
	Events  int
	Last    []byte
	Failure bool
}

// Publish can simulate a failed best-effort notification after a retained write.
func (n *MockAuditNotifier) Publish(_ context.Context, _ string, data []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Events++
	n.Last = append([]byte(nil), data...)
	if n.Failure {
		return fmt.Errorf("notification unavailable")
	}
	return nil
}

// MockAuditCleanup records audited reconciliation requests without worker goroutines.
type MockAuditCleanup struct {
	mu   sync.Mutex
	Refs []string
}

// CheckAuditedItem records the canonical resource reference queued for cleanup.
func (c *MockAuditCleanup) CheckAuditedItem(ref string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Refs = append(c.Refs, ref)
}

var (
	_ contracts.HistoryRepository              = (*MockAuditHistoryRepository)(nil)
	_ contracts.AuditDeliveryReceiptRepository = (*MockAuditReceipts)(nil)
	_ contracts.AuditProjection                = (*MockAuditProjection)(nil)
	_ contracts.MessagePublisher               = (*MockAuditNotifier)(nil)
	_ contracts.AuditedCleanup                 = (*MockAuditCleanup)(nil)
)
