// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package fga

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
	"github.com/stretchr/testify/mock"
)

type mockFGAClient struct {
	mock.Mock
}

func (m *mockFGAClient) Read(
	ctx context.Context,
	req client.ClientReadRequest,
	options client.ClientReadOptions,
) (*client.ClientReadResponse, error) {
	args := m.Called(ctx, req, options)
	//nolint:errcheck // the error is passed through to the caller
	return args.Get(0).(*client.ClientReadResponse), args.Error(1)
}

func (m *mockFGAClient) Write(
	ctx context.Context,
	req client.ClientWriteRequest,
	options client.ClientWriteOptions,
) (*client.ClientWriteResponse, error) {
	args := m.Called(ctx, req, options)
	//nolint:errcheck // the error is passed through to the caller
	return args.Get(0).(*client.ClientWriteResponse), args.Error(1)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestStore(m *mockFGAClient) tupleStore {
	return tupleStore{client: m, logger: discardLogger()}
}

func newTestWriter(m *mockFGAClient) *Writer {
	return &Writer{store: newTestStore(m), lease: noLease{}, logger: discardLogger(), stop: func() {}, done: make(chan struct{})}
}

// noLease grants every lease at once, for tests that run one operation at a
// time.
type noLease struct{}

func (noLease) Acquire(ctx context.Context, _ string) (context.Context, func(), error) {
	return ctx, func() {}, nil
}

// busyLease refuses every lease and records whether one was asked for.
type busyLease struct{ called bool }

func (l *busyLease) Acquire(context.Context, string) (context.Context, func(), error) {
	l.called = true
	return nil, nil, errLeaseBusy
}

// readOptions is the ClientReadOptions every first-page tuple read sends.
var readOptions = client.ClientReadOptions{Consistency: openfga.CONSISTENCYPREFERENCE_HIGHER_CONSISTENCY.Ptr()}

// readFor matches a Read request for object.
func readFor(object string) any {
	return mock.MatchedBy(func(req client.ClientReadRequest) bool {
		return req.Object != nil && *req.Object == object
	})
}

func tuplesResponse(tuples ...openfga.Tuple) *client.ClientReadResponse {
	return &client.ClientReadResponse{Tuples: tuples}
}

func tuple(object, relation, user string) openfga.Tuple {
	return openfga.Tuple{Key: openfga.TupleKey{User: user, Relation: relation, Object: object}}
}

// makeValidationError builds the error the SDK returns for an OpenFGA
// validation_error response.
func makeValidationError(message string) openfga.FgaApiValidationError {
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:8080", nil) //nolint:noctx
	resp := &http.Response{StatusCode: 422, Header: http.Header{}, Request: req}
	body := []byte(`{"code":"validation_error","message":"` + message + `"}`)
	return openfga.NewFgaApiValidationError("Write", nil, resp, body, "store-id")
}

// fakeStatusErr stands in for an SDK API error with an HTTP status code.
type fakeStatusErr struct{ code int }

func (e fakeStatusErr) Error() string           { return fmt.Sprintf("HTTP %d", e.code) }
func (e fakeStatusErr) ResponseStatusCode() int { return e.code }
