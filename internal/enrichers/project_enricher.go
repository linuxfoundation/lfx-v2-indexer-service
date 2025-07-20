// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package enrichers

import (
	"fmt"
	"strings"

	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/entities"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
)

// ProjectEnricher handles project-specific enrichment logic
type ProjectEnricher struct{}

// NewProjectEnricher creates a new project enricher
func NewProjectEnricher() *ProjectEnricher {
	return &ProjectEnricher{}
}

// ObjectType returns the object type this enricher handles
func (e *ProjectEnricher) ObjectType() string {
	return constants.ObjectTypeProject
}

// EnrichData enriches project-specific data
func (e *ProjectEnricher) EnrichData(body *entities.TransactionBody, transaction *entities.LFXTransaction) error {
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

	// Set public flag - required for access control
	if public, ok := data["public"].(bool); !ok {
		return fmt.Errorf("%s: missing or invalid public flag", constants.ErrMappingPublic)
	} else {
		body.Public = public
	}

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

	// Handle parent project reference (enhanced requirement)
	if parentID, ok := data["parentID"].(string); ok && parentID != "" {
		body.ParentRefs = []string{parentID}
	}

	// Also handle legacy parent_uid field for backwards compatibility
	if parentUID, ok := data["parent_uid"].(string); ok && parentUID != "" {
		// If we already have ParentRefs from parentID, append to it
		if body.ParentRefs == nil {
			body.ParentRefs = []string{"project:" + parentUID}
		} else {
			body.ParentRefs = append(body.ParentRefs, "project:"+parentUID)
		}
	}

	// Extract project name for sorting
	if name, ok := data["name"].(string); ok {
		body.SortName = name
		body.NameAndAliases = []string{name}
	}

	// Extract slug as additional alias
	if slug, ok := data["slug"].(string); ok && slug != "" {
		body.NameAndAliases = append(body.NameAndAliases, slug)
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
	if body.Fulltext != "" && !contains(fulltext, body.Fulltext) {
		fulltext = append(fulltext, body.Fulltext)
	}

	// Set final fulltext content
	if len(fulltext) > 0 {
		body.Fulltext = strings.Join(fulltext, " ")
	}

	return nil
}

// Helper function to check if slice contains string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
