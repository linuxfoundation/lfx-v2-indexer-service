// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// GroupsIOServiceEnricher handles groupsio-service-specific enrichment logic
type GroupsIOServiceEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *GroupsIOServiceEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches groupsio-service-specific data
func (e *GroupsIOServiceEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides groupsio-service-specific access control logic
func (e *GroupsIOServiceEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	// Set access control with groupsio-service-specific logic
	// Only apply defaults when fields are completely missing from data
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		body.AccessCheckObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist in data - use computed default with objectType prefix
		body.AccessCheckObject = fmt.Sprintf("%s:%s", objectType, objectID)
	}
	// If field exists but is not a string, leave empty (no override)

	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		body.AccessCheckRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		body.AccessCheckRelation = "auditor" // Groupsio-service-specific default
	}

	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		body.HistoryCheckObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		body.HistoryCheckObject = fmt.Sprintf("%s:%s", objectType, objectID)
	}

	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		body.HistoryCheckRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		body.HistoryCheckRelation = "writer"
	}
}

// NewGroupsIOServiceEnricher creates a new groupsio-service enricher
func NewGroupsIOServiceEnricher() Enricher {
	enricher := &GroupsIOServiceEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeGroupsIOService,
		WithAccessControl(enricher.setAccessControl),
	)
	return enricher
}
