// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditJSONShape_OpaqueResourceAllocation(t *testing.T) {
	// This previously copied a 64 KiB resource key into each of 4096 array
	// descendants, allocating hundreds of MiB for a roughly 74 KiB payload.
	payload := []byte(`{"data":{"` + strings.Repeat("x", 64<<10) + `":[` + strings.Repeat("0,", 4095) + `0]}}`)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	valid := auditJSONShape(payload)
	runtime.ReadMemStats(&after)
	require.True(t, valid)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(16<<20), "opaque keys must not amplify descendant path allocations")
}

func TestAuditJSONShape_OpaqueChecksPreserved(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		valid   bool
	}{
		{name: "nested_resource_metadata_names", payload: `{"data":{"audit":{"arbitrary_resource_field":1}}}`, valid: true},
		{name: "duplicate_resource_keys", payload: `{"data":{"nested":{"name":1,"name":2}}}`},
		{name: "duplicate_snapshot_keys", payload: `{"audit":{"content":{"before":{"name":1,"name":2}}}}`},
		{name: "contact_profile_is_opaque", payload: `{"indexing_config":{"contacts":[{"profile":{"custom":1}}]}}`, valid: true},
		{name: "contact_metadata_still_closed", payload: `{"indexing_config":{"contacts":[{"unapproved_metadata":1}]}}`},
		{name: "deep_resource", payload: `{"data":` + strings.Repeat(`[`, 70) + `0` + strings.Repeat(`]`, 70) + `}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.valid, auditJSONShape([]byte(tt.payload))) })
	}
}
