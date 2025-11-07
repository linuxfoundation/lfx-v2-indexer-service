// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// PastMeetingAttachmentEnricher handles past-meeting-attachment-specific enrichment logic
type PastMeetingAttachmentEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *PastMeetingAttachmentEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches past-meeting-attachment-specific data
func (e *PastMeetingAttachmentEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides past-meeting-attachment-specific access control logic
// Attachment objects inherit permissions from their parent past meeting
func (e *PastMeetingAttachmentEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	pastMeetingLevelPermission := func(data map[string]any) string {
		if value, ok := data["past_meeting_uid"]; ok {
			if pastMeetingUID, ok := value.(string); ok {
				return fmt.Sprintf("%s:%s", constants.ObjectTypePastMeeting, pastMeetingUID)
			}
		}
		return fmt.Sprintf("%s:%s", objectType, objectID)
	}

	// Build access control values
	var accessObject, accessRelation string
	var historyObject, historyRelation string

	// Set access control with past-meeting-attachment-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		accessObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist in data - use computed default with objectType prefix
		accessObject = pastMeetingLevelPermission(data)
	}
	// If field exists but is not a string, leave empty (no override)

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		accessRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		accessRelation = "viewer"
	}

	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		historyObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		historyObject = pastMeetingLevelPermission(data)
	}

	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		historyRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		historyRelation = "auditor"
	}

	// Assign to body fields (deprecated fields)
	body.AccessCheckObject = accessObject
	body.AccessCheckRelation = accessRelation
	body.HistoryCheckObject = historyObject
	body.HistoryCheckRelation = historyRelation

	// Build and assign the query strings
	if accessObject != "" && accessRelation != "" {
		body.AccessCheckQuery = contracts.JoinFgaQuery(accessObject, accessRelation)
	}
	if historyObject != "" && historyRelation != "" {
		body.HistoryCheckQuery = contracts.JoinFgaQuery(historyObject, historyRelation)
	}
}

// extractSortName extracts the sort name from the past meeting attachment data
func (e *PastMeetingAttachmentEnricher) extractSortName(data map[string]any) string {
	if value, ok := data["name"]; ok {
		if strValue, isString := value.(string); isString && strValue != "" {
			return strings.TrimSpace(strValue)
		}
	}
	return ""
}

// extractNameAndAliases extracts the name and aliases from the past meeting attachment data
func (e *PastMeetingAttachmentEnricher) extractNameAndAliases(data map[string]any) []string {
	var nameAndAliases []string
	seen := make(map[string]bool) // Deduplicate names

	fields := []string{"file_name", "link", "name"}
	for _, field := range fields {
		if value, ok := data[field]; ok {
			if strValue, ok := value.(string); ok && strValue != "" {
				trimmed := strings.TrimSpace(strValue)
				if trimmed != "" && !seen[trimmed] {
					nameAndAliases = append(nameAndAliases, trimmed)
					seen[trimmed] = true
				}
			}
		}
	}

	return nameAndAliases
}

// NewPastMeetingAttachmentEnricher creates a new past meeting attachment enricher
func NewPastMeetingAttachmentEnricher() Enricher {
	enricher := &PastMeetingAttachmentEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypePastMeetingAttachment,
		WithAccessControl(enricher.setAccessControl),
		WithNameAndAliases(enricher.extractNameAndAliases),
		WithSortName(enricher.extractSortName),
	)
	return enricher
}
