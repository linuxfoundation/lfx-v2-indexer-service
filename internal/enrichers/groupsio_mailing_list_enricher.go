// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
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

// NewGroupsIOMailingListEnricher creates a new GroupsIO mailing list enricher
func NewGroupsIOMailingListEnricher() Enricher {
	return &GroupsIOMailingListEnricher{
		defaultEnricher: newDefaultEnricher(constants.ObjectTypeGroupsIOMailingList),
	}
}
