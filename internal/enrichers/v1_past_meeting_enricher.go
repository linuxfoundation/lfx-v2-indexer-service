// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// V1PastMeetingEnricher handles V1 past meeting-specific enrichment logic
type V1PastMeetingEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *V1PastMeetingEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches V1 past meeting-specific data
func (e *V1PastMeetingEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// extractPublicFlag extracts the public flag from the V1 past meeting message data
func (e *V1PastMeetingEnricher) extractPublicFlag(data map[string]any) bool {
	visibility, ok := data["visibility"].(string)
	if !ok {
		return false
	}
	return visibility == "public"
}

// NewV1PastMeetingEnricher creates a new V1 past meeting enricher
func NewV1PastMeetingEnricher() Enricher {
	enricher := &V1PastMeetingEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeV1PastMeeting,
		WithPublicFlag(enricher.extractPublicFlag),
	)
	return enricher
}
