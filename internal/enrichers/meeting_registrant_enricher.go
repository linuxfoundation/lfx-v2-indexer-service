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

// MeetingRegistrantEnricher handles meeting-registrant-specific enrichment logic
type MeetingRegistrantEnricher struct{}

// NewMeetingRegistrantEnricher creates a new meeting-registrant enricher
func NewMeetingRegistrantEnricher() *MeetingRegistrantEnricher {
	return &MeetingRegistrantEnricher{}
}

// ObjectType returns the object type this enricher handles
func (e *MeetingRegistrantEnricher) ObjectType() string {
	return constants.ObjectTypeMeetingRegistrant
}

// EnrichData enriches meeting-registrant-specific data
func (e *MeetingRegistrantEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	data := transaction.ParsedData

	// Set the processed data on the body (enricher owns data assignment)
	body.Data = data

	// Extract meeting-registrant ID using 'uid' field (matches reference implementation)
	// Also support 'id' for backwards compatibility
	var objectID string
	if uid, ok := data["uid"].(string); ok {
		objectID = uid
	} else if id, ok := data["id"].(string); ok {
		objectID = id
	} else {
		return fmt.Errorf("%s: missing or invalid meeting-registrant ID", constants.ErrMappingUID)
	}
	body.ObjectID = objectID

	// Registrant data is not public
	body.Public = false

	var meetingUID string
	if meetingUIDStr, ok := data["meeting_uid"].(string); ok {
		meetingUID = meetingUIDStr
	} else {
		return fmt.Errorf("%s: missing or invalid meeting UID", constants.ErrMappingUID)
	}

	// Set access control with reference implementation logic (computed defaults)
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		body.AccessCheckObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist in data - use computed default
		body.AccessCheckObject = fmt.Sprintf("meeting:%s", meetingUID)
	}
	// If field exists but is not a string, leave empty (no override)

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		body.AccessCheckRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		body.AccessCheckRelation = "organizer"
	}

	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		body.HistoryCheckObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		body.HistoryCheckObject = fmt.Sprintf("meeting:%s", meetingUID)
	}

	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		body.HistoryCheckRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		body.HistoryCheckRelation = "writer"
	}

	// Extract email for sorting
	if email, ok := data["email"].(string); ok {
		body.SortName = email
		body.NameAndAliases = []string{email}
	}

	var fulltext []string

	// Extract first name and last name for fulltext search
	if firstName, ok := data["first_name"].(string); ok && firstName != "" {
		fulltext = append(fulltext, firstName)
	}

	if lastName, ok := data["last_name"].(string); ok && lastName != "" {
		fulltext = append(fulltext, lastName)
	}

	// Build comprehensive fulltext search content
	if body.SortName != "" {
		fulltext = append(fulltext, body.SortName)
	}
	for _, alias := range body.NameAndAliases {
		if alias != body.SortName {
			fulltext = append(fulltext, alias)
		}
	}

	// Set final fulltext content
	if len(fulltext) > 0 {
		body.Fulltext = strings.Join(fulltext, " ")
	}

	return nil
}
