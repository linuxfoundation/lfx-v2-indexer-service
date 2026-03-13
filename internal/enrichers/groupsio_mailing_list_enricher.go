// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// GroupsIOMailingListEnricher handles GroupsIO mailing list-specific enrichment logic
type GroupsIOMailingListEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *GroupsIOMailingListEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches GroupsIO mailing list-specific data
func (e *GroupsIOMailingListEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// extractGroupName extracts the sort name for a mailing list, preferring group_name over generic name fields.
func extractGroupName(data map[string]any) string {
	if v, ok := data["group_name"].(string); ok && v != "" {
		return strings.TrimSpace(v)
	}
	return defaultExtractSortName(data)
}

// extractGroupNameAndAliases extracts name and aliases for a mailing list, including group_name.
func extractGroupNameAndAliases(data map[string]any) []string {
	seen := make(map[string]bool)
	var names []string

	addUnique := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			names = append(names, s)
			seen[s] = true
		}
	}

	if v, ok := data["group_name"].(string); ok {
		addUnique(v)
	}

	for _, alias := range defaultExtractNameAndAliases(data) {
		addUnique(alias)
	}

	return names
}

// NewGroupsIOMailingListEnricher creates a new GroupsIO mailing list enricher
func NewGroupsIOMailingListEnricher() Enricher {
	return &GroupsIOMailingListEnricher{
		defaultEnricher: newDefaultEnricher(
			constants.ObjectTypeGroupsIOMailingList,
			WithSortName(extractGroupName),
			WithNameAndAliases(extractGroupNameAndAliases),
		),
	}
}
