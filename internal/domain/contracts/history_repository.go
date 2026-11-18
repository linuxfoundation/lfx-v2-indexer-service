// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import "context"

// HistoryErasedError indicates that this publisher/event identity has a
// permanent erasure fence rather than a retained history payload.
type HistoryErasedError struct{}

// Error deliberately omits event identity and the removed payload.
func (*HistoryErasedError) Error() string { return "history event has been explicitly erased" }

// HistoryRepository appends or verifies an immutable event. A false result
// means an identical record already existed; a conflict must return an error.
type HistoryRepository interface {
	CreateOrVerify(ctx context.Context, record *HistoryRecord) (created bool, err error)
}
