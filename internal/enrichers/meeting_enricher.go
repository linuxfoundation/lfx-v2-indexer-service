// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"
	"slices"
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// MeetingEnricher handles meeting-specific enrichment logic
type MeetingEnricher struct{}

// NewMeetingEnricher creates a new meeting enricher
func NewMeetingEnricher() *MeetingEnricher {
	return &MeetingEnricher{}
}

// ObjectType returns the object type this enricher handles
func (e *MeetingEnricher) ObjectType() string {
	return constants.ObjectTypeMeeting
}

// EnrichData enriches meeting-specific data
func (e *MeetingEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	data := transaction.ParsedData

	// Set the processed data on the body (enricher owns data assignment)
	body.Data = data

	// Extract meeting ID using 'uid' field (matches reference implementation)
	// Also support 'id' for backwards compatibility
	var objectID string
	if uid, ok := data["uid"].(string); ok {
		objectID = uid
	} else if id, ok := data["id"].(string); ok {
		objectID = id
	} else {
		return fmt.Errorf("%s: missing or invalid meeting ID", constants.ErrMappingUID)
	}
	body.ObjectID = objectID

	// Set public flag - required for access control
	// Note: meetings have a visibility field, which can be public or private,
	// so we need to check that field to set the public flag.
	visibility, ok := data["visibility"].(string)
	if !ok {
		return fmt.Errorf("%s: missing or invalid visibility field", constants.ErrMappingPublic)
	}
	body.Public = visibility == "public"

	// Set access control with reference implementation logic (computed defaults)
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		body.AccessCheckObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist in data - use computed default
		body.AccessCheckObject = fmt.Sprintf("meeting:%s", objectID)
	}
	// If field exists but is not a string, leave empty (no override)

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		body.AccessCheckRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		body.AccessCheckRelation = "viewer"
	}

	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		body.HistoryCheckObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		body.HistoryCheckObject = fmt.Sprintf("meeting:%s", objectID)
	}

	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		body.HistoryCheckRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		body.HistoryCheckRelation = "writer"
	}

	// Extract meeting name for sorting
	if title, ok := data["title"].(string); ok {
		body.SortName = title
		body.NameAndAliases = []string{title}
	}

	// Extract description for fulltext search
	if description, ok := data["description"].(string); ok && description != "" {
		body.Fulltext = description
	}

	// Build comprehensive fulltext search content
	var fulltext []string
	if body.SortName != "" {
		fulltext = append(fulltext, body.SortName)
	}
	for _, alias := range body.NameAndAliases {
		if alias != body.SortName {
			fulltext = append(fulltext, alias)
		}
	}
	// Include description if not already in fulltext
	if body.Fulltext != "" && !slices.Contains(fulltext, body.Fulltext) {
		fulltext = append(fulltext, body.Fulltext)
	}

	// Set final fulltext content
	if len(fulltext) > 0 {
		body.Fulltext = strings.Join(fulltext, " ")
	}

	return nil
}
