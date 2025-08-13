// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package enrichers provides data enrichment functionality for different object types.
package enrichers

import (
	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// MeetingEnricher handles meeting-specific enrichment logic
type MeetingEnricher struct {
	defaultEnricher Enricher
}

// ObjectType returns the object type this enricher handles.
func (e *MeetingEnricher) ObjectType() string {
	return e.defaultEnricher.ObjectType()
}

// EnrichData enriches meeting-specific data
func (e *MeetingEnricher) EnrichData(body *contracts.TransactionBody, transaction *contracts.LFXTransaction) error {
	return e.defaultEnricher.EnrichData(body, transaction)
}

// extractPublicFlag extracts the public flag from the meeting message data
func (e *MeetingEnricher) extractPublicFlag(data map[string]any) bool {
	visibility, ok := data["visibility"].(string)
	if !ok {
		return false
	}
	return visibility == "public"
}

// NewMeetingEnricher creates a new meeting enricher
func NewMeetingEnricher() Enricher {
	enricher := &MeetingEnricher{}
	enricher.defaultEnricher = newDefaultEnricher(
		constants.ObjectTypeMeeting,
		WithPublicFlag(enricher.extractPublicFlag),
	)
	return enricher
}
