// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// MeetingRSVPEnricher handles meeting-rsvp-specific enrichment logic
type MeetingRSVPEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *MeetingRSVPEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches meeting-rsvp-specific data
func (e *MeetingRSVPEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides meeting-rsvp-specific access control logic
// RSVP objects inherit permissions from their parent meeting
func (e *MeetingRSVPEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	meetingLevelPermission := func(data map[string]any) string {
		if value, ok := data["meeting_uid"]; ok {
			if meetingUID, ok := value.(string); ok {
				return fmt.Sprintf("%s:%s", constants.ObjectTypeMeeting, meetingUID)
			}
		}
		return fmt.Sprintf("%s:%s", objectType, objectID)
	}

	// Build access control values
	var accessObject, accessRelation string
	var historyObject, historyRelation string

	// Set access control with meeting-rsvp-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		accessObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist in data - use computed default with objectType prefix
		accessObject = meetingLevelPermission(data)
	}
	// If field exists but is not a string, leave empty (no override)

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		accessRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		accessRelation = "auditor"
	}

	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		historyObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		historyObject = meetingLevelPermission(data)
	}

	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		historyRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		historyRelation = "writer"
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

// extractSortName extracts the sort name from the meeting RSVP data
func (e *MeetingRSVPEnricher) extractSortName(data map[string]any) string {
	if value, ok := data["email"]; ok {
		if strValue, isString := value.(string); isString && strValue != "" {
			return strings.TrimSpace(strValue)
		}
	}
	return ""
}

// extractNameAndAliases extracts the name and aliases from the meeting RSVP data
func (e *MeetingRSVPEnricher) extractNameAndAliases(data map[string]any) []string {
	var nameAndAliases []string
	seen := make(map[string]bool) // Deduplicate names

	// Compile regex pattern for name-like fields
	aliasRegex := regexp.MustCompile(`(?i)^(username|email)$`)

	// Collect all name-like fields using regex pattern
	for key, value := range data {
		if aliasRegex.MatchString(key) {
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

// NewMeetingRSVPEnricher creates a new meeting RSVP enricher
func NewMeetingRSVPEnricher() Enricher {
	enricher := &MeetingRSVPEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeMeetingRSVP,
		WithAccessControl(enricher.setAccessControl),
		WithNameAndAliases(enricher.extractNameAndAliases),
		WithSortName(enricher.extractSortName),
	)
	return enricher
}
