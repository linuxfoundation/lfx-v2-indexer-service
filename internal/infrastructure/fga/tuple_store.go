// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
)

// maxOperationsPerBatch is the OpenFGA Write API limit on writes plus deletes
// in one request.
const maxOperationsPerBatch = 100

// writeCollisionIgnoreOptions makes OpenFGA treat a write of an existing tuple,
// or a delete of an absent one, as a no-op instead of failing the request. Both
// fields must be set: a request mixing ignore and error semantics reverts to
// error for the whole request. A retry after a partly applied batch collides
// this way.
var writeCollisionIgnoreOptions = client.ClientWriteOptions{
	Conflict: client.ClientWriteConflictOptions{
		OnDuplicateWrites: client.CLIENT_WRITE_REQUEST_ON_DUPLICATE_WRITES_IGNORE,
		OnMissingDeletes:  client.CLIENT_WRITE_REQUEST_ON_MISSING_DELETES_IGNORE,
	},
}

// tupleStore owns raw tuple I/O: paginated reads and batched writes that drop
// tuples OpenFGA rejects as invalid.
type tupleStore struct {
	client fgaClient
	logger *slog.Logger
}

// readObjectTuples returns every direct tuple on object, following the
// continuation token across pages.
func (s tupleStore) readObjectTuples(ctx context.Context, object string) ([]openfga.Tuple, error) {
	req := client.ClientReadRequest{
		Object: openfga.PtrString(object),
	}
	options := client.ClientReadOptions{}
	var tuples []openfga.Tuple
	for {
		resp, err := s.client.Read(ctx, req, options)
		if err != nil {
			return nil, err
		}
		tuples = append(tuples, resp.Tuples...)
		if resp.ContinuationToken == "" {
			break
		}
		options.ContinuationToken = openfga.PtrString(resp.ContinuationToken)
	}
	return tuples, nil
}

// getTuplesByUserAndObject returns the tuples user holds directly on object.
func (s tupleStore) getTuplesByUserAndObject(ctx context.Context, user, object string) ([]client.ClientTupleKey, error) {
	tuples, err := s.readObjectTuples(ctx, object)
	if err != nil {
		return nil, err
	}
	var filtered []client.ClientTupleKey
	for _, tuple := range tuples {
		if tuple.Key.User == user {
			filtered = append(filtered, client.ClientTupleKey{
				User: tuple.Key.User, Relation: tuple.Key.Relation, Object: object,
			})
		}
	}
	return filtered, nil
}

// writeAndDeleteTuples applies writes and deletes in batches of at most
// maxOperationsPerBatch. It returns the tuple strings of writes OpenFGA
// rejected as invalid and that were therefore never stored. Batches already
// sent stay applied when a later batch fails.
func (s tupleStore) writeAndDeleteTuples(
	ctx context.Context,
	writes []client.ClientTupleKey,
	deletes []client.ClientTupleKeyWithoutCondition,
) ([]string, error) {
	if len(writes) == 0 && len(deletes) == 0 {
		return nil, nil
	}
	if len(writes)+len(deletes) <= maxOperationsPerBatch {
		return s.writeAndDeleteTuplesBatch(ctx, writes, deletes)
	}

	s.logger.InfoContext(ctx, "batching write operations due to size",
		"writes_count", len(writes), "deletes_count", len(deletes))
	var skippedWrites []string
	for batch := 1; len(writes) > 0 || len(deletes) > 0; batch++ {
		var batchWrites []client.ClientTupleKey
		var batchDeletes []client.ClientTupleKeyWithoutCondition
		batchWrites, writes = splitAt(writes, maxOperationsPerBatch)
		batchDeletes, deletes = splitAt(deletes, maxOperationsPerBatch-len(batchWrites))

		batchSkipped, err := s.writeAndDeleteTuplesBatch(ctx, batchWrites, batchDeletes)
		skippedWrites = append(skippedWrites, batchSkipped...)
		if err != nil {
			s.logger.ErrorContext(ctx, "failed to execute batch", "batch_number", batch,
				"batch_writes", len(batchWrites), "batch_deletes", len(batchDeletes), "error", err.Error())
			return skippedWrites, err
		}
	}
	return skippedWrites, nil
}

