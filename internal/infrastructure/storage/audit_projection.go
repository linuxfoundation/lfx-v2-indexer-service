// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/types"
)

// auditProjectionScript performs the revision comparison and document update
// atomically on the resource's primary shard. A legacy document without a
// revision is rejected, not silently treated as revision zero. Every legacy
// publisher of an adopted object type must be blocked before audited delivery.
const auditProjectionScript = `
if (ctx.op != 'create' && !ctx._source.containsKey('audit_source_revision') && !params.bootstrap) {
  throw new IllegalStateException('legacy document has no audit source revision');
}
if (ctx.op != 'create' && ctx._source.containsKey('audit_source_revision')) {
  long previous = ((Number)ctx._source.audit_source_revision).longValue();
  if (params.revision < previous) { ctx.op = 'none'; return; }
  if (params.revision == previous) {
    if (params.projection_only && params.identity.equals(ctx._source.audit_event_key) && !params.fingerprint.equals(ctx._source.audit_event_fingerprint)) {
      throw new IllegalStateException('projection identity reused with different audit content');
    }
    if (params.identity.equals(ctx._source.audit_event_key) && params.fingerprint.equals(ctx._source.audit_event_fingerprint)) {
      ctx.op = 'none'; return;
    }
    if (!params.projection_only) {
      if (ctx._source.containsKey('audit_mutation_key')) {
        if (params.identity.equals(ctx._source.audit_mutation_key) && params.fingerprint.equals(ctx._source.audit_mutation_fingerprint)) {
          ctx.op = 'none'; return;
        }
        throw new IllegalStateException('equal source revision has different audit content');
      }
      if (ctx._source.containsKey('audit_projection_order_time')) {
        ctx._source.audit_mutation_key = params.identity;
        ctx._source.audit_mutation_fingerprint = params.fingerprint;
        return;
      }
    } else if (ctx._source.containsKey('audit_projection_order_time')) {
      int byTime = params.order_time.compareTo(ctx._source.audit_projection_order_time);
      if (byTime < 0 || (byTime == 0 && params.identity.compareTo(ctx._source.audit_projection_order_key) <= 0)) {
        ctx.op = 'none'; return;
      }
    }
  }
}
def previousMutationKey = ctx.op == 'create' ? null : ctx._source.audit_mutation_key;
def previousMutationFingerprint = ctx.op == 'create' ? null : ctx._source.audit_mutation_fingerprint;
boolean sameRevision = ctx.op != 'create' && ctx._source.containsKey('audit_source_revision') && params.revision == ((Number)ctx._source.audit_source_revision).longValue();
ctx._source.clear();
ctx._source.putAll(params.document);
ctx._source.audit_source_revision = params.revision;
ctx._source.audit_event_key = params.identity;
ctx._source.audit_event_fingerprint = params.fingerprint;
if (!params.projection_only) {
  ctx._source.audit_mutation_key = params.identity;
  ctx._source.audit_mutation_fingerprint = params.fingerprint;
} else if (sameRevision && previousMutationKey != null) {
  ctx._source.audit_mutation_key = previousMutationKey;
  ctx._source.audit_mutation_fingerprint = previousMutationFingerprint;
}
if (params.projection_only) {
  ctx._source.audit_projection_order_time = params.order_time;
  ctx._source.audit_projection_order_key = params.identity;
}
`

// AuditProjectionRepository applies a revision-guarded current-resource write.
// It is separate from the legacy bulk indexer and is disabled by default.
type AuditProjectionRepository struct {
	client  *opensearch.Client
	index   string
	timeout time.Duration
}

// NewAuditProjectionRepository targets the existing current-resource index.
func NewAuditProjectionRepository(client *opensearch.Client, index string) *AuditProjectionRepository {
	return NewAuditProjectionRepositoryWithTimeout(client, index, 30*time.Second)
}

// NewAuditProjectionRepositoryWithTimeout bounds the complete update request.
func NewAuditProjectionRepositoryWithTimeout(client *opensearch.Client, index string, timeout time.Duration) *AuditProjectionRepository {
	return &AuditProjectionRepository{client: client, index: index, timeout: timeout}
}

