// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// PastMeetingSummaryEnricher handles past meeting summary-specific enrichment logic
type PastMeetingSummaryEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *PastMeetingSummaryEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches past meeting summary-specific data
func (e *PastMeetingSummaryEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides past meeting summary-specific access control logic
func (e *PastMeetingSummaryEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
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

	// Set access control with past meeting summary-specific logic
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

// NewPastMeetingSummaryEnricher creates a new past meeting summary enricher
func NewPastMeetingSummaryEnricher() Enricher {
	enricher := &PastMeetingSummaryEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypePastMeetingSummary,
		WithAccessControl(enricher.setAccessControl),
	)
	return enricher
}
