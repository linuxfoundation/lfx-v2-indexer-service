// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"errors"
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

// Reply classes and halves of the lfx.indexer.index.* reply contract:
// "OK" or "ERROR: <class>: <half>: <text>".
const (
	classTransient = "transient"
	classRejected  = "rejected"

	halfIndex = "index"
	halfAudit = "audit"
	halfFGA   = "fga"

	replyOK = "OK"

	transientPrefix = classTransient + ": "
)

// deliveryFailure is the single error a delivery reply reports.
type deliveryFailure struct {
	class string
	half  string
	text  string
}

func (f *deliveryFailure) reply() []byte {
	return []byte("ERROR: " + f.class + ": " + f.half + ": " + f.text)
}

func rejected(half, text string) *deliveryFailure {
	return &deliveryFailure{class: classRejected, half: half, text: text}
}

func transient(half, text string) *deliveryFailure {
	return &deliveryFailure{class: classTransient, half: half, text: text}
}

// rejectedError marks an index-half error as proven invalid: the same payload
// fails the same way on every replay. Error text is unchanged so the JetStream
// path logs and replies as before.
type rejectedError struct{ err error }

func (e *rejectedError) Error() string { return e.err.Error() }

func (e *rejectedError) Unwrap() error { return e.err }

// auditError marks an error from the audit delivery receipt or history store,
// so the reply names the audit half instead of the index half.
type auditError struct{ err error }

func (e *auditError) Error() string { return e.err.Error() }

func (e *auditError) Unwrap() error { return e.err }

// classifyIndexError names the half and class of an error returned by the
// index half. Unknown errors are transient: retrying an envelope that cannot
// succeed costs a retry loop, while dropping one that could succeed loses a
// write.
func classifyIndexError(err error) *deliveryFailure {
	half := halfIndex
	if errors.As(err, new(*auditError)) {
		half = halfAudit
	}
	text := err.Error()
	if errors.As(err, new(*rejectedError)) || errors.As(err, new(*contracts.InvalidTransactionError)) ||
		errors.As(err, new(*contracts.HistoryErasedError)) {
		return rejected(half, text)
	}
	return transient(half, strings.TrimPrefix(text, transientPrefix))
}

// classifyFGAError maps FGAWriter errors: a proven-invalid operation is
// rejected; a missing writer and every other error are transient.
func classifyFGAError(err error) *deliveryFailure {
	var rejection *contracts.FGARejectedError
	if errors.As(err, &rejection) {
		text := "invalid fga operation"
		if rejection.Cause != nil {
			text = rejection.Cause.Error()
		}
		return rejected(halfFGA, text)
	}
	return transient(halfFGA, err.Error())
}

// selectFailure picks the one error a reply reports: rejected before
// transient, so a producer does not keep retrying a payload that cannot
// succeed; then index, audit, fga.
func selectFailure(failures ...*deliveryFailure) *deliveryFailure {
	var selected *deliveryFailure
	for _, f := range failures {
		if f != nil && (selected == nil || failureRank(f) < failureRank(selected)) {
			selected = f
		}
	}
	return selected
}

func failureRank(f *deliveryFailure) int {
	rank := map[string]int{halfIndex: 0, halfAudit: 1, halfFGA: 2}[f.half]
	if f.class == classTransient {
		rank += 3
	}
	return rank
}
