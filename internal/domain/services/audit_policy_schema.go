// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// AuditPolicyManifest is a reviewable, explicit publisher and resource-schema
// registry. It carries no credentials or user values. An operator must supply
// publisher-owned, approved schema versions before enabling audited ingestion.
type AuditPolicyManifest struct {
	Publishers []AuditPublisherManifest `json:"publishers"`
}

// AuditPublisherManifest binds a namespace to one authenticated service.
type AuditPublisherManifest struct {
	Publisher       string                           `json:"publisher"`
	Principal       string                           `json:"principal"`
	CanAttestActors bool                             `json:"can_attest_actors"`
	Resources       map[string]AuditResourceManifest `json:"resources"`
}

// AuditResourceManifest holds approved origins and closed snapshot schemas.
type AuditResourceManifest struct {
	Origins []types.AuditOrigin          `json:"origins"`
	Schemas map[string]AuditSnapshotRule `json:"schemas"`
}

// AuditSnapshotRule validates an exact object shape, including nested fields.
// Unlisted fields are rejected, not copied into history by default.
type AuditSnapshotRule struct {
	Fields map[string]AuditFieldRule `json:"fields"`
}

// AuditFieldRule is a recursive closed type definition. Object fields require
// an explicit Fields map; array elements require an explicit Items rule.
type AuditFieldRule struct {
	Type     string                    `json:"type"`
	Required bool                      `json:"required,omitempty"`
	Nullable bool                      `json:"nullable,omitempty"`
	Fields   map[string]AuditFieldRule `json:"fields,omitempty"`
	Items    *AuditFieldRule           `json:"items,omitempty"`
}

var auditSchemaField = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

// The snapshot root is depth zero; each object field or array item adds one.
const maxAuditSnapshotDepth = 16

