// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrFGANotConfigured means the indexer has no OpenFGA connection. A request
// carrying an FGA operation fails as transient, so the producer retries once
// the writer is available.
var ErrFGANotConfigured = errors.New("fga writer is not configured")

// FGARejectedError marks an FGA operation as proven invalid: retrying the same
// payload fails the same way. Any other error from FGAWriter is transient.
type FGARejectedError struct {
	Cause error
}

func (e *FGARejectedError) Error() string { return "rejected: " + e.Cause.Error() }

func (e *FGARejectedError) Unwrap() error { return e.Cause }

// FGAWriteResult reports what an FGA operation changed.
type FGAWriteResult struct {
	// Changed is true when any tuple was written or deleted. The caller must
	// then invalidate the shared FGA cache before replying.
	Changed bool
	// SeedKeys are relation keys known to be allowed after the write, for
	// seeding the cache once it has been invalidated.
	SeedKeys []string
}

// FGAWriter applies one fga-sync generic access operation (update_access,
// delete_access, member_put or member_remove) to OpenFGA.
type FGAWriter interface {
	Apply(ctx context.Context, objectType string, operation json.RawMessage) (FGAWriteResult, error)
}
