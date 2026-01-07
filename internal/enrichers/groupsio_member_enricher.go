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

// GroupsIOMemberEnricher handles GroupsIO member specific enrichment logic
type GroupsIOMemberEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *GroupsIOMemberEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches GroupsIO member specific data
func (e *GroupsIOMemberEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// setAccessControl provides groupsio-member-specific access control logic
// overrides the default access control logic
// the access control logic is based on the member's mailing list UID
// not the member's UID
func (e *GroupsIOMemberEnricher) setAccessControl(body *contracts.TransactionBody, data map[string]any, objectType, objectID string) {

	mailingListLevelPermission := func(data map[string]any) string {
		if value, ok := data["mailing_list_uid"]; ok {
			if mailingListUID, ok := value.(string); ok {
				return fmt.Sprintf("%s:%s", constants.ObjectTypeGroupsIOMailingList, mailingListUID)
			}
		}
		return fmt.Sprintf("%s:%s", objectType, objectID)
	}

	// Build access control values
	var accessObject, accessRelation string
	var historyObject, historyRelation string

	// Set access control with groupsio-member-specific logic
	// This logic represents the access via endpoint where the member is retrieved
	// when the user has auditor access to the mailing list.
	if accessCheckObject, ok := data["accessCheckObject"].(string); ok {
		// Field exists in data (even if empty) - use data value
		accessObject = accessCheckObject
	} else if _, exists := data["accessCheckObject"]; !exists {
		accessObject = mailingListLevelPermission(data)
	}

	// Access check relation - auditor for mailing list
	if accessCheckRelation, ok := data["accessCheckRelation"].(string); ok {
		accessRelation = accessCheckRelation
	} else if _, exists := data["accessCheckRelation"]; !exists {
		accessRelation = "auditor"
	}

	// History check object
	if historyCheckObject, ok := data["historyCheckObject"].(string); ok {
		historyObject = historyCheckObject
	} else if _, exists := data["historyCheckObject"]; !exists {
		historyObject = mailingListLevelPermission(data)
	}

	// History check relation - writer for mailing list
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

// settExtractNameAndAliases extracts the name and aliases from the committee member data
// overrides the default name and aliases extraction logic
func (e *GroupsIOMemberEnricher) settExtractNameAndAliases(data map[string]any) []string {
	var nameAndAliases []string
	seen := make(map[string]bool) // Deduplicate names
	// Compile regex pattern for name-like fields
	aliasRegex := regexp.MustCompile(`(?i)^(first_name|last_name|username)$`)

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
func (e *GroupsIOMemberEnricher) extractSortName(data map[string]any) string {
	if value, ok := data["first_name"]; ok {
		if strValue, isString := value.(string); isString && strValue != "" {
			return strings.TrimSpace(strValue)
		}
	}
	return ""
}

// NewGroupsIOMemberEnricher creates a new GroupsIO member enricher
func NewGroupsIOMemberEnricher() Enricher {
	gme := &GroupsIOMemberEnricher{}
	gme.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeGroupsIOMember,
		WithAccessControl(gme.setAccessControl),
		WithNameAndAliases(gme.settExtractNameAndAliases),
		WithSortName(gme.extractSortName),
	)
	return gme
}
