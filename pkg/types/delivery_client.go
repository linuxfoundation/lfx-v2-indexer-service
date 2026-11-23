// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/constants"
)

// Outcome tells a producer what to do with its durable marker after an
// lfx.indexer.index.{object_type} request.
type Outcome int

const (
	// Success means every half in the request has landed: clear the marker.
	Success Outcome = iota
	// Retry means the result is unknown or retryable: send the same request
	// again with backoff. Replays converge.
	Retry
	// Drop means the request is invalid and fails the same way on every
	// retry: drop it and alert.
	Drop
)

func (o Outcome) String() string {
	switch o {
	case Success:
		return "success"
	case Retry:
		return "retry"
	case Drop:
		return "drop"
	default:
		return fmt.Sprintf("outcome(%d)", int(o))
	}
}

const (
	replyOK             = "OK"
	replyRejectedPrefix = "ERROR: rejected: "
)

// SendIndexRequest sends req to lfx.indexer.index.{objectType}, waits up to
// timeout for the reply, and classifies it. The error describes any outcome
// other than Success. timeout must exceed the indexer's
// INDEXER_REQUEST_TIMEOUT, or a request still being applied is reported as
// Retry while it may yet succeed.
func SendIndexRequest(ctx context.Context, nc *nats.Conn, objectType string, req IndexRequest, timeout time.Duration) (Outcome, error) {
	if objectType == "" || strings.ContainsAny(objectType, ".*> ") {
		return Drop, fmt.Errorf("invalid object type %q", objectType)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return Drop, fmt.Errorf("encode index request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	msg, err := nc.RequestWithContext(ctx, constants.IndexerIndexPrefix+objectType, payload)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Retry, fmt.Errorf("no reply within %s: %w", timeout, err)
		}
		return Retry, fmt.Errorf("index request: %w", err)
	}
	return ClassifyIndexReply(string(msg.Data))
}

// ClassifyIndexReply maps a reply body to an Outcome. Anything other than OK
// or a rejected error is retryable, so an unexpected reply never drops a
// write.
func ClassifyIndexReply(reply string) (Outcome, error) {
	switch {
	case reply == replyOK:
		return Success, nil
	case strings.HasPrefix(reply, replyRejectedPrefix):
		return Drop, errors.New(reply)
	default:
		return Retry, errors.New(reply)
	}
}
