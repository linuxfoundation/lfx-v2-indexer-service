// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// V1MeetingEnricher handles V1 meeting-specific enrichment logic
type V1MeetingEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *V1MeetingEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches V1 meeting-specific data
func (e *V1MeetingEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// extractPublicFlag extracts the public flag from the V1 meeting message data
func (e *V1MeetingEnricher) extractPublicFlag(data map[string]any) bool {
	visibility, ok := data["visibility"].(string)
	if !ok {
		return false
	}
	return visibility == "public"
}

// NewV1MeetingEnricher creates a new V1 meeting enricher
func NewV1MeetingEnricher() Enricher {
	enricher := &V1MeetingEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeV1Meeting,
		WithPublicFlag(enricher.extractPublicFlag),
	)
	return enricher
}
