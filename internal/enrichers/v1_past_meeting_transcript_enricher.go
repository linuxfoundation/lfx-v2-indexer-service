// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// V1PastMeetingTranscriptEnricher handles V1 past meeting transcript-specific enrichment logic
type V1PastMeetingTranscriptEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *V1PastMeetingTranscriptEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches V1 past meeting transcript-specific data
func (e *V1PastMeetingTranscriptEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides V1 past meeting transcript-specific access control logic
func (e *V1PastMeetingTranscriptEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	pastMeetingTranscriptLevelPermission := func(data map[string]any) string {
		if value, ok := data["past_meeting_transcript_uid"]; ok {
			if pastMeetingTranscriptUID, ok := value.(string); ok {
				return fmt.Sprintf("%s:%s", constants.ObjectTypePastMeetingTranscript, pastMeetingTranscriptUID)
			}
		}
		return fmt.Sprintf("%s:%s", objectType, objectID)
	}

	// Build access control values
	var accessObject, accessRelation string
	var historyObject, historyRelation string

	// Set access control with V1 past meeting transcript-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		accessObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		accessObject = pastMeetingTranscriptLevelPermission(data)
	}

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		accessRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		accessRelation = "viewer"
	}

	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		historyObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		historyObject = pastMeetingTranscriptLevelPermission(data)
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

// NewV1PastMeetingTranscriptEnricher creates a new V1 past meeting transcript enricher
func NewV1PastMeetingTranscriptEnricher() Enricher {
	enricher := &V1PastMeetingTranscriptEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeV1PastMeetingTranscript,
		WithAccessControl(enricher.setAccessControl),
	)
	return enricher
}
