// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package constants provides shared constants used throughout the LFX indexer service.
package constants

import "time"

// NATS subject prefixes (preserved from existing)
// These constants define the protocol format for NATS message subjects
const (
	IndexPrefix        = "lfx.index."    // V2 message prefix
	FromV1Prefix       = "lfx.v1.index." // V1 message prefix
	EventSubjectPrefix = "lfx."          // Post-indexing domain event prefix: lfx.{object_type}.{action}
)

// BuildEventSubject constructs the NATS subject for a post-indexing domain event.
// Format: lfx.{object_type}.{action} (e.g., "lfx.project.created", "lfx.committee.deleted").
func BuildEventSubject(objectType string, action MessageAction) string {
	return EventSubjectPrefix + objectType + "." + string(action)
}

// NATS subjects (professional expansion)
const (
	ProjectSubject    = IndexPrefix + "project"  // V2 project messages
	V1ProjectSubject  = FromV1Prefix + "project" // V1 project messages
	MeetingSubject    = IndexPrefix + "meeting"  // V2 meeting messages
	SubjectIndexing   = IndexPrefix + "index"    // V2 indexing messages
	SubjectV1Indexing = FromV1Prefix + "index"   // V1 indexing messages
	AllSubjects       = IndexPrefix + ">"        // All V2 subjects
	AllV1Subjects     = FromV1Prefix + ">"       // All V1 subjects
)

// MessageAction represents the type of indexing operation to perform.
type MessageAction string

const (
	// ActionCreate indicates a resource will be created
	ActionCreate MessageAction = "create"
	// ActionCreated indicates a resource was created
	ActionCreated MessageAction = "created"
	// ActionUpdate indicates a resource will be updated
	ActionUpdate MessageAction = "update"
	// ActionUpdated indicates a resource was updated
	ActionUpdated MessageAction = "updated"
	// ActionDelete indicates a resource will be deleted
	ActionDelete MessageAction = "delete"
	// ActionDeleted indicates a resource was deleted
	ActionDeleted MessageAction = "deleted"
)

// Header constants (centralized)
const (
	HeaderXUsername = "x-username" // V1 username header
	HeaderXEmail    = "x-email"    // V1 email header
)

// Object types (centralized)
const (
	ObjectTypeProject                     = "project"
	ObjectTypeProjectSettings             = "project_settings"
	ObjectTypeCommittee                   = "committee"
	ObjectTypeCommitteeSettings           = "committee_settings"
	ObjectTypeCommitteeMember             = "committee_member"
	ObjectTypeMeeting                     = "meeting"
	ObjectTypeMeetingSettings             = "meeting_settings"
	ObjectTypeMeetingRegistrant           = "meeting_registrant"
	ObjectTypeMeetingRSVP                 = "meeting_rsvp"
	ObjectTypeMeetingAttachment           = "meeting_attachment"
	ObjectTypePastMeeting                 = "past_meeting"
	ObjectTypePastMeetingAttachment       = "past_meeting_attachment"
	ObjectTypePastMeetingParticipant      = "past_meeting_participant"
	ObjectTypePastMeetingRecording        = "past_meeting_recording"
	ObjectTypePastMeetingTranscript       = "past_meeting_transcript"
	ObjectTypePastMeetingSummary          = "past_meeting_summary"
	ObjectTypeGroupsIOService             = "groupsio_service"
	ObjectTypeGroupsIOServiceSettings     = "groupsio_service_settings"
	ObjectTypeGroupsIOMailingList         = "groupsio_mailing_list"
	ObjectTypeGroupsIOMailingListSettings = "groupsio_mailing_list_settings"
	ObjectTypeGroupsIOMember              = "groupsio_member"

	// V1 Meeting object types
	ObjectTypeV1Meeting                = "v1_meeting"
	ObjectTypeV1PastMeeting            = "v1_past_meeting"
	ObjectTypeV1MeetingRegistrant      = "v1_meeting_registrant"
	ObjectTypeV1MeetingRSVP            = "v1_meeting_rsvp"
	ObjectTypeV1PastMeetingParticipant = "v1_past_meeting_participant"
	ObjectTypeV1PastMeetingRecording   = "v1_past_meeting_recording"
	ObjectTypeV1PastMeetingTranscript  = "v1_past_meeting_transcript"
	ObjectTypeV1PastMeetingSummary     = "v1_past_meeting_summary"
)

// Message processing constants
const (
	DefaultQueue = "lfx.indexer.queue"
	RefreshTrue = "true" // OpenSearch refresh parameter — forces immediate segment refresh; use sparingly (high write latency)
	// RefreshFalse defers refresh to the next scheduled interval (default 1s).
	// Documents written with this setting are not immediately searchable — a
	// subsequent read within ~1s may not return the new document. This is the
	// preferred setting for high-throughput writes where eventual consistency
	// within 1s is acceptable.
	RefreshFalse = "false"
	ReplyTimeout = 5 * time.Second
)

// NATS pending buffer and concurrency defaults
const (
	DefaultPendingMsgLimit   = 1_000_000         // 1M messages
	DefaultPendingBytesLimit = 512 * 1024 * 1024 // 512 MiB
	DefaultWorkerCount       = 100               // concurrent message handlers
)
