// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// CommitteeSettingsEnricher handles committee-settings-specific enrichment logic
type CommitteeSettingsEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *CommitteeSettingsEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches committee-specific data
func (e *CommitteeSettingsEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// NewCommitteeSettingsEnricher creates a new committee settings enricher
func NewCommitteeSettingsEnricher() Enricher {
	return &CommitteeSettingsEnricher{
		defaultEnricher: newDefaultEnricher(constants.ObjectTypeCommitteeSettings),
	}
}
