// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// validateEnvelopeAdmission reserves the version/audit discriminator before any
// legacy transaction construction. Version 2 must never reach current-only writes.
func validateEnvelopeAdmission(message map[string]any, isV1 bool) error {
	for key := range message {
		if (strings.EqualFold(key, "schema_version") && key != "schema_version") ||
			(strings.EqualFold(key, "audit") && key != "audit") {
			return fmt.Errorf("schema_version and audit field names must be lowercase")
		}
	}
	raw, hasVersion := message["schema_version"]
	_, hasAudit := message["audit"]
	if !hasVersion {
		if hasAudit {
			return fmt.Errorf("audit requires schema_version 2")
		}
		return nil
	}
	if isV1 {
		return fmt.Errorf("versioned audit envelopes are not supported on V1 subjects")
	}
	encoded, err := json.Marshal(raw)
	if err != nil || raw == nil {
		return fmt.Errorf("schema_version must be an integer")
	}
	var version int
	if err := json.Unmarshal(encoded, &version); err != nil {
		return fmt.Errorf("schema_version must be an integer")
	}
	if version != types.AuditSchemaVersion {
		return fmt.Errorf("unsupported schema_version")
	}
	if !hasAudit || message["audit"] == nil {
		return fmt.Errorf("schema_version 2 requires audit")
	}
	return fmt.Errorf("schema_version 2 processing is not enabled: audit persistence and publisher authorization are not implemented")
}
