// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// AuditDeliveryReceipt reserves one publisher-wide ID across all origins.
// Projection operations do not create audit-history records, but their ID and
// fingerprint still need durable deduplication across retries and revisions.
type AuditDeliveryReceipt struct {
	Publisher   string            `json:"publisher"`
	EventID     string            `json:"event_id"`
	Origin      types.AuditOrigin `json:"origin"`
	Fingerprint string            `json:"fingerprint"`
}

// AuditDeliveryReceiptRepository creates or verifies a stable event identity.
type AuditDeliveryReceiptRepository interface {
	CreateOrVerify(context.Context, AuditDeliveryReceipt) error
}
