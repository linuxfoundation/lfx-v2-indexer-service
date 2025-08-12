// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

var (
	// aliasRegex matches keys that typically contain names or aliases
	aliasRegex     *regexp.Regexp
	aliasRegexOnce sync.Once
)

// defaultEnricher handles default enrichment logic
// This enricher is used when no specific enricher is registered for an object type.
// It provides a fallback enrichment strategy that works for most object types
// by extracting common fields and applying sensible defaults.
type defaultEnricher struct {
	objectType string
	logger     *slog.Logger
}

// ObjectType returns the object type this enricher handles.
// The default enricher returns the default enricher type since it can handle any object type.
func (e *defaultEnricher) ObjectType() string {
	return e.objectType
}

// EnrichData enriches data with default logic applicable to most object types.
// It performs the following operations:
//   - Validates and extracts object ID from 'uid' or 'id' fields
//   - Sets public flag with safe type assertion
//   - Extracts and processes names and aliases
//   - Configures access control with computed defaults
//   - Handles parent references with backward compatibility
//   - Builds comprehensive fulltext search content
//
// Returns an error if required fields are missing or invalid.
func (e *defaultEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	if body == nil {
		return fmt.Errorf("transaction body cannot be nil")
	}
	if transaction == nil {
		return fmt.Errorf("transaction cannot be nil")
	}
	if transaction.ParsedData == nil {
		return fmt.Errorf("transaction parsed data cannot be nil")
	}

	data := transaction.ParsedData
	objectType := e.getObjectType(transaction)

	e.logger.Debug("Starting default enrichment",
		slog.String("object_type", objectType),
		slog.Int("data_fields", len(data)))

	// Set the processed data on the body (enricher owns data assignment)
	body.Data = data
	body.ObjectType = objectType

	// Extract and validate object ID
	objectID, err := e.extractObjectID(data)
	if err != nil {
		e.logger.Error("Failed to extract object ID", slog.String("error", err.Error()))
		return err
	}
	body.ObjectID = objectID

	// Set public flag with safe type assertion and default
	body.Public = e.extractPublicFlag(data)

	// Extract names and aliases
	body.SortName = e.extractSortName(data)
	body.NameAndAliases = e.extractNameAndAliases(data)

	// Set access control with computed defaults
	e.setAccessControl(body, data, objectType, objectID)

	// Handle parent references
	e.setParentReferences(body, data, objectType)

	// Build comprehensive fulltext search content
	e.buildFulltextContent(body, data)

	e.logger.Debug("Completed default enrichment",
		slog.String("object_id", objectID),
		slog.Bool("public", body.Public),
		slog.String("sort_name", body.SortName),
		slog.Int("aliases_count", len(body.NameAndAliases)))

	return nil
}

// extractObjectID safely extracts the object ID from data with proper validation
func (e *defaultEnricher) extractObjectID(data map[string]any) (string, error) {
	// Try 'uid' field first (preferred)
	if uid, ok := data["uid"]; ok {
		if uidStr, isString := uid.(string); isString && uidStr != "" {
			return uidStr, nil
		}
		// uid exists but is not a valid string
		return "", fmt.Errorf("%s: 'uid' field exists but is not a valid non-empty string", constants.ErrMappingUID)
	}

	// Fall back to 'id' field for backwards compatibility
	if id, ok := data["id"]; ok {
		if idStr, isString := id.(string); isString && idStr != "" {
			return idStr, nil
		}
		// id exists but is not a valid string
		return "", fmt.Errorf("%s: 'id' field exists but is not a valid non-empty string", constants.ErrMappingUID)
	}

	return "", fmt.Errorf("%v: missing required 'uid' or 'id' field", constants.ErrMappingUID)
}

// extractPublicFlag safely extracts the public flag with proper defaults
func (e *defaultEnricher) extractPublicFlag(data map[string]any) bool {
	if publicVal, ok := data["public"]; ok {
		if publicBool, isBool := publicVal.(bool); isBool {
			return publicBool
		}
		// Log warning for invalid type but continue with default
		e.logger.Warn("Public field exists but is not a boolean, defaulting to false",
			slog.Any("public_value", publicVal))
	}
	// Default to false if not present or invalid
	return false
}

