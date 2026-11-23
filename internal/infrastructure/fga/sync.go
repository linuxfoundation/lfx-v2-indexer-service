// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"slices"
	"strings"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/infrastructure/fgacache"
)

const (
	userPrefix           = "user:"
	userWildcard         = "user:*"
	teamPrefix           = "team:"
	globalRelationPrefix = "global_"
	relationViewer       = "viewer"
	relationParent       = "parent"
)

// syncResult is what syncObjectTuples sent to OpenFGA, how many of the writes
// OpenFGA skipped as invalid, and the relation keys of user tuples it stored.
type syncResult struct {
	writes   []client.ClientTupleKey
	deletes  []client.ClientTupleKeyWithoutCondition
	skipped  int
	seedKeys []string
}

// syncObjectTuples makes the tuples on object match relations: it reads the
// current tuples, writes the missing ones and deletes the rest, except
// relations in excludeRelations and team grants on non-global relations.
// On a write error the result still lists what was sent, because earlier
// batches may have been applied.
func (s tupleStore) syncObjectTuples(
	ctx context.Context,
	object string,
	relations []client.ClientTupleKey,
	excludeRelations ...string,
) (syncResult, error) {
	desired := relationsMap(object, relations)
	existing, err := s.readObjectTuples(ctx, object)
	if err != nil {
		return syncResult{}, err
	}

	result := syncResult{deletes: staleTuples(object, desired, existing, excludeRelations)}
	seedKeys := make(map[string]struct{}, len(desired))
	for _, relation := range desired {
		result.writes = append(result.writes, relation)
		// Only user tuples are seeded: access checks always resolve to a user.
		if strings.HasPrefix(relation.User, userPrefix) {
			seedKeys[fgacache.RelationKey(relation.Object, relation.Relation, relation.User)] = struct{}{}
		}
	}
	if len(result.writes) == 0 && len(result.deletes) == 0 {
		return syncResult{}, nil
	}

	skippedWrites, err := s.writeAndDeleteTuples(ctx, result.writes, result.deletes)
	if err != nil {
		return result, err
	}
	result.skipped = len(skippedWrites)
	// A write OpenFGA skipped as invalid was never stored and must not be
	// seeded as allowed.
	for _, tupleStr := range skippedWrites {
		delete(seedKeys, tupleStr)
	}
	for key := range seedKeys {
		result.seedKeys = append(result.seedKeys, key)
	}
	slices.Sort(result.seedKeys)
	return result, nil
}

// relationsMap keys the desired tuples by "relation@user", fills in an empty
// Object and drops tuples for any other object.
func relationsMap(object string, relations []client.ClientTupleKey) map[string]client.ClientTupleKey {
	desired := make(map[string]client.ClientTupleKey, len(relations))
	for _, relation := range relations {
		switch {
		case relation.Object == "":
			relation.Object = object
		case relation.Object != object:
			continue
		}
		desired[relation.Relation+"@"+relation.User] = relation
	}
	return desired
}

// staleTuples removes the existing tuples from desired and returns the
// existing tuples that are not desired and may be deleted.
func staleTuples(
	object string,
	desired map[string]client.ClientTupleKey,
	existing []openfga.Tuple,
	excludeRelations []string,
) []client.ClientTupleKeyWithoutCondition {
	var deletes []client.ClientTupleKeyWithoutCondition
	for _, tuple := range existing {
		key := tuple.Key.Relation + "@" + tuple.Key.User
		if _, match := desired[key]; match {
			delete(desired, key)
			continue
		}
		if slices.Contains(excludeRelations, tuple.Key.Relation) {
			continue
		}
		// Team grants are managed outside the publisher's sync, except on
		// global_ relations, which the publisher owns.
		if strings.HasPrefix(tuple.Key.User, teamPrefix) && !strings.HasPrefix(tuple.Key.Relation, globalRelationPrefix) {
			continue
		}
		deletes = append(deletes, client.ClientTupleKeyWithoutCondition{
			User: tuple.Key.User, Relation: tuple.Key.Relation, Object: object,
		})
	}
	return deletes
}
