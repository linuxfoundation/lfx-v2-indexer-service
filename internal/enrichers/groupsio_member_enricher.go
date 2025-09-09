// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
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

// NewGroupsIOMemberEnricher creates a new GroupsIO member enricher
func NewGroupsIOMemberEnricher() Enricher {
	return &GroupsIOMemberEnricher{
		defaultEnricher: newDefaultEnricher(constants.ObjectTypeGroupsIOMember),
	}
}
