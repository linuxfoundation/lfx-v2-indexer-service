// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package constants_test

import (
	"testing"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/stretchr/testify/assert"
)

func TestBuildEventSubject(t *testing.T) {
	tests := []struct {
		objectType string
		action     constants.MessageAction
		expected   string
	}{
		{constants.ObjectTypeProject, constants.ActionCreated, "lfx.project.created"},
		{constants.ObjectTypeProject, constants.ActionUpdated, "lfx.project.updated"},
		{constants.ObjectTypeProject, constants.ActionDeleted, "lfx.project.deleted"},
		{constants.ObjectTypeCommittee, constants.ActionCreated, "lfx.committee.created"},
		{constants.ObjectTypeMeeting, constants.ActionDeleted, "lfx.meeting.deleted"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := constants.BuildEventSubject(tt.objectType, tt.action)
			assert.Equal(t, tt.expected, got)
		})
	}
}
