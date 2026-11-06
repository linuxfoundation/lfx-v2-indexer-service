<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Audited indexer rollout and recovery (schema version 2)

**Default: disabled.** Do not enable audited publishers until the producer has
adopted the immutable PostgreSQL mutation/outbox handoff (#4615), its approved
snapshot schema has been reviewed, and every indexer replica understands version
2. The history-query endpoint (#4618) has its own authorization rollout.

## Publisher policy

The operator supplies an existing ConfigMap with a `policy.json` key; it is
mounted read-only. Policies must be reviewed together with the publisher-owned
snapshot schema. No publisher is trusted by default. This is a **shape-only
fixture**, not approval to enable a real publisher:

```json
{
  "publishers": [{
    "publisher": "test-publisher",
    "principal": "fixture@clients",
    "can_attest_actors": true,
    "resources": {
      "fixture_resource": {
        "origins": ["mutation", "backfill", "reindex"],
        "schemas": {
          "fixture.audit.v1": {
            "fields": {
              "name": {"type": "string", "required": true},
              "count": {"type": "integer"}
            }
          }
        }
      }
    }
  }]
}
```

Each snapshot is a closed object: unknown fields, wrong types, missing required
fields, and disallowed credential-related field names fail. Objects and arrays
must declare their nested fields/items; `nullable` must be explicit. Snapshot
values may be at most 16 levels below the root object, counting each object
field and array item as one level. Policy loading rejects schemas that describe
values beyond the same runtime limit. The policy
binds a validated service principal to a publisher namespace, resource types,
origins and versioned snapshot schemas. A source must capture and attest both
historical actor references. Each `user:` actor must carry its immutable profile
snapshot or explicit unavailable-profile status; `service:` actors must not carry
profile snapshots. The indexer cannot reconstruct historical attribution at delivery.
Only admit publisher/resource combinations whose source-owned field allowlist
has been approved. Never put tokens, raw headers or user values in this file.

Publisher/event identity is distinct from revision authority. If more than one
publisher can address the same resource identity, all must use one coordinated
authoritative source revision sequence; projection workers copy it unchanged.
Do not register independent revision counters on overlapping resources. Startup
does not infer this coordination. Reindex/sync-echo policies, like mutations,
must target legacy-fenced required types. Only a backfill-only policy may prepare
an unfenced type, without claiming history coverage; legacy writes may erase
that preparatory guard until the type is fenced and rechecked/rebootstrapped.

## Provisioning and rollout

1. Provision the private `resource-history-v1` index and the separate private
   `resource-audit-deliveries-v1` identity-receipts index from their opt-in
   chart hooks. Neither has an age-based purge. Apply the chart's opt-in audited
   projection mapping to the existing current-resource index. Verify actual
   mappings on indices that predate these hooks; an existing history index is
   not automatically remapped by the create-only hook. The indexer refuses
   audited startup if either private index does not retain full `_source` or
   enforce all required field types (including nested scope and erasure-fence
   fields) and strict/unindexed private fields, or if any current-index backing
   mapping lacks the unindexed internal ordering fields or its own `_source`.
   `audit_source_revision` must retain doc values for janitor sorting (omitted
   uses OpenSearch's enabled default); an explicit `doc_values:false` is rejected.
   History, receipts, and current-resource targets must each resolve to exactly
   one concrete index. Multi-index current aliases are rejected even with a
   designated write index: revision guards cannot compare documents across
   backing indices. Do not roll over or retarget aliases during audited delivery;
   pause dispatch, preserve/bootstrap revision metadata, and revalidate the
   target before resuming.
2. Configure OpenSearch roles **outside this chart**. The runtime indexer needs:
   - Create and read access to `resource-history-v1`, including immutable-event
     verification on duplicate delivery; targeted erasure needs its separately
     approved overwrite permission if that primitive is exercised.
   - Create and read access to `resource-audit-deliveries-v1`, including receipt
     read-back for duplicate/identity verification before any history/current write.
   - Current-resource read/write/update access for guarded projections and janitor
     reconciliation, plus mapping metadata reads for startup verification.

   Private history queries require the separately authorized history-query
   service; indexer verification access is not permission to expose history to
   generic/current-resource readers. Those readers must not be granted history
   or delivery-receipt access. Set `opensearch.auth.existingSecret` for the restricted
   indexer runtime role, and `opensearch.auth.provisioningSecret` separately
   for the chart's short-lived mapping Jobs. Helm's auth flag alone does not
   create roles or protect an open cluster.
   Audit mapping checks, history/receipt operations, audited projections, and
   the shared janitor in audit-enabled deployments use a separate HTTP client
   without tracing instrumentation. It preserves the same runtime credentials,
   TLS/connection defaults and timeout settings; this prevents automatic HTTP
   spans from exporting document-ID paths and transport-error details.
   Ordinary indexing retains its existing instrumentation. This is not transport
   encryption: verify HTTPS or an explicitly verified mesh/mTLS exception before rollout,
   because the chart's default OpenSearch URL is HTTP and credentials/audit data
   must be protected in transit.
3. Upgrade all consumers on the shared subjects while `audit.enabled=false`.
   Mixed-version consumers can ACK and discard audit requirements. Rollback
   across that version boundary requires pausing audited publisher dispatch
   and retaining the immutable source outbox for replay.
4. Agree on source revisions and the bootstrap coverage for existing resources.
   Pause **all legacy writes/indexing dispatch for the chosen types**, and verify
   already queued legacy deliveries have drained. Keep audited mutation/outbox
   dispatch paused as well. With approved schemas and issuer/subject permissions
   ready, configure `audit.enabled=true`, a nonempty `audit.requiredTypes`, and
   `audit.policyConfigMap`. The policy must approve mutation and `backfill` for
   each chosen type. Even the first type must be marked required at this stage:
   disabled audit processing cannot consume its backfill, and enabled startup
   requires at least one required type with an approved mutation schema.
   Enable all required private-index/projection-mapping hooks and OpenSearch auth.
   Configure each timeout to cover the larger of its audited and legacy budgets:
   - ACK wait: `max(5 × OPENSEARCH_TIMEOUT + 10s, OPENSEARCH_TIMEOUT + OPENSEARCH_BATCH_MAX_WAIT + 10s)`.
   - Drain timeout: `max(5 × OPENSEARCH_TIMEOUT + 5s, OPENSEARCH_TIMEOUT + OPENSEARCH_BATCH_MAX_WAIT + 5s)`.

   For example, a 1s OpenSearch timeout and 10s batch wait require at least 21s
   ACK wait and 16s drain, not the smaller audited-only bounds. Set the pod
   termination grace period above drain plus shutdown headroom. The chart's
   default 45s/55s/60s timings are deliberately insufficient for the default
   audited OpenSearch request budget.
   Wait until **every indexer replica** has the new configuration and is ready
   before dispatching any backfill; mixed settings can reject those deliveries.
5. While legacy and audited mutation dispatch remain paused, send authorized
   source-owned `backfill` events to initialize revision metadata without
   creating history. Ordinary audited mutations refuse legacy documents without
   this metadata. Resolve all bootstrap/metadata gaps and reconcile pre-existing
   `latest:true` duplicates before starting mutation delivery. Audited cleanup
   allows up to 12 delayed visibility retries for the guarded canonical document,
   then warns and stops if it is still not searchable. On visibility, cleanup
   prefers the guarded document over older legacy live documents or tombstones.
   Investigate/reconcile an exhausted visibility budget rather than relying on
   further automatic retries. This queue remains best-effort
   with overflow monitoring; it is not a replacement for adoption reconciliation.
6. Enable audited mutation dispatch for a canary, then resume supported source
   mutations only through their audited/outbox paths. The indexer refuses legacy
   mutations on `audit.requiredTypes`; unrelated legacy publishers continue
   unchanged. Record the **actual** coverage start date and any gaps for each
   resource type/publisher. No earlier revisions can be reconstructed from
   current search documents alone.

Operator-facing audit erasure/export entry points are deferred from the V2
launch scope; they are not prerequisites for this rollout. The internal erasure
adapter and replay-fence protections remain implemented and tested. This scope
decision does not constitute a privacy/legal sign-off for indefinite retention.

The internal ordering fields on current documents are unindexed and omitted
from query-service's explicit `SourceIncludes`. They do not grant history
access. For equal source revisions, an identical mutation is a no-op; a
different mutation is rejected. Authorized projection-only events may refresh
current state without appending history. A late mutation is kept in history
but cannot replace a newer or same-revision refreshed current projection. At
the same revision, it records/checks the mutation identity without replacing
the body or sending a replacement-body notification. Test these cases on a real
OpenSearch cluster before turning on publisher delivery.

## Monitoring, reconciliation, and erasure

- Alert on the indexer's `JetStream message exhausted delivery attempts —
  durable reconciliation required` log, grouped by stream, sequence, and
  subject. It is emitted only for handler errors at the final attempt, **not**
  every form of delivery exhaustion. A final delivery NAKed while waiting for
  a worker during shutdown can exhaust without that log. Configure independent
  JetStream exhaustion monitoring and periodic source-outbox reconciliation
  so these cases are detected even when no handler-error alert fires.
  The log is a detection signal, **not** a durable repair queue. The stream
  expires after 24h or 10 GiB; the publisher's PostgreSQL outbox must
  remain the original credential-free repair source, preserving event ID,
  source revision, occurrence time and approved snapshots. A broker PubAck
  alone is not proof of successful history/current writes.
- Alert on janitor warnings containing `visibility retries exhausted;
  reconciliation required`. They mean the canonical document was still missing
  from search after 12 delayed retries; inspect missing revision metadata,
  `latest:false` canonical copies, or a removed/rebuilt document and reconcile
  from the authoritative source. No further visibility retry is scheduled by
  that request.
- Reconcile against source outbox events, not current OpenSearch documents.
  Re-deliver the same immutable event with **fresh service authentication**;
  the indexer first creates/verifies history and then conditionally writes
  current state. This normal replay path also restores missing history when
  current state already exists; a guarded current no-op emits no new
  notification. No separate history-repair operator API is provided. Do not invent a fresh ID or
  actor profile on replay.
- Terminal-error classification is a separate shared-consumer follow-up. The
  current consumer NAKs all handler errors up to its five-delivery limit,
  including deterministic admission, erasure-fence and identity/revision
  conflicts. Inspect the error category and reconcile configuration/source
  defects rather than blindly redelivering those permanent rejections. An
  exhausted-delivery signal is not proof of a transient outage or missing history.
- Approved audit data, including captured personal fields, has no age-based
  expiry. A targeted erasure overwrites the single publisher/event document
  with a permanent minimal fence. If this primitive is exercised, coordinate
  source outbox copies and backups;
  a fence in OpenSearch alone does not erase those copies. No age-based job
  purges the index or the fence. Deleted-committee history access fails closed
  when its committee FGA tuples are gone; public-resource grants never grant
  history access.
