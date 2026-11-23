// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/fga"
)

// openFGAURL returns TEST_OPENFGA_URL or skips. Each run creates its own
// store, so any disposable OpenFGA server works.
func openFGAURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_OPENFGA_URL")
	if url == "" {
		t.Skip("TEST_OPENFGA_URL is not set")
	}
	return url
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// newIntegrationStore creates a store with the test model and returns an
// admin client bound to it, for reading tuples back.
func newIntegrationStore(t *testing.T, url, name string) *client.OpenFgaClient {
	t.Helper()
	ctx := testContext(t)
	admin, err := client.NewSdkClient(&client.ClientConfiguration{ApiUrl: url})
	require.NoError(t, err)
	store, err := admin.CreateStore(ctx).Body(client.ClientCreateStoreRequest{Name: name}).Execute()
	require.NoError(t, err)
	require.NoError(t, admin.SetStoreId(store.Id))
	t.Cleanup(func() {
		_, _ = admin.DeleteStore(context.Background()).Execute()
	})

	raw, err := os.ReadFile(filepath.Join("testdata", "model.json"))
	require.NoError(t, err)
	var model client.ClientWriteAuthorizationModelRequest
	require.NoError(t, json.Unmarshal(raw, &model))
	_, err = admin.WriteAuthorizationModel(ctx).Body(model).Execute()
	require.NoError(t, err)
	return admin
}

// readTuples returns every tuple on object as "relation@user", sorted.
func readTuples(t *testing.T, admin *client.OpenFgaClient, object string) []string {
	t.Helper()
	ctx := testContext(t)
	options := client.ClientReadOptions{PageSize: openfga.PtrInt32(50)}
	var got []string
	for {
		resp, err := admin.Read(ctx).Body(client.ClientReadRequest{Object: openfga.PtrString(object)}).Options(options).Execute()
		require.NoError(t, err)
		for _, tuple := range resp.Tuples {
			got = append(got, tuple.Key.Relation+"@"+tuple.Key.User)
		}
		if resp.ContinuationToken == "" {
			break
		}
		options.ContinuationToken = openfga.PtrString(resp.ContinuationToken)
	}
	slices.Sort(got)
	if len(got) <= 10 {
		t.Logf("read back %s: %d tuples %v", object, len(got), got)
	} else {
		t.Logf("read back %s: %d tuples", object, len(got))
	}
	return got
}

func writeTuple(t *testing.T, admin *client.OpenFgaClient, object, relation, user string) {
	t.Helper()
	_, err := admin.WriteTuples(testContext(t)).Body(client.ClientWriteTuplesBody{
		{User: user, Relation: relation, Object: object},
	}).Execute()
	require.NoError(t, err)
}

func apply(t *testing.T, w *fga.Writer, payload string) (contracts.FGAWriteResult, error) {
	t.Helper()
	result, err := w.Apply(testContext(t), "committee", json.RawMessage(payload))
	var rejected *contracts.FGARejectedError
	t.Logf("apply: changed=%t seed_keys=%d rejected=%t err=%v", result.Changed, len(result.SeedKeys), errors.As(err, &rejected), err)
	return result, err
}

func mustApply(t *testing.T, w *fga.Writer, payload string) contracts.FGAWriteResult {
	t.Helper()
	result, err := apply(t, w, payload)
	require.NoError(t, err)
	return result
}

func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newResolvedWriter builds a Writer that finds the store by name, and waits
// for the background lookup to succeed.
func newResolvedWriter(t *testing.T, url, storeName string) *fga.Writer {
	t.Helper()
	w := fga.New(fga.Config{APIURL: url, StoreName: storeName, LookupRetryInterval: 50 * time.Millisecond}, logger())
	t.Cleanup(w.Close)
	probe := `{"object_type":"committee","operation":"delete_access","data":{"uid":"lookup-probe"}}`
	require.Eventually(t, func() bool {
		_, err := apply(t, w, probe)
		return !errors.Is(err, fga.ErrStoreNotResolved)
	}, 10*time.Second, 50*time.Millisecond, "store %q was not found by name", storeName)
	result, err := apply(t, w, probe)
	require.NoError(t, err)
	assert.False(t, result.Changed)
	t.Logf("store %q found by name", storeName)
	return w
}

