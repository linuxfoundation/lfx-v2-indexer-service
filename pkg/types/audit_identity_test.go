// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidAuditPublisherName exercises the namespace grammar shared by wire,
// policy, receipt, and history validation, including path/identity separators.
func TestValidAuditPublisherName(t *testing.T) {
	for _, tt := range []struct {
		publisher string
		valid     bool
	}{
		{"a", true}, {"test-publisher_2", true}, {"a" + strings.Repeat("b", 63), true},
		{"", false}, {"Committee.Service", false}, {"a:b", false}, {"2publisher", false},
		{"publisher/child", false}, {"publisher?query", false}, {"publisher%encoded", false},
		{"a" + strings.Repeat("b", 64), false},
	} {
		t.Run(tt.publisher, func(t *testing.T) {
			require.Equal(t, tt.valid, ValidAuditPublisherName(tt.publisher))
		})
	}
}

// TestValidAuditObjectType covers the subject grammar and reserved type shared
// by wire validation, publisher policies, required-type config and routing.
func TestValidAuditObjectType(t *testing.T) {
	for _, tt := range []struct {
		name  string
		valid bool
	}{
		{"a", true}, {"fixture_resource2", true}, {"a" + strings.Repeat("b", 63), true},
		{"", false}, {"index", false}, {"Fixture", false}, {"fixture-resource", false},
		{"fixture.resource", false}, {"2fixture", false}, {"a" + strings.Repeat("b", 64), false},
		{"a" + strings.Repeat("b", 127), false},
	} {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.valid, ValidAuditObjectType(tt.name)) })
	}
}
