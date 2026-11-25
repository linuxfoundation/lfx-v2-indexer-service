// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Lease serializes operations on one OpenFGA object across every replica.
// Two requests for different indexed resources can share one FGA object;
// without a lease their reads, diffs and writes interleave and the stored
// tuples match neither request.
type Lease interface {
	// Acquire waits until object is held or ctx is done. The returned
	// context ends before the lease can expire, so no write outlives it;
	// release frees the lease.
	Acquire(ctx context.Context, object string) (held context.Context, release func(), err error)
}

var (
	errLeaseBusy        = errors.New("another request is writing this object")
	errLeaseUnavailable = errors.New("fga write lease unavailable")
)

const (
	leaseRetryInterval  = 25 * time.Millisecond
	leaseReleaseTimeout = time.Second
	// leaseHold is fixed rather than derived from the bucket TTL, so every
	// replica, whenever it started, holds a lease for the same time.
	leaseHold = 30 * time.Second
)

// kvLease holds a lease as a key in a NATS KV bucket. Create succeeds for
// one caller only; the bucket TTL frees the lease of a crashed holder.
type kvLease struct {
	js     jetstream.JetStream
	bucket string
	hold   time.Duration
	logger *slog.Logger

	mu sync.Mutex
	kv jetstream.KeyValue
}

// NewKVLease returns a Lease kept in bucket. The bucket is bound on first use
// and its TTL must be at least twice leaseHold.
func NewKVLease(js jetstream.JetStream, bucket string, logger *slog.Logger) Lease {
	return &kvLease{js: js, bucket: bucket, hold: leaseHold, logger: logger}
}

func (l *kvLease) Acquire(ctx context.Context, object string) (context.Context, func(), error) {
	kv, err := l.bound(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errLeaseUnavailable, err)
	}
	key := leaseKey(object)
	for {
		// The TTL starts when NATS stores the key, after attempt, so a hold
		// measured from attempt ends before the key expires however late the
		// acknowledgement arrives.
		attempt := time.Now()
		revision, err := kv.Create(ctx, key, nil)
		if err == nil {
			// Checked after Create: a TTL lowered far enough to expire the
			// previous holder's key early is visible once this key exists.
			if err := l.checkTTL(ctx, kv); err != nil {
				l.release(kv, key, revision)
				return nil, nil, fmt.Errorf("%w: %w", errLeaseUnavailable, err)
			}
			held, cancel := context.WithDeadline(ctx, attempt.Add(l.hold))
			if held.Err() != nil {
				cancel()
				l.release(kv, key, revision)
				return nil, nil, errLeaseBusy
			}
			return held, func() {
				cancel()
				l.release(kv, key, revision)
			}, nil
		}
		if ctx.Err() != nil {
			return nil, nil, errLeaseBusy
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return nil, nil, fmt.Errorf("%w: %w", errLeaseUnavailable, err)
		}
		timer := time.NewTimer(leaseRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, errLeaseBusy
		case <-timer.C:
		}
	}
}

// release deletes the lease only at the revision this holder created, so a
// lease that expired and was taken by another holder is left alone.
func (l *kvLease) release(kv jetstream.KeyValue, key string, revision uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), leaseReleaseTimeout)
	defer cancel()
	if err := kv.Delete(ctx, key, jetstream.LastRevision(revision)); err != nil {
		l.logger.Warn("FGA write lease release failed; it expires with the bucket TTL",
			"bucket", l.bucket, "error", err.Error())
	}
}

func (l *kvLease) bound(ctx context.Context) (jetstream.KeyValue, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.kv != nil {
		return l.kv, nil
	}
	kv, err := l.js.KeyValue(ctx, l.bucket)
	if err != nil {
		return nil, fmt.Errorf("bind KV bucket %q: %w", l.bucket, err)
	}
	l.kv = kv
	return l.kv, nil
}

// checkTTL requires a bucket TTL of at least twice the hold, so a holder
// stops writing well before its lease can expire.
func (l *kvLease) checkTTL(ctx context.Context, kv jetstream.KeyValue) error {
	status, err := kv.Status(ctx)
	if err != nil {
		return fmt.Errorf("KV bucket %q status: %w", l.bucket, err)
	}
	if ttl := status.TTL(); ttl < 2*l.hold {
		return fmt.Errorf("KV bucket %q TTL %s is below %s, twice the lease hold", l.bucket, ttl, 2*l.hold)
	}
	return nil
}

// leaseKey hashes object, because OpenFGA ids may hold characters NATS KV
// keys do not allow.
func leaseKey(object string) string {
	sum := sha256.Sum256([]byte(object))
	return hex.EncodeToString(sum[:])
}