func TestIntegration(t *testing.T) {
	url := openFGAURL(t)
	storeName := fmt.Sprintf("gh1525-it-%d", time.Now().UnixNano())
	admin := newIntegrationStore(t, url, storeName)
	w := newResolvedWriter(t, url, storeName)

	t.Run("update_access full sync", func(t *testing.T) { testUpdateAccess(t, admin, w) })
	t.Run("update_access exclude_relations and team grants", func(t *testing.T) { testExcludeRelations(t, admin, w) })
	t.Run("member_put mutually exclusive relations", func(t *testing.T) { testMemberPut(t, admin, w) })
	t.Run("member_remove", func(t *testing.T) { testMemberRemove(t, admin, w) })
	t.Run("delete_access", func(t *testing.T) { testDeleteAccess(t, admin, w) })
	t.Run("paging past one page of tuples", func(t *testing.T) { testPaging(t, admin, w) })
	t.Run("rejected payload writes nothing", func(t *testing.T) { testRejected(t, admin, w) })
	t.Run("OpenFGA unreachable is transient", testUnreachable)
}

func testUpdateAccess(t *testing.T, admin *client.OpenFgaClient, w *fga.Writer) {
	// "bogus" is not in the model: OpenFGA rejects it with validation_error,
	// it is dropped, and the rest is written.
	payload := `{"object_type":"committee","operation":"update_access","data":{"uid":"c-update","public":true,` +
		`"relations":{"writer":["alice"],"member":["bob","carol"],"bogus":["dave"]},` +
		`"references":{"project":["p1"],"parent":["c-parent"]}}}`

	result := mustApply(t, w, payload)

	assert.True(t, result.Changed)
	assert.Equal(t, []string{
		"committee:c-update#member@user:bob", "committee:c-update#member@user:carol",
		"committee:c-update#viewer@user:*", "committee:c-update#writer@user:alice",
	}, result.SeedKeys)
	assert.Equal(t, []string{
		"member@user:bob", "member@user:carol", "parent@committee:c-parent",
		"project@project:p1", "viewer@user:*", "writer@user:alice",
	}, readTuples(t, admin, "committee:c-update"))

	replay := mustApply(t, w, payload)
	assert.False(t, replay.Changed, "replaying the same full sync changes nothing")

	result = mustApply(t, w, `{"object_type":"committee","operation":"update_access","data":{"uid":"c-update",`+
		`"relations":{"member":["carol"]},"references":{"project":["p1"]}}}`)
	assert.True(t, result.Changed)
	assert.Empty(t, result.SeedKeys, "no user tuple was newly written")
	assert.Equal(t, []string{"member@user:carol", "project@project:p1"}, readTuples(t, admin, "committee:c-update"))
}

func testExcludeRelations(t *testing.T, admin *client.OpenFgaClient, w *fga.Writer) {
	object := "committee:c-exclude"
	writeTuple(t, admin, object, "participant", "user:managed-elsewhere")
	writeTuple(t, admin, object, "member", "user:old-member")
	writeTuple(t, admin, object, "auditor", "team:auditors#member")
	writeTuple(t, admin, object, "global_writer", "team:global-writers#member")

	result := mustApply(t, w, `{"object_type":"committee","operation":"update_access","data":{"uid":"c-exclude",`+
		`"relations":{"member":["new-member"]},"exclude_relations":["participant"]}}`)

	assert.True(t, result.Changed)
	assert.Equal(t, []string{
		"auditor@team:auditors#member", "member@user:new-member", "participant@user:managed-elsewhere",
	}, readTuples(t, admin, object), "excluded relation and non-global team grant survive; global_ team grant is removed")
}

func testMemberPut(t *testing.T, admin *client.OpenFgaClient, w *fga.Writer) {
	object := "meeting:m-put"
	writeTuple(t, admin, object, "organizer", "user:alice")

	result := mustApply(t, w, `{"object_type":"meeting","operation":"member_put","data":{"uid":"m-put","username":"alice","relations":["host"]}}`)
	assert.Equal(t, contracts.FGAWriteResult{Changed: true}, result)
	assert.Equal(t, []string{"host@user:alice", "organizer@user:alice"}, readTuples(t, admin, object))

	result = mustApply(t, w, `{"object_type":"meeting","operation":"member_put","data":{"uid":"m-put","username":"alice",`+
		`"relations":["participant"],"mutually_exclusive_with":["host","participant"]}}`)
	assert.True(t, result.Changed)
	assert.Equal(t, []string{"organizer@user:alice", "participant@user:alice"}, readTuples(t, admin, object),
		"host is replaced by participant; organizer is not in the exclusive set")

	result = mustApply(t, w, `{"object_type":"meeting","operation":"member_put","data":{"uid":"m-put","username":"alice",`+
		`"relations":["participant"],"mutually_exclusive_with":["host","participant"]}}`)
	assert.False(t, result.Changed, "the member already has the relation")
}

