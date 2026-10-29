// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package types

import (
	"bytes"
	"encoding/json"
	"slices"
	"unicode/utf8"
)

// encoding/json accepts case-insensitive struct field names and duplicate keys.
// Both are ambiguous for immutable event comparison. Check the versioned wire
// vocabulary exactly. Actor profiles are closed metadata; resource data and
// current-index contact profile maps remain governed by their owned schemas.
var auditJSONFields = map[string][]string{
	"":                                {"schema_version", "audit", "action", "headers", "data", "tags", "indexing_config"},
	"audit":                           {"event_id", "publisher", "occurred_at", "resource_revision", "origin", "attribution", "scope", "content"},
	"audit.attribution":               {"actor_ref", "effective_actor_ref", "actor_profile", "effective_actor_profile"},
	"audit.attribution.actor_profile": {"availability", "name", "avatar_url", "email", "username"},
	"audit.attribution.effective_actor_profile": {"availability", "name", "avatar_url", "email", "username"},
	"audit.scope":                {"before", "after"},
	"audit.content":              {"schema", "representation", "before", "after"},
	"indexing_config":            {"object_id", "public", "access_check_object", "access_check_relation", "history_check_object", "history_check_relation", "sort_name", "name_and_aliases", "parent_refs", "tags", "fulltext", "contacts"},
	"indexing_config.contacts[]": {"lfx_principal", "name", "emails", "bot", "profile"},
}

const auditOpaqueJSONPath = "\x00"

func auditJSONShape(payload []byte) bool {
	if !utf8.Valid(payload) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	return auditJSONValue(decoder, "", 0)
}

func auditJSONValue(decoder *json.Decoder, path string, depth int) bool {
	if depth > 64 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return false
			}
			seen[key] = true
			if allowed, fixed := auditJSONFields[path]; fixed && !slices.Contains(allowed, key) {
				return false
			}
			if !auditJSONValue(decoder, auditJSONChildPath(path, key), depth+1) {
				return false
			}
		}
		if path == "audit.attribution.actor_profile" || path == "audit.attribution.effective_actor_profile" {
			// Explicit nulls distinguish unavailable attributes from a producer
			// forgetting to capture a requested field. Go snapshots emit every key.
			for _, key := range auditJSONFields[path] {
				if !seen[key] {
					return false
				}
			}
		}
		token, err = decoder.Token()
		return err == nil && token == json.Delim('}')
	case json.Delim('['):
		childPath := auditOpaqueJSONPath
		if path == "indexing_config.contacts" {
			childPath = "indexing_config.contacts[]"
		}
		for decoder.More() {
			if !auditJSONValue(decoder, childPath, depth+1) {
				return false
			}
		}
		token, err = decoder.Token()
		return err == nil && token == json.Delim(']')
	default:
		return true
	}
}

func auditJSONChildPath(path, key string) string {
	if _, fixed := auditJSONFields[path]; !fixed {
		// Resource/snapshot/profile-map keys can be arbitrarily large. Do not
		// copy them into every descendant's path; only metadata has vocabulary.
		return auditOpaqueJSONPath
	}
	next := key
	if path != "" {
		next = path + "." + key
	}
	if _, fixed := auditJSONFields[next]; fixed || next == "indexing_config.contacts" {
		return next
	}
	return auditOpaqueJSONPath
}
