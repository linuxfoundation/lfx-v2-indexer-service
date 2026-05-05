// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

func TestBuildEventSubject(t *testing.T) {
	tests := []struct {
		objectType string
		action     constants.MessageAction
		expected   string
	}{
		{"project", constants.ActionCreated, "lfx.project.created"},
		{"project", constants.ActionUpdated, "lfx.project.updated"},
		{"project", constants.ActionDeleted, "lfx.project.deleted"},
		{"committee", constants.ActionCreated, "lfx.committee.created"},
		{"meeting", constants.ActionDeleted, "lfx.meeting.deleted"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := constants.BuildEventSubject(tt.objectType, tt.action)
			assert.Equal(t, tt.expected, got)
		})
	}
}
