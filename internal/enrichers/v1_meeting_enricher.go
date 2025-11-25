// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// V1MeetingEnricher handles V1 meeting-specific enrichment logic
type V1MeetingEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *V1MeetingEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches V1 meeting-specific data
func (e *V1MeetingEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// extractPublicFlag extracts the public flag from the V1 meeting message data
func (e *V1MeetingEnricher) extractPublicFlag(data map[string]any) bool {
	visibility, ok := data["visibility"].(string)
	if !ok {
		return false
	}
	return visibility == "public"
}

// setAccessControl provides V1 meeting-specific access control logic using meeting_id
func (e *V1MeetingEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	meetingLevelPermission := func(data map[string]any) string {
		if value, ok := data["id"]; ok {
			if meetingID, ok := value.(string); ok {
				return fmt.Sprintf("%s:%s", objectType, meetingID)
			}
		}
		return fmt.Sprintf("%s:%s", objectType, objectID)
	}

	// Build access control values
	var accessObject, accessRelation string
	var historyObject, historyRelation string

	// Set access control with V1 meeting-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		accessObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		accessObject = meetingLevelPermission(data)
	}

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		accessRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		accessRelation = "viewer"
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

// NewV1MeetingEnricher creates a new V1 meeting enricher
func NewV1MeetingEnricher() Enricher {
	enricher := &V1MeetingEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeV1Meeting,
		WithPublicFlag(enricher.extractPublicFlag),
		WithAccessControl(enricher.setAccessControl),
	)
	return enricher
}