// extractSortName extracts the primary name for sorting purposes
func (e *defaultEnricher) extractSortName(data map[string]any) string {
	// Try common name fields in order of preference
	nameFields := []string{"name", "title", "display_name", "label"}

	for _, field := range nameFields {
		if value, ok := data[field]; ok {
			if strValue, isString := value.(string); isString && strValue != "" {
				return strings.TrimSpace(strValue)
			}
		}
	}

	return ""
}

// getAliasRegex returns the compiled alias regex, initializing it once.
func (e *defaultEnricher) getAliasRegex() *regexp.Regexp {
	aliasRegexOnce.Do(func() {
		aliasRegex = regexp.MustCompile(`(?i)^(name|title|label|alias|slug|display_name)$`)
	})
	return aliasRegex
}

// extractNameAndAliases collects all name-like fields for comprehensive searching
func (e *defaultEnricher) extractNameAndAliases(data map[string]any) []string {
	var nameAndAliases []string
	seen := make(map[string]bool) // Deduplicate names

	// Collect all name-like fields using regex pattern
	for key, value := range data {
		if e.getAliasRegex().MatchString(key) {
			if strValue, ok := value.(string); ok && strValue != "" {
				trimmed := strings.TrimSpace(strValue)
				if trimmed != "" && !seen[trimmed] {
					nameAndAliases = append(nameAndAliases, trimmed)
					seen[trimmed] = true
				}
			}
		}
	}

	return nameAndAliases
}

// getObjectType determines the object type from transaction or returns empty string
func (e *defaultEnricher) getObjectType(transaction *contracts.LFXTransaction) string {
	return transaction.ObjectType
}

// setAccessControl configures access control fields with computed defaults
func (e *defaultEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
	// Access check object
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		body.AccessCheckObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		// Field doesn't exist - use computed default
		body.AccessCheckObject = fmt.Sprintf("%s:%s", objectType, objectID)
	}
	// If field exists but is not a string, leave empty (no override)

	// Access check relation
	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		body.AccessCheckRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		body.AccessCheckRelation = "viewer"
	}

	// History check object
	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		body.HistoryCheckObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		body.HistoryCheckObject = fmt.Sprintf("%s:%s", objectType, objectID)
	}

	// History check relation
	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		body.HistoryCheckRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		body.HistoryCheckRelation = "writer"
	}
}

// setParentReferences handles parent references with backward compatibility
func (e *defaultEnricher) setParentReferences(body *contracts.TransactionBody, data map[string]any, objectType string) {
	var parentRefs []string

	// Handle parent_uid field (preferred)
	if parentUID, ok := data["parent_uid"].(string); ok && parentUID != "" {
		parentRefs = append(parentRefs, fmt.Sprintf("%s:%s", objectType, parentUID))
	}

	// Handle legacy parentID field for backwards compatibility
	if parentID, ok := data["parentID"].(string); ok && parentID != "" {
		ref := fmt.Sprintf("%s:%s", objectType, parentID)
		// Avoid duplicates
		if !slices.Contains(parentRefs, ref) {
			parentRefs = append(parentRefs, ref)
		}
	}

	if len(parentRefs) > 0 {
		body.ParentRefs = parentRefs
	}
}

// buildFulltextContent creates comprehensive fulltext search content
func (e *defaultEnricher) buildFulltextContent(body *contracts.TransactionBody, data map[string]any) {
	var fulltext []string
	seen := make(map[string]bool)

	// Add sort name if present
	if body.SortName != "" {
		fulltext = append(fulltext, body.SortName)
		seen[body.SortName] = true
	}

	// Add all unique aliases
	for _, alias := range body.NameAndAliases {
		if !seen[alias] {
			fulltext = append(fulltext, alias)
			seen[alias] = true
		}
	}

	// Add description if present
	if description, ok := data["description"].(string); ok && description != "" {
		trimmed := strings.TrimSpace(description)
		if trimmed != "" && !seen[trimmed] {
			fulltext = append(fulltext, trimmed)
		}
	}

	// Set final fulltext content
	if len(fulltext) > 0 {
		body.Fulltext = strings.Join(fulltext, " ")
	}
}

// newDefaultEnricher creates a new default enricher with structured logging
func newDefaultEnricher(component string) Enricher {
	return &defaultEnricher{
		objectType: component,
		logger:     logging.NewLogger().With(slog.String("component", component)),
	}
}
