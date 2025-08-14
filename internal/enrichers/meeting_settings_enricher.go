// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// MeetingSettingsEnricher handles meeting-settings-specific enrichment logic
type MeetingSettingsEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *MeetingSettingsEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches meeting-specific data
func (e *MeetingSettingsEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides meeting-settings-specific access control logic
func (e *MeetingSettingsEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	// Set access control with meeting-settings-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		body.AccessCheckObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist in data - use computed default with objectType prefix
		body.AccessCheckObject = fmt.Sprintf("%s:%s", constants.ObjectTypeMeeting, objectID)
	}
	// If field exists but is not a string, leave empty (no override)

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		body.AccessCheckRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		// TODO: should this be auditor from the project? How would we determine that from the meeting fga object?
		body.AccessCheckRelation = "organizer"
	}

	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		body.HistoryCheckObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		body.HistoryCheckObject = fmt.Sprintf("%s:%s", constants.ObjectTypeMeeting, objectID)
	}

	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		body.HistoryCheckRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		body.HistoryCheckRelation = "writer"
	}
}

// NewMeetingSettingsEnricher creates a new meeting settings enricher
func NewMeetingSettingsEnricher() Enricher {
	enricher := &MeetingSettingsEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeMeetingSettings,
		WithAccessControl(enricher.setAccessControl),
	)
	return enricher
}