func testMemberRemove(t *testing.T, admin *client.OpenFgaClient, w *fga.Writer) {
	object := "meeting:m-remove"
	for _, relation := range []string{"host", "participant", "organizer"} {
		writeTuple(t, admin, object, relation, "user:alice")
	}
	writeTuple(t, admin, object, "host", "user:bob")

	result := mustApply(t, w, `{"object_type":"meeting","operation":"member_remove","data":{"uid":"m-remove","username":"alice","relations":["host",""]}}`)
	assert.True(t, result.Changed)
	assert.Equal(t, []string{"host@user:bob", "organizer@user:alice", "participant@user:alice"}, readTuples(t, admin, object))

	result = mustApply(t, w, `{"object_type":"meeting","operation":"member_remove","data":{"uid":"m-remove","username":"alice","relations":[]}}`)
	assert.True(t, result.Changed)
	assert.Equal(t, []string{"host@user:bob"}, readTuples(t, admin, object))

	result = mustApply(t, w, `{"object_type":"meeting","operation":"member_remove","data":{"uid":"m-remove","username":"alice"}}`)
	assert.False(t, result.Changed, "nothing is left to remove")
}

func testDeleteAccess(t *testing.T, admin *client.OpenFgaClient, w *fga.Writer) {
	object := "committee:c-delete"
	mustApply(t, w, `{"object_type":"committee","operation":"update_access","data":{"uid":"c-delete","public":true,`+
		`"relations":{"writer":["alice"]},"references":{"project":["p1"]}}}`)
	writeTuple(t, admin, object, "auditor", "team:auditors#member")

	result := mustApply(t, w, `{"object_type":"committee","operation":"delete_access","data":{"uid":"c-delete"}}`)

	assert.Equal(t, contracts.FGAWriteResult{Changed: true}, result)
	assert.Equal(t, []string{"auditor@team:auditors#member"}, readTuples(t, admin, object))
}

func testPaging(t *testing.T, admin *client.OpenFgaClient, w *fga.Writer) {
	members := make([]string, 130)
	var want []string
	for i := range members {
		members[i] = fmt.Sprintf("member-%03d", i)
		want = append(want, "member@user:"+members[i])
	}
	raw, err := json.Marshal(members)
	require.NoError(t, err)
	payload := `{"object_type":"committee","operation":"update_access","data":{"uid":"c-paging","relations":{"member":` + string(raw) + `}}}`

	// 130 writes span two Write batches.
	result := mustApply(t, w, payload)
	assert.True(t, result.Changed)
	assert.Len(t, result.SeedKeys, 130)
	assert.Equal(t, want, readTuples(t, admin, "committee:c-paging"))

	// The replay must read all three default-size pages to find nothing to do.
	replay := mustApply(t, w, payload)
	assert.False(t, replay.Changed)

	result = mustApply(t, w, `{"object_type":"committee","operation":"delete_access","data":{"uid":"c-paging"}}`)
	assert.True(t, result.Changed)
	assert.Empty(t, readTuples(t, admin, "committee:c-paging"), "130 deletes span two Write batches")
}

func testRejected(t *testing.T, admin *client.OpenFgaClient, w *fga.Writer) {
	payloads := []string{
		`{"object_type":"committee","operation":"member_put","data":{"uid":"c-rejected","username":"alice","relations":[]}}`,
		`{"object_type":"committee","operation":"update_access","data":{"uid":"c-rejected","references":{"project":[":p1"]}}}`,
		`{"object_type":"committee","operation":"not_an_operation","data":{"uid":"c-rejected"}}`,
	}
	for _, payload := range payloads {
		result, err := apply(t, w, payload)

		var rejectedErr *contracts.FGARejectedError
		require.ErrorAs(t, err, &rejectedErr, payload)
		assert.False(t, result.Changed)
	}
	assert.Empty(t, readTuples(t, admin, "committee:c-rejected"))
}

func testUnreachable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedURL := "http://" + listener.Addr().String()
	require.NoError(t, listener.Close())
	payload := `{"object_type":"committee","operation":"update_access","data":{"uid":"c-unreachable","public":true}}`

	byID := fga.New(fga.Config{APIURL: closedURL, StoreID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}, logger())
	defer byID.Close()
	_, err = apply(t, byID, payload)
	require.Error(t, err)
	var rejectedErr *contracts.FGARejectedError
	assert.False(t, errors.As(err, &rejectedErr), "an unreachable OpenFGA is transient: %v", err)

	byName := fga.New(fga.Config{APIURL: closedURL, StoreName: "lfx-core", LookupRetryInterval: 50 * time.Millisecond}, logger())
	defer byName.Close()
	_, err = apply(t, byName, payload)
	assert.ErrorIs(t, err, fga.ErrStoreNotResolved)
	assert.False(t, errors.As(err, &rejectedErr))
}