// DecodeAuditPolicies rejects unknown manifest keys and invalid schema rules.
// Errors never include source values, tokens or user identifiers.
func DecodeAuditPolicies(data []byte) ([]AuditPublisherPolicy, error) {
	if len(data) == 0 || len(data) > types.MaxAuditEnvelopeBytes {
		return nil, fmt.Errorf("invalid audit policy size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest AuditPolicyManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("invalid audit policy manifest")
	}
	if err := decoder.Decode(new(any)); err != io.EOF || len(manifest.Publishers) == 0 {
		return nil, fmt.Errorf("audit policy manifest requires publishers and one JSON value")
	}
	policies := make([]AuditPublisherPolicy, 0, len(manifest.Publishers))
	for _, entry := range manifest.Publishers {
		if !types.ValidAuditPublisherName(entry.Publisher) {
			return nil, fmt.Errorf("invalid audit policy publisher")
		}
		policy := AuditPublisherPolicy{Publisher: entry.Publisher, Principal: entry.Principal,
			CanAttestActors: entry.CanAttestActors, Resources: make(map[string]AuditResourcePolicy, len(entry.Resources))}
		for objectType, resource := range entry.Resources {
			if !types.ValidAuditObjectType(objectType) || len(resource.Origins) == 0 {
				return nil, fmt.Errorf("invalid audit policy resource")
			}
			approved := AuditResourcePolicy{Origins: make(map[types.AuditOrigin]bool), Schemas: make(map[string]func(before, after json.RawMessage) error)}
			for _, origin := range resource.Origins {
				switch origin {
				case types.AuditOriginMutation, types.AuditOriginReindex, types.AuditOriginBackfill, types.AuditOriginSyncEcho:
					approved.Origins[origin] = true
				default:
					return nil, fmt.Errorf("invalid audit policy origin")
				}
			}
			if approved.Origins[types.AuditOriginMutation] && len(resource.Schemas) == 0 {
				return nil, fmt.Errorf("mutation policy requires approved schemas")
			}
			for schema, rule := range resource.Schemas {
				if !types.ValidAuditSchemaName(schema) || len(rule.Fields) == 0 {
					return nil, fmt.Errorf("invalid audit snapshot schema")
				}
				if err := validateAuditRules(rule.Fields, 0); err != nil {
					return nil, err
				}
				approved.Schemas[schema] = func(before, after json.RawMessage) error {
					for _, snapshot := range []json.RawMessage{before, after} {
						if len(snapshot) != 0 {
							if err := validateAuditObject(snapshot, rule.Fields, 0); err != nil {
								return err
							}
						}
					}
					return nil
				}
			}
			policy.Resources[objectType] = approved
		}
		policies = append(policies, policy)
	}
	return policies, nil
}

// validateAuditRules admits only closed, credential-free schemas whose values
// fit the same depth budget enforced when snapshots are ingested.
func validateAuditRules(fields map[string]AuditFieldRule, depth int) error {
	// This map describes values one level below its containing object/array.
	// Refuse a schema whose child values would exceed the runtime limit.
	if depth >= maxAuditSnapshotDepth || len(fields) == 0 {
		return fmt.Errorf("invalid audit schema depth or empty object")
	}
	for key, field := range fields {
		if !auditSchemaField.MatchString(key) || prohibitedAuditField(key) {
			return fmt.Errorf("invalid or prohibited audit schema field")
		}
		switch field.Type {
		case "string", "boolean", "integer", "number":
			if len(field.Fields) != 0 || field.Items != nil {
				return fmt.Errorf("scalar audit schema field has nested rules")
			}
		case "object":
			if field.Items != nil {
				return fmt.Errorf("object audit schema field has array rule")
			}
			if err := validateAuditRules(field.Fields, depth+1); err != nil {
				return err
			}
		case "array":
			if len(field.Fields) != 0 || field.Items == nil {
				return fmt.Errorf("array audit schema field requires item rule")
			}
			if err := validateAuditRules(map[string]AuditFieldRule{"item": *field.Items}, depth+1); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported audit schema field type")
		}
	}
	return nil
}

// prohibitedAuditField excludes credential-bearing and raw-request field names
// even if a policy author attempts to include them in an approved schema.
func prohibitedAuditField(key string) bool {
	key = strings.ToLower(key)
	for _, part := range []string{"password", "secret", "token", "authorization", "credential", "cookie", "private_key", "request_body"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

// validateAuditObject checks a snapshot's exact approved field set and required
// properties before any private history record can be constructed.
func validateAuditObject(raw json.RawMessage, fields map[string]AuditFieldRule, depth int) error {
	if depth > maxAuditSnapshotDepth {
		return fmt.Errorf("audit snapshot exceeds schema depth")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return fmt.Errorf("audit snapshot must be an object")
	}
	for key, value := range values {
		rule, exists := fields[key]
		if !exists {
			return fmt.Errorf("audit snapshot contains unapproved field")
		}
		if err := validateAuditValue(value, rule, depth+1); err != nil {
			return err
		}
	}
	for key, rule := range fields {
		if rule.Required && values[key] == nil {
			return fmt.Errorf("audit snapshot missing required field")
		}
	}
	return nil
}

// validateAuditValue enforces explicit nullability and recursive value types,
// without converting large integer snapshots through float64.
func validateAuditValue(raw json.RawMessage, rule AuditFieldRule, depth int) error {
	if depth > maxAuditSnapshotDepth {
		return fmt.Errorf("audit snapshot exceeds schema depth")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if rule.Nullable {
			return nil
		}
		return fmt.Errorf("audit snapshot field must not be null")
	}
	switch rule.Type {
	case "object":
		return validateAuditObject(raw, rule.Fields, depth)
	case "array":
		var elements []json.RawMessage
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &elements) != nil {
			return fmt.Errorf("audit snapshot field must be an array")
		}
		for _, element := range elements {
			if err := validateAuditValue(element, *rule.Items, depth+1); err != nil {
				return err
			}
		}
		return nil
	case "string":
		var value string
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("audit snapshot field must be a string")
		}
		return nil
	case "boolean":
		if bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false")) {
			return nil
		}
	case "integer", "number":
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
			break
		}
		var value json.Number
		if json.Unmarshal(raw, &value) == nil {
			if rule.Type == "number" || !strings.ContainsAny(value.String(), ".eE") {
				return nil
			}
		}
	}
	return fmt.Errorf("audit snapshot field has wrong type")
}
