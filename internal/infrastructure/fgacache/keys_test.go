// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The fixture file is byte-identical to lfx-v2-fga-sync's
// testdata/cache_key_golden.json; both repositories must produce these keys.
type goldenFixtures struct {
	InvalidationKey string `json:"invalidation_key"`
	Cases           []struct {
		Name     string `json:"name"`
		Object   string `json:"object"`
		Relation string `json:"relation"`
		User     string `json:"user"`
		Want     string `json:"want"`
	} `json:"cases"`
}

func TestCacheKeyGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/cache_key_golden.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var fixtures goldenFixtures
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	if len(fixtures.Cases) == 0 {
		t.Fatal("no fixture cases")
	}

	if InvalidationKey != fixtures.InvalidationKey {
		t.Errorf("InvalidationKey = %q, want %q", InvalidationKey, fixtures.InvalidationKey)
	}
	for _, tc := range fixtures.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := CacheKey(RelationKey(tc.Object, tc.Relation, tc.User))
			if got != tc.Want {
				t.Errorf("CacheKey = %q, want %q", got, tc.Want)
			}
		})
	}
}

// The marker keys must equal lfx-v2-fga-sync's typeMarkerKey, userMarkerKey
// and modelMarkerKey (fga_invalidation.go); the encoded users were computed
// independently with base32 (RFC 4648, no padding).
func TestScopedMarkerKeys(t *testing.T) {
	if ModelMarkerKey != "inv.model" {
		t.Errorf("ModelMarkerKey = %q, want %q", ModelMarkerKey, "inv.model")
	}
	tests := []struct {
		name         string
		relationKeys []string
		want         []string
	}{
		{"concrete user", []string{"project:p1#writer@user:example-user"},
			[]string{"inv.t.project", "inv.u.project.OVZWK4R2MV4GC3LQNRSS25LTMVZA"}},
		{"non-ascii user", []string{"project:p1#viewer@user:usér-ñame"},
			[]string{"inv.t.project", "inv.u.project.OVZWK4R2OVZ4HKLSFXB3CYLNMU"}},
		{"userset subject has only the type marker", []string{"committee:c1#member@group:g1#member"},
			[]string{"inv.t.committee"}},
		{"wildcard has only the type marker", []string{"project:p1#viewer@user:*"},
			[]string{"inv.t.project"}},
		{"distinct markers once", []string{"project:p1#writer@user:example-user", "project:p2#viewer@user:example-user"},
			[]string{"inv.t.project", "inv.u.project.OVZWK4R2MV4GC3LQNRSS25LTMVZA"}},
		{"type fga-sync cannot scope", []string{"bad.type:p1#writer@user:example-user"}, nil},
		{"object without id", []string{"project:#writer@user:example-user"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scopedMarkerKeys(tt.relationKeys); !slices.Equal(got, tt.want) {
				t.Errorf("scopedMarkerKeys = %q, want %q", got, tt.want)
			}
		})
	}
}
