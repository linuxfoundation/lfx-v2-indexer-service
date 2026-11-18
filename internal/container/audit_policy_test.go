// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package container

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

func TestValidateAuditPolicyCoverage(t *testing.T) {
	policy := services.AuditPublisherPolicy{Resources: map[string]services.AuditResourcePolicy{
		"fixture_resource": {
			Origins: map[types.AuditOrigin]bool{types.AuditOriginMutation: true},
			Schemas: map[string]func(before, after json.RawMessage) error{"fixture.audit.v1": func(_, _ json.RawMessage) error { return nil }},
		},
		"projection_only": {Origins: map[types.AuditOrigin]bool{types.AuditOriginBackfill: true}},
	}}
	require.NoError(t, validateAuditPolicyCoverage([]services.AuditPublisherPolicy{policy}, []string{"fixture_resource"}))
	require.ErrorContains(t, validateAuditPolicyCoverage([]services.AuditPublisherPolicy{policy}, []string{"other_resource"}), "must be audit-required")
	require.ErrorContains(t, validateAuditPolicyCoverage([]services.AuditPublisherPolicy{policy}, []string{"fixture_resource", "projection_only"}), "lacks an approved mutation schema")
}

// TestValidateAuditPolicyCoverage_ProjectionFences permits only backfill to
// prepare an unfenced type; ongoing rebuild/sync origins need legacy exclusion.
func TestValidateAuditPolicyCoverage_ProjectionFences(t *testing.T) {
	for _, origin := range []types.AuditOrigin{types.AuditOriginBackfill, types.AuditOriginReindex, types.AuditOriginSyncEcho} {
		t.Run(string(origin), func(t *testing.T) {
			policies := []services.AuditPublisherPolicy{{Resources: map[string]services.AuditResourcePolicy{
				"fixture_resource": {Origins: map[types.AuditOrigin]bool{types.AuditOriginMutation: true}, Schemas: map[string]func(json.RawMessage, json.RawMessage) error{"fixture.v1": func(_, _ json.RawMessage) error { return nil }}},
				"bootstrap_type":   {Origins: map[types.AuditOrigin]bool{origin: true}},
			}}}
			err := validateAuditPolicyCoverage(policies, []string{"fixture_resource"})
			if origin == types.AuditOriginBackfill {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "must be audit-required")
			}
		})
	}
}

// TestValidateAuditPolicyCoverage_MultiplePublishers preserves the documented
// shared-authority option; coordination is an operator/source contract, not
// something that can be inferred from distinct publisher namespace strings.
func TestValidateAuditPolicyCoverage_MultiplePublishers(t *testing.T) {
	resource := services.AuditResourcePolicy{
		Origins: map[types.AuditOrigin]bool{types.AuditOriginMutation: true},
		Schemas: map[string]func(json.RawMessage, json.RawMessage) error{"fixture.v1": func(_, _ json.RawMessage) error { return nil }},
	}
	policies := []services.AuditPublisherPolicy{
		{Publisher: "source-a", Resources: map[string]services.AuditResourcePolicy{"fixture_resource": resource}},
		{Publisher: "source-b", Resources: map[string]services.AuditResourcePolicy{"fixture_resource": resource}},
	}
	require.NoError(t, validateAuditPolicyCoverage(policies, []string{"fixture_resource"}))
}
