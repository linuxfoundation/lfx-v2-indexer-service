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

// CommitteeMemberEnricher handles committee-specific enrichment logic
type CommitteeMemberEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *CommitteeMemberEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches committee-specific data
func (e *CommitteeMemberEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides meeting-registrant-specific access control logic
func (e *CommitteeMemberEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {

	committeeLevelPermission := func(data map[string]any) string {
		if value, ok := data["committee_uid"]; ok {
			if committeeUID, ok := value.(string); ok {
				return fmt.Sprintf("%s:%s", constants.ObjectTypeCommittee, committeeUID)
			}
		}
		return fmt.Sprintf("%s:%s", objectType, objectID)
	}

	// Set access control with committee-member-specific logic
	// This logic represents the access via endpoint where the committee member is retrieved
	// when the user has access to the committee.
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		body.AccessCheckObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		body.AccessCheckObject = committeeLevelPermission(data)
	}

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
		body.HistoryCheckObject = committeeLevelPermission(data)
	}

	// History check relation
	if historyCheckRelation, ok := data["historyCheckRelation"].(string); ok {
		body.HistoryCheckRelation = historyCheckRelation
	} else if _, exists := data["historyCheckRelation"]; !exists {
		body.HistoryCheckRelation = "writer"
	}
}

func (e *CommitteeMemberEnricher) settExtractNameAndAliases(data map[string]any) []string {
	var nameAndAliases []string
	seen := make(map[string]bool) // Deduplicate names
	// Compile regex pattern for name-like fields
	aliasRegex := regexp.MustCompile(`(?i)^(committee_name|first_name|last_name|username)$`)

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

// extractSortName extracts the sort name from the meeting message data
func (e *CommitteeMemberEnricher) extractSortName(data map[string]any) string {
	if value, ok := data["first_name"]; ok {
		if strValue, isString := value.(string); isString && strValue != "" {
			return strings.TrimSpace(strValue)
		}
	}
	return ""
}

// NewCommitteeEnricher creates a new committee enricher
func NewCommitteeMemberEnricher() Enricher {
	cme := &CommitteeMemberEnricher{}
	cme.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeCommitteeMember,
		WithAccessControl(cme.setAccessControl),
		WithNameAndAliases(cme.settExtractNameAndAliases),
		WithSortName(cme.extractSortName),
	)
	return cme

}
