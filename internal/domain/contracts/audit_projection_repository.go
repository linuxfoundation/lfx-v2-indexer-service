// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// AuditProjection atomically compares the source revision and immutable event
// identity before replacing current state. Its boolean reports body replacement,
// not history repair, an identity-only update or a guarded no-op.
type AuditProjection interface {
	ApplyIfNewer(context.Context, *types.IndexerMessageEnvelope, string, string) (*TransactionBody, bool, error)
}
