// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openfga/go-sdk/client"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// updateAccess fully syncs an object's tuples to data: public, references and
// relations. Relations in exclude_relations are left alone.
func (w *Writer) updateAccess(ctx context.Context, op types.FGAOperation) (contracts.FGAWriteResult, error) {
	data, err := decodeData[types.FGAAccessData](op)
	if err != nil {
		return contracts.FGAWriteResult{}, err
	}
	if data.UID == "" {
		return contracts.FGAWriteResult{}, rejected(fmt.Errorf("%s ID not found", op.ObjectType))
	}
	object := buildObjectID(op.ObjectType, data.UID)
	tuples, err := accessTuples(object, op.ObjectType, data)
	if err != nil {
		return contracts.FGAWriteResult{}, err
	}
	result, err := w.store.syncObjectTuples(ctx, object, tuples, data.ExcludeRelations...)
	return syncWriteResult(result), err
}

func accessTuples(object, objectType string, data *types.FGAAccessData) ([]client.ClientTupleKey, error) {
	tuples := make([]client.ClientTupleKey, 0, 4)
	if data.Public {
		tuples = append(tuples, client.ClientTupleKey{User: userWildcard, Relation: relationViewer, Object: object})
	}
	for reference, values := range data.References {
		refType := reference
		// A parent reference points at an object of the same type.
		if reference == relationParent {
			refType = objectType
		}
		for _, value := range values {
			user, err := referenceUser(refType, value)
			if err != nil {
				return nil, err
			}
			tuples = append(tuples, client.ClientTupleKey{User: user, Relation: reference, Object: object})
		}
	}
	for relation, principals := range data.Relations {
		for _, principal := range principals {
			tuples = append(tuples, client.ClientTupleKey{User: userPrefix + principal, Relation: relation, Object: object})
		}
	}
	return tuples, nil
}

// referenceUser returns value as a "type:id" user, prefixing refType when
// value is a bare id.
func referenceUser(refType, value string) (string, error) {
	if !strings.Contains(value, ":") {
		return refType + ":" + value, nil
	}
	if typ, id, _ := strings.Cut(value, ":"); typ == "" || id == "" {
		return "", rejected(errors.New("invalid reference format: must be 'type:id' with both parts non-empty"))
	}
	return value, nil
}

// deleteAccess removes every publisher-managed tuple on the object, keeping
// team grants on non-global relations.
func (w *Writer) deleteAccess(ctx context.Context, op types.FGAOperation) (contracts.FGAWriteResult, error) {
	data, err := decodeData[types.FGADeleteData](op)
	if err != nil {
		return contracts.FGAWriteResult{}, err
	}
	if data.UID == "" {
		return contracts.FGAWriteResult{}, rejected(errors.New("uid is required"))
	}
	result, err := w.store.syncObjectTuples(ctx, buildObjectID(op.ObjectType, data.UID), nil)
	return syncWriteResult(result), err
}

func syncWriteResult(result syncResult) contracts.FGAWriteResult {
	return contracts.FGAWriteResult{
		Changed:  changed(len(result.writes), len(result.deletes), result.skipped),
		SeedKeys: result.seedKeys,
	}
}

// changed reports whether a successful write stored or removed any tuple.
// Writes OpenFGA skipped as invalid were not stored.
func changed(writes, deletes, skippedWrites int) bool {
	return deletes > 0 || writes > skippedWrites
}

// memberPut gives the user every listed relation and removes the user's
// mutually exclusive relations that are not listed.
func (w *Writer) memberPut(ctx context.Context, op types.FGAOperation) (contracts.FGAWriteResult, error) {
	data, err := decodeData[types.FGAMemberData](op)
	if err != nil {
		return contracts.FGAWriteResult{}, err
	}
	if err := validateMemberPut(data); err != nil {
		return contracts.FGAWriteResult{}, err
	}
	object := buildObjectID(op.ObjectType, data.UID)
	writes, deletes, err := w.memberPutChanges(ctx, object, userPrefix+data.Username, data)
	if err != nil {
		return contracts.FGAWriteResult{}, err
	}
	return w.applyChanges(ctx, writes, deletes)
}

