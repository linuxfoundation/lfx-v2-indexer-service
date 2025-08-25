// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// GroupsIOServiceEnricher handles GroupsIO service-specific enrichment logic
type GroupsIOServiceEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *GroupsIOServiceEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches GroupsIO service-specific data
func (e *GroupsIOServiceEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// NewGroupsIOServiceEnricher creates a new GroupsIO service enricher
func NewGroupsIOServiceEnricher() Enricher {
	return &GroupsIOServiceEnricher{
		defaultEnricher: newDefaultEnricher(constants.ObjectTypeGroupsIOService),
	}
}
