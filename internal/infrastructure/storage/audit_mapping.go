// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

type auditMappingField struct {
	Type       string                       `json:"type"`
	Index      *bool                        `json:"index"`
	DocValues  *bool                        `json:"doc_values"`
	Enabled    *bool                        `json:"enabled"`
	Dynamic    string                       `json:"dynamic"`
	Properties map[string]auditMappingField `json:"properties"`
}

// auditFieldsMatch recursively checks required types and explicit privacy flags,
// allowing OpenSearch's implicit object type when properties are present.
func auditFieldsMatch(actual, required map[string]auditMappingField) bool {
	for name, expected := range required {
		field, present := actual[name]
		// OpenSearch may omit the explicit object type when properties are set.
		if field.Type == "" && len(field.Properties) > 0 {
			field.Type = "object"
		}
		enabled := field.Enabled == nil || *field.Enabled
		if !present || field.Type != expected.Type ||
			(expected.Index != nil && (field.Index == nil || *field.Index != *expected.Index)) ||
			(expected.Enabled != nil && enabled != *expected.Enabled) ||
			(expected.Dynamic != "" && field.Dynamic != expected.Dynamic) || !auditFieldsMatch(field.Properties, expected.Properties) {
			return false
		}
	}
	return true
}

// auditPrivateFields defines the minimum private history or delivery mapping,
// including the fields needed to write and later verify permanent erasure fences.
func auditPrivateFields(history bool) map[string]auditMappingField {
	no := false
	yes := true
	fields := map[string]auditMappingField{
		"publisher": {Type: "keyword"}, "event_id": {Type: "keyword"},
		"fingerprint": {Type: "keyword", Index: &no},
		"erased":      {Type: "boolean"}, "erased_at": {Type: "date"},
	}
	if !history {
		fields["origin"] = auditMappingField{Type: "keyword"}
		return fields
	}
	for _, name := range []string{"object_type", "object_id", "action", "history_check_object", "history_check_relation"} {
		fields[name] = auditMappingField{Type: "keyword"}
	}
	fields["resource_revision"] = auditMappingField{Type: "long"}
	fields["occurred_at"] = auditMappingField{Type: "date"}
	fields["ingested_at"] = auditMappingField{Type: "date"}
	fields["audit"] = auditMappingField{Type: "object", Enabled: &no}
	fields["scope"] = auditMappingField{Type: "object", Enabled: &yes, Dynamic: "strict", Properties: map[string]auditMappingField{
		"before": {Type: "keyword"}, "after": {Type: "keyword"},
	}}
	return fields
}

type auditMapping struct {
	Mappings struct {
		Dynamic    string                       `json:"dynamic"`
		Properties map[string]auditMappingField `json:"properties"`
		Source     struct {
			Enabled  *bool    `json:"enabled"`
			Includes []string `json:"includes"`
			Excludes []string `json:"excludes"`
		} `json:"_source"`
	} `json:"mappings"`
}

// auditSourceRetained rejects disabled or filtered source storage, which could
// otherwise acknowledge an append without retaining its unindexed snapshots.
func auditSourceRetained(mapping auditMapping) bool {
	source := mapping.Mappings.Source
	return (source.Enabled == nil || *source.Enabled) && len(source.Includes) == 0 && len(source.Excludes) == 0
}

// VerifyAuditMappings fails startup if an existing index or alias backing
// index cannot enforce the closed, private history and internal ordering
// field mappings. A Helm hook that skips an existing index is not proof of
// the mapping actually installed in the cluster.
func VerifyAuditMappings(ctx context.Context, client *opensearch.Client, currentIndex string, timeout time.Duration) error {
	if client == nil || currentIndex == "" || currentIndex == historyIndex || timeout <= 0 {
		return fmt.Errorf("audit mapping verification is not configured")
	}
	history, err := readAuditMappings(ctx, client, historyIndex, timeout)
	if err != nil {
		return fmt.Errorf("read private history index mapping: %w", err)
	}
	if len(history) != 1 {
		return fmt.Errorf("history index must resolve to one backing index")
	}
	for _, mapping := range history {
		if !auditSourceRetained(mapping) || mapping.Mappings.Dynamic != "strict" || !auditFieldsMatch(mapping.Mappings.Properties, auditPrivateFields(true)) {
			return fmt.Errorf("private history index mapping is not strict and unindexed")
		}
	}
	deliveries, err := readAuditMappings(ctx, client, auditDeliveryReceiptsIndex, timeout)
	if err != nil {
		return fmt.Errorf("read private audit deliveries index mapping: %w", err)
	}
	if len(deliveries) != 1 {
		return fmt.Errorf("audit deliveries index must resolve to one backing index")
	}
	for _, mapping := range deliveries {
		if !auditSourceRetained(mapping) || mapping.Mappings.Dynamic != "strict" || !auditFieldsMatch(mapping.Mappings.Properties, auditPrivateFields(false)) {
			return fmt.Errorf("private audit deliveries index mapping is not strict and unindexed")
		}
	}
	current, err := readAuditMappings(ctx, client, currentIndex, timeout)
	if err != nil {
		return fmt.Errorf("read current-resource index mapping: %w", err)
	}
	if len(current) != 1 {
		return fmt.Errorf("current-resource index must resolve to one backing index")
	}
	for _, mapping := range current {
		if !auditSourceRetained(mapping) {
			return fmt.Errorf("current-resource source storage is disabled or filtered")
		}
		for field, fieldType := range map[string]string{
			"audit_source_revision": "long", "audit_event_key": "keyword", "audit_event_fingerprint": "keyword",
			"audit_mutation_key": "keyword", "audit_mutation_fingerprint": "keyword",
			"audit_projection_order_time": "keyword", "audit_projection_order_key": "keyword",
		} {
			property, present := mapping.Mappings.Properties[field]
			if !present || property.Type != fieldType || property.Index == nil || *property.Index {
				return fmt.Errorf("current-resource audited ordering mapping is missing or indexed")
			}
			if field == "audit_source_revision" && property.DocValues != nil && !*property.DocValues {
				return fmt.Errorf("current-resource audit revision doc values must be enabled")
			}
		}
	}
	return nil
}

// readAuditMappings loads bounded mapping metadata under a request deadline,
// returning sanitized failures rather than OpenSearch response payloads.
func readAuditMappings(ctx context.Context, client *opensearch.Client, index string, timeout time.Duration) (map[string]auditMapping, error) {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := (opensearchapi.IndicesGetMappingRequest{Index: []string{index}}).Do(requestCtx, client)
	if err != nil {
		return nil, fmt.Errorf("OpenSearch mapping transport failure")
	}
	defer func() { _ = res.Body.Close() }()
	if res.IsError() {
		return nil, fmt.Errorf("OpenSearch mapping status %d", res.StatusCode)
	}
	var mappings map[string]auditMapping
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&mappings); err != nil || len(mappings) == 0 {
		return nil, fmt.Errorf("invalid OpenSearch mapping response")
	}
	return mappings, nil
}
