// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

const (
	testStoreID  = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	otherStoreID = "01BX5ZZKBKACTAV9WEVGEMMVRZ"
	memberRemove = `{"object_type":"committee","operation":"member_remove","data":{"uid":"c1","username":"alice","relations":["member"]}}`
)

type fakeStore struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// fakeOpenFGA answers ListStores from pages and records the store id and
// authorization model id of every write.
type fakeOpenFGA struct {
	mu          sync.Mutex
	pages       func(call int, token string) ([]fakeStore, string)
	listCalls   int
	listNames   []string
	writeStores []string
	writeModels []string
}

func (f *fakeOpenFGA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/stores":
		f.listCalls++
		f.listNames = append(f.listNames, r.URL.Query().Get("name"))
		stores, next := f.pages(f.listCalls, r.URL.Query().Get("continuation_token"))
		_ = json.NewEncoder(w).Encode(map[string]any{"stores": stores, "continuation_token": next})
	case r.Method == http.MethodPost && len(r.URL.Path) > len("/stores/"):
		f.writeStores = append(f.writeStores, r.URL.Path)
		var body struct {
			AuthorizationModelID string `json:"authorization_model_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.writeModels = append(f.writeModels, body.AuthorizationModelID)
		_, _ = w.Write([]byte(`{}`))
	default:
		http.NotFound(w, r)
	}
}

func newFakeOpenFGA(t *testing.T, pages func(call int, token string) ([]fakeStore, string)) (*fakeOpenFGA, string) {
	t.Helper()
	f := &fakeOpenFGA{pages: pages}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func TestFindStore(t *testing.T) {
	tests := []struct {
		name    string
		pages   func(call int, token string) ([]fakeStore, string)
		want    string
		wantErr string
	}{
		{
			name: "one store with the name",
			pages: func(int, string) ([]fakeStore, string) {
				return []fakeStore{{ID: testStoreID, Name: "lfx-core"}}, ""
			},
			want: testStoreID,
		},
		{
			name: "store on the second page",
			pages: func(_ int, token string) ([]fakeStore, string) {
				if token == "" {
					return nil, "next"
				}
				return []fakeStore{{ID: testStoreID, Name: "lfx-core"}}, ""
			},
			want: testStoreID,
		},
		{
			name: "only stores with other names",
			pages: func(int, string) ([]fakeStore, string) {
				return []fakeStore{{ID: otherStoreID, Name: "lfx-core-old"}}, ""
			},
			wantErr: errStoreNotFound.Error(),
		},
		{
			name: "two stores with the name",
			pages: func(int, string) ([]fakeStore, string) {
				return []fakeStore{{ID: testStoreID, Name: "lfx-core"}, {ID: otherStoreID, Name: "lfx-core"}}, ""
			},
			wantErr: "2 openfga stores have this name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, url := newFakeOpenFGA(t, tt.pages)
			c, err := newSDKClient(url, "")
			require.NoError(t, err)

			got, err := c.findStore(context.Background(), "lfx-core")

			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
			for _, name := range f.listNames {
				assert.Equal(t, "lfx-core", name, "ListStores must filter by name")
			}
		})
	}
}

func TestNewLooksUpTheStoreInTheBackgroundUntilFound(t *testing.T) {
	var found atomic.Bool
	f, url := newFakeOpenFGA(t, func(call int, _ string) ([]fakeStore, string) {
		if call < 3 {
			return nil, ""
		}
		found.Store(true)
		return []fakeStore{{ID: testStoreID, Name: "lfx-core"}}, ""
	})

	w := New(Config{APIURL: url, StoreName: "lfx-core", LookupRetryInterval: 10 * time.Millisecond}, noLease{}, discardLogger())
	defer w.Close()

	require.Eventually(t, found.Load, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		_, err := apply(w, memberRemove)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Contains(t, f.writeStores, "/stores/"+testStoreID+"/write")
}

func TestApplyBeforeTheStoreIsFoundIsTransient(t *testing.T) {
	_, url := newFakeOpenFGA(t, func(int, string) ([]fakeStore, string) { return nil, "" })
	w := New(Config{APIURL: url, StoreName: "lfx-core", LookupRetryInterval: 10 * time.Millisecond}, noLease{}, discardLogger())

	_, err := apply(w, memberRemove)

	assert.ErrorIs(t, err, ErrStoreNotResolved)
	assert.False(t, isRejected(err))

	closed := make(chan struct{})
	go func() {
		w.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the store lookup")
	}
}

func TestNewWithStoreIDSkipsTheLookup(t *testing.T) {
	f, url := newFakeOpenFGA(t, func(int, string) ([]fakeStore, string) { return nil, "" })
	w := New(Config{APIURL: url, StoreName: "lfx-core", StoreID: otherStoreID}, noLease{}, discardLogger())
	defer w.Close()

	_, err := apply(w, memberRemove)

	require.NoError(t, err)
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Zero(t, f.listCalls)
	assert.Equal(t, []string{"/stores/" + otherStoreID + "/write"}, f.writeStores)
}

func TestWritesCarryTheConfiguredAuthorizationModel(t *testing.T) {
	const modelID = "01HVMMBCMGZNT3SED4Z17ECXCA"
	for name, configured := range map[string]string{"pinned": modelID, "latest": ""} {
		t.Run(name, func(t *testing.T) {
			f, url := newFakeOpenFGA(t, func(int, string) ([]fakeStore, string) { return nil, "" })
			w := New(Config{APIURL: url, StoreID: testStoreID, AuthModelID: configured}, noLease{}, discardLogger())
			defer w.Close()

			_, err := apply(w, memberRemove)

			require.NoError(t, err)
			f.mu.Lock()
			defer f.mu.Unlock()
			assert.Equal(t, []string{configured}, f.writeModels)
		})
	}
}

func TestNewWithMalformedAuthorizationModelIsNotConfigured(t *testing.T) {
	w := New(Config{APIURL: "http://openfga:8080", StoreID: testStoreID, AuthModelID: "not-a-ulid"}, noLease{}, discardLogger())
	defer w.Close()

	_, err := apply(w, memberRemove)

	assert.EqualError(t, err, contracts.ErrFGANotConfigured.Error())
}

func TestHTTPClientIsBoundedByEightSeconds(t *testing.T) {
	assert.Equal(t, 8*time.Second, newHTTPClient().Timeout)
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("OPENFGA_API_URL", "")
	t.Setenv("OPENFGA_STORE_NAME", "")
	t.Setenv("OPENFGA_STORE_ID", "")
	t.Setenv("OPENFGA_AUTH_MODEL_ID", "")
	t.Setenv("OPENFGA_LOCK_BUCKET", "")
	cfg := ConfigFromEnv()
	assert.False(t, cfg.Enabled())
	assert.Equal(t, DefaultStoreName, cfg.StoreName)
	assert.Equal(t, DefaultLockBucket, cfg.LockBucket)
	assert.Empty(t, cfg.AuthModelID)
	assert.Equal(t, defaultLookupRetryInterval, cfg.lookupRetryInterval())

	t.Setenv("OPENFGA_API_URL", "http://openfga:8080")
	t.Setenv("OPENFGA_STORE_NAME", "custom")
	t.Setenv("OPENFGA_STORE_ID", testStoreID)
	t.Setenv("OPENFGA_AUTH_MODEL_ID", otherStoreID)
	t.Setenv("OPENFGA_LOCK_BUCKET", "custom-locks")
	assert.Equal(t, Config{
		APIURL: "http://openfga:8080", StoreName: "custom", StoreID: testStoreID,
		AuthModelID: otherStoreID, LockBucket: "custom-locks",
	}, ConfigFromEnv())
}