// ApplyIfNewer atomically applies newer revisions. Equal divergent mutation
// revisions fail; authorized reindex/backfill/sync projections may refresh an
// equal revision without manufacturing an audit-history mutation. The boolean
// reports body replacement, not a metadata-only mutation identity update.
func (r *AuditProjectionRepository) ApplyIfNewer(ctx context.Context, envelope *types.IndexerMessageEnvelope, objectType, fingerprint string) (*contracts.TransactionBody, bool, error) {
	if r == nil || r.client == nil || r.index == "" || r.index == historyIndex || envelope == nil || envelope.Audit == nil || fingerprint == "" {
		return nil, false, fmt.Errorf("audited projection is not configured")
	}
	body, err := contracts.AuditProjectionBody(envelope, objectType)
	if err != nil {
		return nil, false, err
	}
	projectionOnly := envelope.Audit.Origin != types.AuditOriginMutation
	request := struct {
		ScriptedUpsert bool           `json:"scripted_upsert"`
		Upsert         map[string]any `json:"upsert"`
		Script         struct {
			Source string         `json:"source"`
			Lang   string         `json:"lang"`
			Params map[string]any `json:"params"`
		} `json:"script"`
	}{ScriptedUpsert: true, Upsert: map[string]any{}}
	request.Script.Source = auditProjectionScript
	request.Script.Lang = "painless"
	request.Script.Params = map[string]any{
		"revision":        envelope.Audit.ResourceRevision,
		"identity":        envelope.Audit.Publisher + ":" + envelope.Audit.EventID,
		"fingerprint":     fingerprint,
		"projection_only": projectionOnly,
		"order_time":      envelope.Audit.OccurredAt.UTC().Format("2006-01-02T15:04:05.000000000Z"),
		"bootstrap":       envelope.Audit.Origin == types.AuditOriginBackfill,
		"document":        body,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, false, fmt.Errorf("encode guarded projection: %w", err)
	}
	retries := 8
	requestCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	res, err := (opensearchapi.UpdateRequest{
		// opensearch-go v2.3.0 interpolates the ID directly into the URL.
		// Escape the path segment, not the opaque reference stored in the body.
		Index: r.index, DocumentID: url.PathEscape(body.ObjectRef), Body: bytes.NewReader(encoded),
		RetryOnConflict: &retries, Refresh: refreshValue(ctx),
		SourceIncludes: []string{"audit_event_key", "audit_event_fingerprint", "audit_mutation_key", "audit_mutation_fingerprint"},
	}).Do(requestCtx, r.client)
	if err != nil {
		// Transport errors can echo an opaque resource reference or credentials.
		return nil, false, fmt.Errorf("apply guarded projection: transport failure")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		// Painless errors can echo full document fields. Never expose the body.
		return nil, false, fmt.Errorf("apply guarded projection: OpenSearch status %d", res.StatusCode)
	}
	var result struct {
		Result string `json:"result"`
		Get    struct {
			Source struct {
				Identity            string `json:"audit_event_key"`
				Fingerprint         string `json:"audit_event_fingerprint"`
				MutationIdentity    string `json:"audit_mutation_key"`
				MutationFingerprint string `json:"audit_mutation_fingerprint"`
			} `json:"_source"`
		} `json:"get"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&result); err != nil {
		return nil, false, fmt.Errorf("decode guarded projection result")
	}
	switch result.Result {
	case "created", "updated":
		if result.Get.Source.Identity == "" || result.Get.Source.Fingerprint == "" {
			return nil, false, fmt.Errorf("guarded projection result is missing identity")
		}
		// A same-revision mutation can record its identity without replacing
		// an already refreshed projection. Use the atomically returned source,
		// not a later GET, to avoid publishing the mutation's obsolete body.
		identity := envelope.Audit.Publisher + ":" + envelope.Audit.EventID
		if result.Get.Source.Identity != identity || result.Get.Source.Fingerprint != fingerprint {
			if !projectionOnly && result.Result == "updated" && result.Get.Source.Identity != identity &&
				result.Get.Source.MutationIdentity == identity && result.Get.Source.MutationFingerprint == fingerprint {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("guarded projection result has unexpected identity")
		}
		return body, true, nil
	case "noop":
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("unexpected guarded projection result")
	}
}
