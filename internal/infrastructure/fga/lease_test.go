// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var leaseBucketSeq atomic.Int64

func startJetStream(t *testing.T) *server.Server {
	t.Helper()
	s, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true,
	})
	require.NoError(t, err)
	go s.Start()
	require.True(t, s.ReadyForConnections(10*time.Second), "nats server did not start")
	t.Cleanup(s.Shutdown)
	return s
}

// connectJetStream opens its own connection, as each replica does.
func connectJetStream(t *testing.T, s *server.Server) jetstream.JetStream {
	t.Helper()
	nc, err := natsgo.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	return js
}

func createLeaseBucket(t *testing.T, js jetstream.JetStream, ttl time.Duration) string {
	t.Helper()
	name := fmt.Sprintf("fga-locks-%d", leaseBucketSeq.Add(1))
	_, err := js.CreateKeyValue(context.Background(), jetstream.KeyValueConfig{Bucket: name, History: 1, TTL: ttl})
	require.NoError(t, err)
	return name
}

// shortLease holds for hold, so a test bucket can have a short TTL.
func shortLease(js jetstream.JetStream, bucket string, hold time.Duration) *kvLease {
	l := NewKVLease(js, bucket, discardLogger()).(*kvLease)
	l.hold = hold
	return l
}

func acquire(t *testing.T, l Lease, object string) (context.Context, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	held, release, err := l.Acquire(ctx, object)
	require.NoError(t, err)
	return held, release
}

func TestKVLeaseIsExclusiveAcrossReplicas(t *testing.T) {
	s := startJetStream(t)
	first := connectJetStream(t, s)
	bucket := createLeaseBucket(t, first, time.Minute)
	replicaA := NewKVLease(first, bucket, discardLogger())
	replicaB := NewKVLease(connectJetStream(t, s), bucket, discardLogger())

	_, release := acquire(t, replicaA, "committee:c1")

	busy, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := replicaB.Acquire(busy, "committee:c1")
	require.ErrorIs(t, err, errLeaseBusy)

	_, releaseOther := acquire(t, replicaB, "committee:c2")
	releaseOther()

	acquired := make(chan struct{})
	go func() {
		_, releaseB := acquire(t, replicaB, "committee:c1")
		releaseB()
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("the second replica acquired a held lease")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("the lease was not handed over after release")
	}
}

func TestKVLeaseHoldEndsBeforeTheLeaseExpires(t *testing.T) {
	s := startJetStream(t)
	js := connectJetStream(t, s)
	l := shortLease(js, createLeaseBucket(t, js, 2*time.Second), time.Second)

	held, release := acquire(t, l, "committee:c1")
	defer release()

	deadline, ok := held.Deadline()
	require.True(t, ok)
	assert.LessOrEqual(t, time.Until(deadline), time.Second)
}

// slowAckKV stores the key at once and returns from Create after delay, as a
// slow JetStream acknowledgement would.
type slowAckKV struct {
	jetstream.KeyValue
	delay time.Duration
}

func (s slowAckKV) Create(ctx context.Context, key string, value []byte, opts ...jetstream.KVCreateOpt) (uint64, error) {
	revision, err := s.KeyValue.Create(ctx, key, value, opts...)
	time.Sleep(s.delay)
	return revision, err
}

func slowAckLease(t *testing.T, ttl, delay time.Duration) *kvLease {
	t.Helper()
	s := startJetStream(t)
	js := connectJetStream(t, s)
	l := shortLease(js, createLeaseBucket(t, js, ttl), ttl/2)
	kv, err := l.bound(context.Background())
	require.NoError(t, err)
	l.kv = slowAckKV{KeyValue: kv, delay: delay}
	return l
}

func TestKVLeaseHoldIsMeasuredFromTheCreateAttempt(t *testing.T) {
	delay := 300 * time.Millisecond
	l := slowAckLease(t, 2*time.Second, delay)

	held, release := acquire(t, l, "committee:c1")
	defer release()

	deadline, ok := held.Deadline()
	require.True(t, ok)
	assert.LessOrEqual(t, time.Until(deadline), time.Second-delay)
}

