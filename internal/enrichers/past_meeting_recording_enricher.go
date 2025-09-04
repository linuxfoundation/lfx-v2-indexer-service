// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// PastMeetingRecordingEnricher handles past meeting recording-specific enrichment logic
type PastMeetingRecordingEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *PastMeetingRecordingEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches meeting-specific data
func (e *PastMeetingRecordingEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides meeting-settings-specific access control logic
func (e *PastMeetingRecordingEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	// Set access control with past meeting recording-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		body.AccessCheckObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		body.AccessCheckObject = fmt.Sprintf("%s:%s", constants.ObjectTypePastMeeting, objectID)
	}

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		body.AccessCheckRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		body.AccessCheckRelation = "viewer"
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

// NewPastMeetingRecordingEnricher creates a new past meeting recording enricher
func NewPastMeetingRecordingEnricher() Enricher {
	enricher := &PastMeetingRecordingEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypePastMeetingRecording,
		WithAccessControl(enricher.setAccessControl),
	)
	return enricher
}
