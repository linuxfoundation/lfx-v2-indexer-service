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

// PastMeetingParticipantEnricher handles past meeting participant-specific enrichment logic
type PastMeetingParticipantEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *PastMeetingParticipantEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches past meeting participant-specific data
func (e *PastMeetingParticipantEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides past meeting participant-specific access control logic
func (e *PastMeetingParticipantEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	// Set access control with past meeting participant-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		body.AccessCheckObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist in data - use computed default with objectType prefix
		body.AccessCheckObject = fmt.Sprintf("%s:%s", constants.ObjectTypePastMeeting, objectID)
	}
	// If field exists but is not a string, leave empty (no override)

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		body.AccessCheckRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		// TODO: should this be auditor from the project? How would we determine that from the past meeting fga object?
		body.AccessCheckRelation = "organizer"
	}

	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		body.HistoryCheckObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		body.HistoryCheckObject = fmt.Sprintf("%s:%s", constants.ObjectTypePastMeeting, objectID)
	}

	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		body.HistoryCheckRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		body.HistoryCheckRelation = "writer"
	}
}

// extractSortName extracts the sort name from the past meeting participant message data
func (e *PastMeetingParticipantEnricher) extractSortName(data map[string]any) string {
	if value, ok := data["email"]; ok {
		if strValue, isString := value.(string); isString && strValue != "" {
			return strings.TrimSpace(strValue)
		}
	}
	return ""
}

// extractNameAndAliases extracts the name and aliases from the past meeting participant message data
func (e *PastMeetingParticipantEnricher) extractNameAndAliases(data map[string]any) []string {
	var nameAndAliases []string
	seen := make(map[string]bool) // Deduplicate names

	// Compile regex pattern for name-like fields
	aliasRegex := regexp.MustCompile(`(?i)^(first_name|last_name|username)$`)

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

// NewPastMeetingParticipantEnricher creates a new past meeting participant enricher
func NewPastMeetingParticipantEnricher() Enricher {
	enricher := &PastMeetingParticipantEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypePastMeetingParticipant,
		WithAccessControl(enricher.setAccessControl),
		WithNameAndAliases(enricher.extractNameAndAliases),
		WithSortName(enricher.extractSortName),
	)
	return enricher
}
