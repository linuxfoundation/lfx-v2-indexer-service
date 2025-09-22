// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// CommitteeSettingsEnricher handles committee-settings-specific enrichment logic
type CommitteeSettingsEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *CommitteeSettingsEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches committee-specific data
func (e *CommitteeSettingsEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides committee-settings-specific access control logic
func (e *CommitteeSettingsEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	// Build access control values
	var accessObject, accessRelation string
	var historyObject, historyRelation string

	// Set access control with committee-settings-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		accessObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist in data - use computed default with objectType prefix
		accessObject = fmt.Sprintf("%s:%s", objectType, objectID)
	}
	// If field exists but is not a string, leave empty (no override)

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		accessRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		accessRelation = "auditor" // Committee-settings-specific default
	}

	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		historyObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		historyObject = fmt.Sprintf("%s:%s", objectType, objectID)
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
		body.AccessCheckQuery = fmt.Sprintf("%s#%s", accessObject, accessRelation)
	}
	if historyObject != "" && historyRelation != "" {
		body.HistoryCheckQuery = fmt.Sprintf("%s#%s", historyObject, historyRelation)
	}
}

// NewCommitteeSettingsEnricher creates a new committee settings enricher
func NewCommitteeSettingsEnricher() Enricher {
	enricher := &CommitteeSettingsEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeCommitteeSettings,
		WithAccessControl(enricher.setAccessControl),
	)
	return enricher
}
