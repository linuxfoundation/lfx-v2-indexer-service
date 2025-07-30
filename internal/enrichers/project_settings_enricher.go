// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// ProjectSettingsEnricher handles project-settings-specific enrichment logic
type ProjectSettingsEnricher struct{}

// NewProjectSettingsEnricher creates a new project settings enricher
func NewProjectSettingsEnricher() *ProjectSettingsEnricher {
	return &ProjectSettingsEnricher{}
}

// ObjectType returns the object type this enricher handles
func (e *ProjectSettingsEnricher) ObjectType() string {
	return constants.ObjectTypeProjectSettings
}

// EnrichData enriches project-specific data
func (e *ProjectSettingsEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	data := transaction.ParsedData

	// Set the processed data on the body (enricher owns data assignment)
	body.Data = data

	// Extract project ID using 'uid' field (matches reference implementation)
	// Also support 'id' for backwards compatibility
	var objectID string
	if uid, ok := data["uid"].(string); ok {
		objectID = uid
	} else if id, ok := data["id"].(string); ok {
		objectID = id
	} else {
		return fmt.Errorf("%s: missing or invalid project ID", constants.ErrMappingUID)
	}
	body.ObjectID = objectID

	// A project's settings are not public for any project status
	body.Public = false

	// Set access control with reference implementation logic (computed defaults)
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		body.AccessCheckObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist in data - use computed default
		body.AccessCheckObject = fmt.Sprintf("project:%s", objectID)
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
		body.HistoryCheckObject = fmt.Sprintf("project:%s", objectID)
	}

	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		body.HistoryCheckRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		body.HistoryCheckRelation = "writer"
	}

	return nil
}
