// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"fmt"
	"io"
)

// drainAuditWriteResponse releases a write connection before any follow-up GET.
// The request's existing context bounds the drain. Neither response payloads
// nor raw read errors are exposed: OpenSearch can echo private indexed values.
func drainAuditWriteResponse(body io.ReadCloser) error {
	_, readErr := io.Copy(io.Discard, body)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("read audit write response: failed")
	}
	return nil
}
