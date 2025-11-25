// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// V1PastMeetingEnricher handles V1 past meeting-specific enrichment logic
type V1PastMeetingEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *V1PastMeetingEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches V1 past meeting-specific data
func (e *V1PastMeetingEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// extractPublicFlag extracts the public flag from the V1 past meeting message data
func (e *V1PastMeetingEnricher) extractPublicFlag(data map[string]any) bool {
	visibility, ok := data["visibility"].(string)
	if !ok {
		return false
	}
	return visibility == "public"
}

// setAccessControl provides V1 past meeting-specific access control logic using meeting_and_occurrence_id
func (e *V1PastMeetingEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	pastMeetingLevelPermission := func(data map[string]any) string {
		if value, ok := data["id"]; ok {
			if meetingAndOccurrenceID, ok := value.(string); ok {
				return fmt.Sprintf("%s:%s", objectType, meetingAndOccurrenceID)
			}
		}
		return fmt.Sprintf("%s:%s", objectType, objectID)
	}

	// Build access control values
	var accessObject, accessRelation string
	var historyObject, historyRelation string

	// Set access control with V1 past meeting-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		accessObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		accessObject = pastMeetingLevelPermission(data)
	}

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

// NewV1PastMeetingEnricher creates a new V1 past meeting enricher
func NewV1PastMeetingEnricher() Enricher {
	enricher := &V1PastMeetingEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeV1PastMeeting,
		WithPublicFlag(enricher.extractPublicFlag),
		WithAccessControl(enricher.setAccessControl),
	)
	return enricher
}
