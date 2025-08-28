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

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
)

var (
	// parentRefRegex matches field names that end with '_uid' or 'ID'.
	// It captures any prefix before these suffixes. This is used to identify parent reference fields.
	// It intentionally excludes fields that do not end with these suffixes,
	// such as those ending with other patterns.
	parentRefRegex = regexp.MustCompile(`^(.*)(_uid$|ID$)`)
)

// EnricherOption defines a function that can configure a defaultEnricher
type EnricherOption func(*defaultEnricher)

// Option functions for configuring defaultEnricher

// WithAccessControl allows overriding the access control logic
func WithAccessControl(fn func(body *contracts.TransactionBody, data map[string]any, objectType, objectID string)) EnricherOption {
	return func(e *defaultEnricher) {
		e.setAccessControlFn = fn
	}
}

// WithParentReferences allows overriding the parent references logic
func WithParentReferences(fn func(body *contracts.TransactionBody, data map[string]any, objectType string)) EnricherOption {
	return func(e *defaultEnricher) {
		e.setParentReferencesFn = fn
	}
}

// WithPublicFlag allows overriding the public flag extraction logic
func WithPublicFlag(fn func(data map[string]any) bool) EnricherOption {
	return func(e *defaultEnricher) {
		e.extractPublicFlagFn = fn
	}
}

// WithNameAndAliases allows overriding the name and aliases extraction logic
func WithNameAndAliases(fn func(data map[string]any) []string) EnricherOption {
	return func(e *defaultEnricher) {
		e.extractNameAndAliasesFn = fn
	}
}

// WithSortName allows overriding the sort name extraction logic
func WithSortName(fn func(data map[string]any) string) EnricherOption {
	return func(e *defaultEnricher) {
		e.extractSortNameFn = fn
	}
}

// defaultEnricher handles default enrichment logic
// This enricher is used when no specific enricher is registered for an object type.
// It provides a fallback enrichment strategy that works for most object types
// by extracting common fields and applying sensible defaults.
type defaultEnricher struct {
	objectType string
	logger     *slog.Logger

	// Configurable functions - can be overridden via options
	setAccessControlFn      func(body *contracts.TransactionBody, data map[string]any, objectType, objectID string)
	setParentReferencesFn   func(body *contracts.TransactionBody, data map[string]any, objectType string)
	extractPublicFlagFn     func(data map[string]any) bool
	extractNameAndAliasesFn func(data map[string]any) []string
	extractSortNameFn       func(data map[string]any) string
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
	body.Public = e.extractPublicFlagFn(data)

	// Extract names and aliases
	body.SortName = e.extractSortNameFn(data)
	body.NameAndAliases = e.extractNameAndAliasesFn(data)

	// Set access control with computed defaults
	e.setAccessControlFn(body, data, objectType, objectID)

	// Handle parent references
	e.setParentReferencesFn(body, data, objectType)

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

// extractSortName extracts the primary name for sorting purposes
func defaultExtractSortName(data map[string]any) string {
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

// getObjectType determines the object type from transaction or returns empty string
func (e *defaultEnricher) getObjectType(transaction *contracts.LFXTransaction) string {
	return transaction.ObjectType
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

// Default implementation functions

// defaultSetAccessControl provides the default access control logic
func defaultSetAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {
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

// defaultSetParentReferences provides the default parent references logic
// it matches the pattern <anything>_uid or <anything>ID (case sensitive)
// except parent_uid and parentID
// and adds the parent references to the body.ParentRefs field
func defaultSetParentReferences(body *contracts.TransactionBody, data map[string]any, objectType string) {
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

	parentRefFunc := func(pattern *regexp.Regexp) {
		for key, value := range data {
			if pattern.MatchString(key) && key != "parent_uid" && key != "parentID" {
				// remove the suffix from the key
				match := pattern.FindStringSubmatch(key)
				if len(match) > 1 {
					keyType := strings.ToLower(match[1])
					ref := fmt.Sprintf("%s:%v", keyType, value)
					if !slices.Contains(parentRefs, ref) {
						parentRefs = append(parentRefs, ref)
					}
				}
			}
		}
	}

	// handle pattern <anything>_uid or <anything>ID (case sensitive)
	// except parent_uid
	parentRefFunc(parentRefRegex)

	if len(parentRefs) > 0 {
		body.ParentRefs = parentRefs
	}
}

// defaultExtractPublicFlag provides the default public flag extraction logic
func defaultExtractPublicFlag(data map[string]any) bool {
	if publicVal, ok := data["public"]; ok {
		if publicBool, isBool := publicVal.(bool); isBool {
			return publicBool
		}
		// Note: We can't log here since we don't have access to logger
		// The calling enricher can handle logging if needed
	}
	// Default to false if not present or invalid
	return false
}

// defaultExtractNameAndAliases provides the default name and aliases extraction logic
func defaultExtractNameAndAliases(data map[string]any) []string {
	var nameAndAliases []string
	seen := make(map[string]bool) // Deduplicate names

	// Compile regex pattern for name-like fields
	aliasRegex := regexp.MustCompile(`(?i)^(name|title|label|alias|slug|display_name)$`)

	// Collect all name-like fields using regex pattern
	for key, value := range data {
		if aliasRegex.MatchString(key) {
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

// newDefaultEnricher creates a new default enricher with structured logging and optional customizations
func newDefaultEnricher(component string, options ...EnricherOption) Enricher {
	e := &defaultEnricher{
		objectType: component,
		logger:     logging.NewLogger().With(slog.String("component", component)),

		// Set default implementations
		setAccessControlFn:      defaultSetAccessControl,
		setParentReferencesFn:   defaultSetParentReferences,
		extractPublicFlagFn:     defaultExtractPublicFlag,
		extractNameAndAliasesFn: defaultExtractNameAndAliases,
		extractSortNameFn:       defaultExtractSortName,
	}

	// Apply options to override defaults
	for _, option := range options {
		option(e)
	}

	return e
}
