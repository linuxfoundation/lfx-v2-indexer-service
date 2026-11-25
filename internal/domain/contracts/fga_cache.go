// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"context"
	"errors"
	"time"
)

// ErrCacheBypassed means the FGA cache was not consulted. The caller must
// answer from OpenFGA instead.
var ErrCacheBypassed = errors.New("fga cache bypassed")

// InvalidationMarker is the cut-off read from the cache's invalidation key.
// Callers treat it as opaque: it is a time on NATS KV and a generation and
// counter on Valkey.
type InvalidationMarker struct {
	at         time.Time
	revision   uint64
	generation string
	counter    int64
}

// NewTimeMarker returns a marker for backends that compare store write times.
func NewTimeMarker(at time.Time) InvalidationMarker {
	return InvalidationMarker{at: at}
}

// WithRevision returns m carrying the store revision of the invalidation key,
// zero when the key is absent, for backends whose Invalidate compares it.
func (m InvalidationMarker) WithRevision(revision uint64) InvalidationMarker {
	m.revision = revision
	return m
}

// Revision returns the store revision set by WithRevision.
func (m InvalidationMarker) Revision() uint64 {
	return m.revision
}

// NewCounterMarker returns a marker for backends that compare an
// invalidation counter within a generation of the store's contents.
func NewCounterMarker(generation string, counter int64) InvalidationMarker {
	return InvalidationMarker{generation: generation, counter: counter}
}

// Time returns the cut-off time of a time-based marker.
func (m InvalidationMarker) Time() time.Time {
	return m.at
}

// Generation returns the generation of a counter-based marker.
func (m InvalidationMarker) Generation() string {
	return m.generation
}

// Counter returns the counter value of a counter-based marker.
func (m InvalidationMarker) Counter() int64 {
	return m.counter
}

// FGACache caches OpenFGA check results keyed by relation key
// ("<object>#<relation>@<user>"). Get never returns an entry written before
// the marker it is given, and Set does not keep results whose check
// overlapped a later invalidation.
//
// Every method returns ErrCacheBypassed when the cache is off, down, or
// slower than its per-call timeout; the caller then asks OpenFGA.
type FGACache interface {
	// CurrentMarker reads the invalidation marker. Call it before an OpenFGA
	// check and pass the result to Get and Set for that check. If it returns
	// an error, skip Get and Set for that check.
	CurrentMarker(ctx context.Context) (InvalidationMarker, error)

	// Get returns the cached result for each relation key that is present
	// and fresh under m. Absent or stale keys are omitted.
	Get(ctx context.Context, relationKeys []string, m InvalidationMarker) (map[string]bool, error)

	// Set writes check results under the marker read before the check. If an
	// invalidation landed since m, the results are not kept; on NATS KV a
	// concurrent Get can still see them until they are hidden when the
	// invalidation lands while they are being written. If they cannot be
	// hidden the cache stays down until they are. Callers must not pass
	// results that carried an error.
	Set(ctx context.Context, results map[string]bool, m InvalidationMarker) error

	// Invalidate records an invalidation and returns the marker of that
	// write, for Seed. before is the marker read just before the OpenFGA
	// write; onlyMine reports that no other invalidation landed between
	// before and this one. It is attempted even while the cache is marked
	// down; an error means the invalidation may not have been recorded.
	Invalidate(ctx context.Context, before InvalidationMarker) (m InvalidationMarker, onlyMine bool, err error)

	// Seed writes true for relation keys just written to OpenFGA, under the
	// marker returned by the Invalidate that followed that write. Seed only
	// when that Invalidate reported onlyMine: another invalidation in between
	// may belong to a later change to the same tuples, such as a removal,
	// which the seed would undo.
	Seed(ctx context.Context, relationKeys []string, m InvalidationMarker) error

	// HealthCheck reports whether the store is reachable. It is never part
	// of readiness.
	HealthCheck(ctx context.Context) error
}
