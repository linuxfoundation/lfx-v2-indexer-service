// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// CommitteeMemberSensitiveEnricher enriches committee member sensitive data
type CommitteeMemberSensitiveEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *CommitteeMemberSensitiveEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches committee-specific data
func (e *CommitteeMemberSensitiveEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides committee-member-sensitive-specific access control logic
// overrides the default access control logic
// Since it's a sensitive object, the access control logic is set to auditor/writer on the committee level
func (e *CommitteeMemberSensitiveEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {

	committeeLevelPermission := func(data map[string]any) string {
		if value, ok := data["committee_uid"]; ok {
			if committeeUID, ok := value.(string); ok {
				return fmt.Sprintf("%s:%s", constants.ObjectTypeCommittee, committeeUID)
			}
		}
		return fmt.Sprintf("%s:%s", objectType, objectID)
	}

	// Build access control values
	var accessObject, accessRelation string
	var historyObject, historyRelation string

	// Set access control with committee-member-specific logic
	// This logic represents the access via endpoint where the committee member is retrieved
	// when the user has access to the committee.
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		accessObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		accessObject = committeeLevelPermission(data)
	}

	// Access check relation
	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		accessRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		accessRelation = "auditor"
	}

	// History check object
	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		historyObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		historyObject = committeeLevelPermission(data)
	}

	// History check relation
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

// setExtractNameAndAliases extracts the name and aliases from the committee member data
// overrides the default name and aliases extraction logic
func (e *CommitteeMemberSensitiveEnricher) setExtractNameAndAliases(data map[string]any) []string {
	var nameAndAliases []string
	seen := make(map[string]bool) // Deduplicate names
	// Compile regex pattern for name-like fields
	aliasRegex := regexp.MustCompile(`(?i)^(email)$`)

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

// extractSortName extracts the sort name from the committee member data
// overrides the default sort name extraction logic
func (e *CommitteeMemberSensitiveEnricher) extractSortName(data map[string]any) string {
	if value, ok := data["email"]; ok {
		if strValue, isString := value.(string); isString && strValue != "" {
			return strings.TrimSpace(strValue)
		}
	}
	return ""
}

// NewCommitteeMemberSensitiveEnricher creates a new committee member sensitive enricher instance
func NewCommitteeMemberSensitiveEnricher() Enricher {
	cme := &CommitteeMemberSensitiveEnricher{}
	cme.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeCommitteeMemberSensitive,
		WithAccessControl(cme.setAccessControl),
		WithNameAndAliases(cme.setExtractNameAndAliases),
		WithSortName(cme.extractSortName),
	)
	return cme

}