func splitAt[T any](items []T, n int) (head, tail []T) {
	if n > len(items) {
		n = len(items)
	}
	return items[:n], items[n:]
}

// writeAndDeleteTuplesBatch sends one Write request. When OpenFGA answers
// with a validation_error naming a tuple, that tuple is removed and the rest
// is sent again; any other error is returned.
func (s tupleStore) writeAndDeleteTuplesBatch(
	ctx context.Context,
	writes []client.ClientTupleKey,
	deletes []client.ClientTupleKeyWithoutCondition,
) ([]string, error) {
	var skippedWrites []string
	for {
		req := client.ClientWriteRequest{Writes: writes, Deletes: deletes}
		_, err := s.client.Write(ctx, req, writeCollisionIgnoreOptions)
		if err == nil {
			break
		}
		tupleStr, ok := extractInvalidTuple(err)
		if !ok {
			return skippedWrites, err
		}
		var removedWrite, removed bool
		writes, removedWrite = removeInvalidWriteTuple(writes, tupleStr)
		removed = removedWrite
		if !removed {
			deletes, removed = removeInvalidDeleteTuple(deletes, tupleStr)
		}
		if !removed {
			return skippedWrites, err
		}
		if removedWrite {
			skippedWrites = append(skippedWrites, tupleStr)
		}
		s.logger.WarnContext(ctx, "skipping invalid tuple and retrying batch write",
			"remaining_writes", len(writes), "remaining_deletes", len(deletes))
		if len(writes) == 0 && len(deletes) == 0 {
			return skippedWrites, nil
		}
	}
	s.logger.InfoContext(ctx, "wrote and deleted tuples",
		"writes_count", len(writes), "deletes_count", len(deletes))
	return skippedWrites, nil
}

// extractInvalidTuple returns the tuple string ("object:id#relation@user:id")
// named by an OpenFGA validation_error, and whether there was one.
func extractInvalidTuple(err error) (string, bool) {
	var validationErr openfga.FgaApiValidationError
	if !errors.As(err, &validationErr) {
		return "", false
	}
	const prefix = "Invalid tuple '"
	_, afterPrefix, found := strings.Cut(validationErr.Error(), prefix)
	if !found {
		return "", false
	}
	tuple, _, found := strings.Cut(afterPrefix, "'")
	if !found {
		return "", false
	}
	return tuple, true
}

// removeInvalidWriteTuple returns writes without the first tuple matching
// tupleStr, and whether one was removed.
func removeInvalidWriteTuple(writes []client.ClientTupleKey, tupleStr string) ([]client.ClientTupleKey, bool) {
	for i, t := range writes {
		if tupleString(t.Object, t.Relation, t.User) == tupleStr {
			result := make([]client.ClientTupleKey, 0, len(writes)-1)
			result = append(result, writes[:i]...)
			return append(result, writes[i+1:]...), true
		}
	}
	return writes, false
}

// removeInvalidDeleteTuple returns deletes without the first tuple matching
// tupleStr, and whether one was removed.
func removeInvalidDeleteTuple(
	deletes []client.ClientTupleKeyWithoutCondition,
	tupleStr string,
) ([]client.ClientTupleKeyWithoutCondition, bool) {
	for i, t := range deletes {
		if tupleString(t.Object, t.Relation, t.User) == tupleStr {
			result := make([]client.ClientTupleKeyWithoutCondition, 0, len(deletes)-1)
			result = append(result, deletes[:i]...)
			return append(result, deletes[i+1:]...), true
		}
	}
	return deletes, false
}

// tupleString is the "object#relation@user" form OpenFGA uses to name a tuple
// in a validation_error.
func tupleString(object, relation, user string) string {
	return object + "#" + relation + "@" + user
}