func validateMemberPut(data *types.FGAMemberData) error {
	if data.Username == "" {
		return rejected(errors.New("username is required"))
	}
	if data.UID == "" {
		return rejected(errors.New("uid is required"))
	}
	if len(data.Relations) == 0 {
		return rejected(errors.New("relations array cannot be empty"))
	}
	for _, relation := range data.Relations {
		if relation == "" {
			return rejected(errors.New("relation value cannot be empty"))
		}
	}
	return nil
}

func (w *Writer) memberPutChanges(
	ctx context.Context,
	object, user string,
	data *types.FGAMemberData,
) ([]client.ClientTupleKey, []client.ClientTupleKeyWithoutCondition, error) {
	existing, err := w.store.getTuplesByUserAndObject(ctx, user, object)
	if err != nil {
		return nil, nil, err
	}
	desired := make(map[string]bool, len(data.Relations))
	for _, relation := range data.Relations {
		desired[relation] = true
	}
	exclusive := make(map[string]bool, len(data.MutuallyExclusiveWith))
	for _, relation := range data.MutuallyExclusiveWith {
		exclusive[relation] = true
	}

	var deletes []client.ClientTupleKeyWithoutCondition
	held := make(map[string]bool, len(existing))
	for _, tuple := range existing {
		held[tuple.Relation] = true
		if exclusive[tuple.Relation] && !desired[tuple.Relation] {
			deletes = append(deletes, client.ClientTupleKeyWithoutCondition{
				User: tuple.User, Relation: tuple.Relation, Object: tuple.Object,
			})
		}
	}
	var writes []client.ClientTupleKey
	for relation := range desired {
		if !held[relation] {
			writes = append(writes, client.ClientTupleKey{User: user, Relation: relation, Object: object})
		}
	}
	return writes, deletes, nil
}

// memberRemove removes the listed relations from the user, or all of the
// user's relations on the object when none are listed. Empty entries are
// ignored.
func (w *Writer) memberRemove(ctx context.Context, op types.FGAOperation) (contracts.FGAWriteResult, error) {
	data, err := decodeData[types.FGAMemberData](op)
	if err != nil {
		return contracts.FGAWriteResult{}, err
	}
	if data.Username == "" {
		return contracts.FGAWriteResult{}, rejected(errors.New("member_remove: username is required"))
	}
	if data.UID == "" {
		return contracts.FGAWriteResult{}, rejected(errors.New("member_remove: uid is required"))
	}
	object := buildObjectID(op.ObjectType, data.UID)
	user := userPrefix + data.Username

	var deletes []client.ClientTupleKeyWithoutCondition
	for _, relation := range data.Relations {
		if relation != "" {
			deletes = append(deletes, client.ClientTupleKeyWithoutCondition{User: user, Relation: relation, Object: object})
		}
	}
	if len(deletes) == 0 {
		held, err := w.store.getTuplesByUserAndObject(ctx, user, object)
		if err != nil {
			return contracts.FGAWriteResult{}, err
		}
		for _, tuple := range held {
			deletes = append(deletes, client.ClientTupleKeyWithoutCondition{
				User: tuple.User, Relation: tuple.Relation, Object: tuple.Object,
			})
		}
	}
	return w.applyChanges(ctx, nil, deletes)
}

// applyChanges sends writes and deletes. Changed is true on an error, because
// an earlier batch may have been applied.
func (w *Writer) applyChanges(
	ctx context.Context,
	writes []client.ClientTupleKey,
	deletes []client.ClientTupleKeyWithoutCondition,
) (contracts.FGAWriteResult, error) {
	if len(writes) == 0 && len(deletes) == 0 {
		return contracts.FGAWriteResult{}, nil
	}
	skipped, err := w.store.writeAndDeleteTuples(ctx, writes, deletes)
	if err != nil {
		return contracts.FGAWriteResult{Changed: true}, err
	}
	return contracts.FGAWriteResult{Changed: changed(len(writes), len(deletes), len(skipped))}, nil
}