func TestKVLeaseAcknowledgedAfterTheHoldIsBusyAndReleased(t *testing.T) {
	l := slowAckLease(t, 2*time.Second, 1500*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := l.Acquire(ctx, "committee:c1")
	require.ErrorIs(t, err, errLeaseBusy)

	_, err = l.kv.Get(ctx, leaseKey("committee:c1"))
	assert.ErrorIs(t, err, jetstream.ErrKeyNotFound)
}

func TestKVLeaseOfACrashedHolderExpires(t *testing.T) {
	s := startJetStream(t)
	js := connectJetStream(t, s)
	l := shortLease(js, createLeaseBucket(t, js, time.Second), 500*time.Millisecond)

	_, _ = acquire(t, l, "committee:c1")

	_, release := acquire(t, l, "committee:c1")
	release()
}

func TestKVLeaseStaleReleaseKeepsTheNewHolder(t *testing.T) {
	s := startJetStream(t)
	js := connectJetStream(t, s)
	l := shortLease(js, createLeaseBucket(t, js, time.Second), 500*time.Millisecond)

	_, staleRelease := acquire(t, l, "committee:c1")
	_, release := acquire(t, l, "committee:c1")
	defer release()
	staleRelease()

	busy, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := l.Acquire(busy, "committee:c1")
	assert.ErrorIs(t, err, errLeaseBusy)
}

// Lowering the bucket TTL expires a held lease early. While the holder is
// still writing, no replica, already running or started after the change,
// may take the object.
func TestKVLeaseRefusesAfterTheBucketTTLIsLowered(t *testing.T) {
	s := startJetStream(t)
	first := connectJetStream(t, s)
	bucket := createLeaseBucket(t, first, 4*time.Second)
	holder := shortLease(first, bucket, 2*time.Second)
	running := shortLease(connectJetStream(t, s), bucket, 2*time.Second)
	_, releaseOther := acquire(t, running, "committee:c2")
	releaseOther()

	held, release := acquire(t, holder, "committee:c1")
	defer release()
	_, err := first.UpdateKeyValue(context.Background(), jetstream.KeyValueConfig{Bucket: bucket, History: 1, TTL: 200 * time.Millisecond})
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)
	require.NoError(t, held.Err())

	started := shortLease(connectJetStream(t, s), bucket, 2*time.Second)
	for name, l := range map[string]Lease{"running": running, "started": started} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _, err := l.Acquire(ctx, "committee:c1")
		cancel()
		assert.ErrorIs(t, err, errLeaseUnavailable, name)
	}
}

func TestKVLeaseNeedsABucketWithTTL(t *testing.T) {
	s := startJetStream(t)
	js := connectJetStream(t, s)

	_, _, err := NewKVLease(js, "missing", discardLogger()).Acquire(context.Background(), "committee:c1")
	assert.ErrorIs(t, err, errLeaseUnavailable)

	noTTL := createLeaseBucket(t, js, 0)
	_, _, err = NewKVLease(js, noTTL, discardLogger()).Acquire(context.Background(), "committee:c1")
	assert.ErrorIs(t, err, errLeaseUnavailable)
	assert.ErrorContains(t, err, "TTL 0s is below 1m0s")
}

// tupleServer is an in-memory OpenFGA that pauses after each read, so two
// unserialized full syncs of one object would both read the same state.
type tupleServer struct {
	mu     sync.Mutex
	tuples map[client.ClientTupleKey]bool
}

func (f *tupleServer) Read(_ context.Context, req client.ClientReadRequest, _ client.ClientReadOptions) (*client.ClientReadResponse, error) {
	f.mu.Lock()
	var tuples []openfga.Tuple
	for key := range f.tuples {
		if key.Object == *req.Object {
			tuples = append(tuples, openfga.Tuple{Key: openfga.TupleKey{User: key.User, Relation: key.Relation, Object: key.Object}})
		}
	}
	f.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	return &client.ClientReadResponse{Tuples: tuples}, nil
}

func (f *tupleServer) Write(_ context.Context, req client.ClientWriteRequest, _ client.ClientWriteOptions) (*client.ClientWriteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range req.Deletes {
		delete(f.tuples, client.ClientTupleKey{User: d.User, Relation: d.Relation, Object: d.Object})
	}
	for _, w := range req.Writes {
		f.tuples[client.ClientTupleKey{User: w.User, Relation: w.Relation, Object: w.Object}] = true
	}
	return &client.ClientWriteResponse{}, nil
}

func (f *tupleServer) writers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var users []string
	for key := range f.tuples {
		if key.Relation == "writer" {
			users = append(users, key.User)
		}
	}
	slices.Sort(users)
	return users
}

// Two indexed resources whose requests name the same FGA object reach two
// replicas at once. The stored tuples must match one request, not both.
func TestFullSyncsOfOneFGATargetAcrossReplicasAreSerialized(t *testing.T) {
	s := startJetStream(t)
	first := connectJetStream(t, s)
	bucket := createLeaseBucket(t, first, time.Minute)
	openFGA := &tupleServer{tuples: map[client.ClientTupleKey]bool{}}
	replica := func(js jetstream.JetStream) *Writer {
		return &Writer{
			store:  tupleStore{client: openFGA, logger: discardLogger()},
			lease:  NewKVLease(js, bucket, discardLogger()),
			logger: discardLogger(), stop: func() {}, done: make(chan struct{}),
		}
	}
	replicas := []*Writer{replica(first), replica(connectJetStream(t, s))}

	var wg sync.WaitGroup
	for i, w := range replicas {
		payload := fmt.Sprintf(`{"object_type":"project","operation":"update_access","data":{"uid":"shared","relations":{"writer":["user-%d"]}}}`, i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := w.Apply(ctx, "fixture_resource", json.RawMessage(payload))
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.Contains(t, [][]string{{"user:user-0"}, {"user:user-1"}}, openFGA.writers())
}
