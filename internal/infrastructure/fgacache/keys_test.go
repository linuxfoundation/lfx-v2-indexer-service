// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fgacache

import (
	"encoding/json"
	"os"
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
