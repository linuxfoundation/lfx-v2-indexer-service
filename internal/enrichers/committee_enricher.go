// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// CommitteeEnricher handles committee-specific enrichment logic
type CommitteeEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *CommitteeEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches committee-specific data
func (e *CommitteeEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// NewCommitteeEnricher creates a new committee enricher
func NewCommitteeEnricher() Enricher {
	return &CommitteeEnricher{
		defaultEnricher: newDefaultEnricher(constants.ObjectTypeCommittee),
	}
}
