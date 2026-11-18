// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package contracts

import (
	"fmt"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// AuditProjectionBody builds the existing current-resource shape from a
// validated v2 envelope. It uses literal IndexingConfig values (no legacy
// template expansion) and never persists a relay credential or the private
// actor profile snapshots in the current document.
func AuditProjectionBody(envelope *types.IndexerMessageEnvelope, objectType string) (*TransactionBody, error) {
	if envelope == nil || envelope.Audit == nil || envelope.IndexingConfig == nil || objectType == "" {
		return nil, fmt.Errorf("audited projection requires a validated envelope")
	}
	config := envelope.IndexingConfig
	latest := true
	body := &TransactionBody{
		ObjectRef: objectType + ":" + config.ObjectID, ObjectType: objectType,
		ObjectID: config.ObjectID, Latest: &latest,
		Tags:                 uniqueAuditTags(envelope.Tags, config.Tags),
		HistoryCheckObject:   config.HistoryCheckObject,
		HistoryCheckRelation: config.HistoryCheckRelation,
		HistoryCheckQuery:    JoinFgaQuery(config.HistoryCheckObject, config.HistoryCheckRelation),
	}
	occurredAt := envelope.Audit.OccurredAt.UTC()
	actors := []string{envelope.Audit.Attribution.ActorRef}
	if effective := envelope.Audit.Attribution.EffectiveActorRef; effective != "" && effective != actors[0] {
		actors = append(actors, effective)
	}
	switch envelope.Action {
	case constants.ActionCreated, constants.ActionUpdated:
		data, ok := envelope.Data.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("audited create/update data must be an object")
		}
		body.Data = data
		body.ParentRefs = config.ParentRefs
		body.SortName = config.SortName
		body.NameAndAliases = config.NameAndAliases
		body.Fulltext = config.Fulltext
		body.Contacts = config.Contacts
		if config.Public != nil {
			body.Public = *config.Public
		}
		body.AccessCheckObject = config.AccessCheckObject
		body.AccessCheckRelation = config.AccessCheckRelation
		body.AccessCheckQuery = JoinFgaQuery(config.AccessCheckObject, config.AccessCheckRelation)
		if envelope.Action == constants.ActionCreated {
			body.CreatedAt, body.UpdatedAt = &occurredAt, &occurredAt
			body.CreatedBy, body.CreatedByPrincipals = append([]string(nil), actors...), actors
		} else {
			body.UpdatedAt = &occurredAt
			body.UpdatedBy, body.UpdatedByPrincipals = append([]string(nil), actors...), actors
		}
	case constants.ActionDeleted:
		id, ok := envelope.Data.(string)
		if !ok || id != config.ObjectID {
			return nil, fmt.Errorf("audited delete UID must match indexing_config")
		}
		body.ParentRefs = config.ParentRefs
		body.Tags = append([]string(nil), envelope.Tags...)
		body.DeletedAt = &occurredAt
		body.DeletedBy, body.DeletedByPrincipals = append([]string(nil), actors...), actors
	default:
		return nil, fmt.Errorf("invalid audited action")
	}
	return body, nil
}

// uniqueAuditTags merges envelope/config tags in first-seen order without
// modifying the publisher's immutable source slices.
func uniqueAuditTags(first, second []string) []string {
	seen := make(map[string]bool, len(first)+len(second))
	var result []string
	for _, tag := range append(append([]string{}, first...), second...) {
		if !seen[tag] {
			result = append(result, tag)
			seen[tag] = true
		}
	}
	return result
}
