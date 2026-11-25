// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package application

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
)

func TestClassifyIndexError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"unknown error is transient", errors.New("opensearch unavailable"), "ERROR: transient: index: opensearch unavailable"},
		{"rejected index error", &rejectedError{errors.New("invalid indexing message JSON")}, "ERROR: rejected: index: invalid indexing message JSON"},
		{"wrapped rejected index error", fmt.Errorf("outer: %w", &rejectedError{errors.New("bad")}), "ERROR: rejected: index: outer: bad"},
		{"invalid transaction from validation", fmt.Errorf("failed to process transaction: %w", &contracts.InvalidTransactionError{Err: errors.New("invalid transaction action: bogus")}), "ERROR: rejected: index: failed to process transaction: invalid transaction action: bogus"},
		{"audit store error", &auditError{errors.New("append audit history: down")}, "ERROR: transient: audit: append audit history: down"},
		{"erased audit identity", &auditError{fmt.Errorf("append audit history: %w", &contracts.HistoryErasedError{})}, "ERROR: rejected: audit: append audit history: history event has been explicitly erased"},
		{"reused audit identity", &auditError{fmt.Errorf("reserve audited delivery identity: %w", &contracts.InvalidTransactionError{Err: errors.New("projection event identity reused or erased")})}, "ERROR: rejected: audit: reserve audited delivery identity: projection event identity reused or erased"},
		{"transient audit error keeps its class once", &auditError{&contracts.IdentityLookupError{}}, "ERROR: transient: audit: identity lookup unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, string(classifyIndexError(tt.err).reply()))
		})
	}
}

func TestClassifyFGAError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"rejected uses the cause text once", &contracts.FGARejectedError{Cause: errors.New("member_put requires username")}, "ERROR: rejected: fga: member_put requires username"},
		{"wrapped rejected", fmt.Errorf("apply: %w", &contracts.FGARejectedError{Cause: errors.New("unknown operation")}), "ERROR: rejected: fga: unknown operation"},
		{"rejected without a cause", &contracts.FGARejectedError{}, "ERROR: rejected: fga: invalid fga operation"},
		{"not configured", contracts.ErrFGANotConfigured, "ERROR: transient: fga: fga writer is not configured"},
		{"other error", errors.New("openfga returned 503"), "ERROR: transient: fga: openfga returned 503"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, string(classifyFGAError(tt.err).reply()))
		})
	}
}

func TestFGARejectedErrorWithoutCauseDoesNotPanic(t *testing.T) {
	require.Equal(t, "rejected: invalid fga operation", (&contracts.FGARejectedError{}).Error())
	require.Equal(t, "rejected: bad", (&contracts.FGARejectedError{Cause: errors.New("bad")}).Error())
}

func TestSelectFailure(t *testing.T) {
	indexTransient := transient(halfIndex, "a")
	auditTransient := transient(halfAudit, "b")
	fgaTransient := transient(halfFGA, "c")
	fgaRejected := rejected(halfFGA, "d")
	indexRejected := rejected(halfIndex, "e")

	tests := []struct {
		name     string
		failures []*deliveryFailure
		want     *deliveryFailure
	}{
		{"none", []*deliveryFailure{nil, nil}, nil},
		{"one", []*deliveryFailure{nil, fgaTransient}, fgaTransient},
		{"rejected before transient", []*deliveryFailure{indexTransient, fgaRejected}, fgaRejected},
		{"index before fga among rejected", []*deliveryFailure{fgaRejected, indexRejected}, indexRejected},
		{"index before audit before fga among transient", []*deliveryFailure{fgaTransient, auditTransient, indexTransient}, indexTransient},
		{"audit before fga", []*deliveryFailure{fgaTransient, auditTransient}, auditTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Same(t, tt.want, selectFailure(tt.failures...))
		})
	}
}
