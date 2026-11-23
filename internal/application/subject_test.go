// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestObjectTypeFromSubject(t *testing.T) {
	tests := []struct {
		subject    string
		objectType string
		ok         bool
	}{
		{subject: "lfx.index.committee", objectType: "committee", ok: true},
		{subject: "lfx.indexer.index.committee", objectType: "committee", ok: true},
		{subject: "lfx.indexer.index.committee_member", objectType: "committee_member", ok: true},
		{subject: "lfx.index.", objectType: "", ok: true},
		{subject: "lfx.indexer.committee", ok: false},
		{subject: "lfx.committee.created", ok: false},
		{subject: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.subject, func(t *testing.T) {
			objectType, ok := ObjectTypeFromSubject(tt.subject)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.objectType, objectType)
		})
	}
}

// ObjectTypeFromSubject replaces strings.CutPrefix(subject, "lfx.index."), so
// on that prefix it must return exactly what CutPrefix did.
func TestObjectTypeFromSubjectMatchesIndexPrefixCut(t *testing.T) {
	for _, subject := range []string{"lfx.index.project", "lfx.index.a.b", "lfx.index.", "lfx.indexx.project"} {
		want, wantOK := strings.CutPrefix(subject, "lfx.index.")
		got, gotOK := ObjectTypeFromSubject(subject)
		assert.Equal(t, wantOK, gotOK, subject)
		if wantOK {
			assert.Equal(t, want, got, subject)
		}
	}
}
