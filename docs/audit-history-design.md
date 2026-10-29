<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Append-only audit history: implementation design

**Status: work in progress for linuxfoundation/lfx-self-serve#4616.** This is a
proposal, not an available publisher or query API. The authoritative implemented
contract remains [indexer-contract.md](indexer-contract.md). No history-write
support or new envelope fields have been implemented yet.

## Current implementation boundary

- Current resources still use `{object_type}:{object_id}` as their document ID.
  Updates overwrite previous revisions; deletes overwrite with tombstones.
- Deletes with a complete `IndexingConfig` now preserve parent scope and history
  authorization metadata. They remain non-public and do not gain ordinary access
  fields. UID-only deletes still work, without invented scope or authorization.
  Fully parsed configs with mismatched IDs warn and fall back to a legacy
  tombstone; malformed config shapes or missing required config fields fail
  before writing. Legacy envelope tags and V1 data remain unchanged on tombstones
  and post-indexing events, but are not an approved audit-history payload.
- The ingestion timestamp and generated processing/log message IDs are not stable
  domain-event identities. Neither may be used for audit deduplication.
- Query-service's current search/count API operates on current resources, not
  audit events. Its owned contract is
  `lfx-v2-query-service/docs/query-service-contract.md`. No query-service change
  is included in this work.

## Proposed event contract

Add an optional, explicitly versioned audit block to the V2 envelope. Keep the
existing action, headers, data, and indexing config compatible. Legacy envelopes
continue indexing current resources but cannot establish audit completeness.

The audit block should carry:

- Schema version; immutable publisher namespace and event ID, assigned once per
  committed domain mutation and reused across publish retries, redelivery,
  replay, and storage retries. Prefer an outbox identity, not a request ID.
- Source occurrence time, independent of the indexer's ingestion time; optional
  publisher resource revision for source ordering. Equal occurrence times need
  an event-ID tie-breaker, not an invented total mutation order.
- Operation origin: domain mutation, reindex/backfill, or sync echo. Only domain
  mutations append history; a legitimate system-driven mutation is a mutation
  with a system actor, not automatically a sync echo. Derive origin from a trusted
  ingress/workflow or accept it only from an authenticated producer authorized
  for that resource type and origin. Ordinary request data cannot select audit
  suppression. Validate this trust boundary before writing or suppressing history;
  reject unknown or unauthorized classifications rather than silently skipping
  a mutation. Producer identity must not be trusted merely because it appears in
  a payload field; bind it to transport/service authentication.
- Resource identity matching the subject and existing data/config identity.
  Action comes from the existing envelope's validated action constant.
- Explicit project/committee scope and history object/relation captured at
  mutation time, including on deletion. Do not recover authoritative deletion
  context from the current search document.
- Approved effective actor and delegation attribution, validated against the
  authenticated publisher/request context. Use an approved opaque principal
  reference, not a display name/email copied from headers. Do not trust an
  arbitrary caller-supplied actor or infer a system actor when auth is missing.

The initial safe representation is **metadata-only**: event identity, action,
times, resource, scope, and approved attribution/authorization. Snapshot values
and field-change values require explicit per-resource allowlists before they can
be added; neither arbitrary maps nor a sensitive-key denylist is sufficient.
Confirm that this representation meets the initial history-query requirement
with [publisher adoption](https://github.com/linuxfoundation/lfx-self-serve/issues/4615)
and [history queries](https://github.com/linuxfoundation/lfx-self-serve/issues/4618).
Never serialize the existing transaction body, resource `Data`,
contacts/profiles, auth headers, bearer tokens, secrets, or arbitrary request
bodies into history. Unknown audit fields should be rejected, not silently
retained or accepted and dropped.

## Proposed storage and recovery

Use a separate, explicitly provisioned `resource-history-v1` index; never add
history records to `resources`, set `latest` on them, or run the current-document
janitor against them. The history schema must be strict (`dynamic: strict`), with
keyword event/resource/scope/auth fields and date fields for occurrence and
ingestion times. Coordinate exact field names and mapping with query-service
before implementing a consumer-facing schema. Actor references are sensitive
even when opaque and require restricted index access.

Use OpenSearch create-only writes with a deterministic document ID derived from
publisher namespace plus event ID. A duplicate must compare the immutable
semantic payload, not blindly treat every HTTP 409 as success: an identity reused
with different content is an error. The comparison excludes ingestion time,
which is assigned on first successful creation and must not change on replay.
Before any create or reconciliation, enforce retention admission based on the
original occurrence time and the policy-approved retention horizon, not a fresh
ingestion time. Consult a durable erasure fence keyed by publisher namespace and
event ID: a missing record can mean deliberate erasure, not a failed write.
Publishers/reconciliation must not reassign the erased mutation a new identity.
The fence must not retain actor references or erased payloads and needs its own
approved minimal schema and lifetime, covering every permitted replay source.

Prefer history-first writes, then the existing current-document write. Return
success/ACK only after both required writes succeed. Recovery must cover:

| State after an attempt | Retry/reconciliation behavior |
| --- | --- |
| Neither write succeeded | Check retention/erasure admission, then retry the same event identity |
| History succeeded, current failed | Verify matching existing history; retry current write |
| Current exists but history is absent (e.g. reconciliation of older partial work) | Check retention/erasure admission; if permitted, create missing history from the retained original event, not from current search data |
| Both succeeded but ACK was lost | Match existing history; current replay follows the current-resource ordering policy |
| Same identity, different immutable content | Fail; alert for publisher identity misuse; never overwrite history |

There is no cross-index transaction. An event in history records a publisher's
committed mutation, not proof that its current search projection has caught up.
Do not roll back a successful append if the current projection write fails.

### Downstream post-indexing events

The current implemented contract emits `lfx.{object_type}.{action}` after each
successful current-document write. Publication is best-effort/non-blocking; it
has no stable audit-event ID and is not an exactly-once mutation notification.
This proposal must not silently change that existing subscriber contract:

| Recovery path | Proposed post-indexing publication behavior |
| --- | --- |
| Neither write succeeded | No publication until the required writes succeed; then attempt publication after the successful current write |
| History succeeded, current failed | Do not publish for the failed current write; attempt publication after a successful retry of the current write |
| Current exists, history absent | A history-only repair does not publish a new current-resource event; if reconciliation rewrites current state, attempt publication after that successful write |
| Both succeeded, ACK lost | Repeating a current write repeats the publication attempt; a duplicate history record alone does not suppress it |
| Identity conflict | Fail before the current write; do not publish |

For audit-participating mutations, select an additive stable downstream identity
derived from the validated publisher namespace/event ID before shipping dual
writes, and coordinate subscriber deduplication against it. Do not use the
existing `document_id` (shared by all mutations of a resource) or processing
timestamp as that identity. Distinguish mutation identity from a projection
rebuild/repair notification so suppression of reindex audit records does not
silently suppress the existing post-indexing notification. Legacy messages retain
their existing best-effort behavior; no new delivery/exactly-once guarantee is
claimed. If reliable publication is required, it needs a separate durable handoff.
Add dual-write integration cases for both partial-write paths, ACK-loss replay,
history-only repair, and non-blocking publish failure when that code is built;
this slice has no dual-write implementation to exercise yet.

Current-resource writes currently have no source-revision guard. Replaying a
late/old event can overwrite newer search state; do not claim chronological
current-state correctness. Before dual writes ship, select and test an ordering
guard or a reconciliation workflow that restores current state from the owning
resource without fabricating a domain mutation. Retain all authorized late
mutation events in history regardless of their arrival order.

The existing JetStream retry limit and 24-hour/10-GiB stream retention are not
an indefinite recovery queue. Add durable failed-event monitoring/reconciliation
and define recovery ownership before claiming lossless audit delivery. Publisher
outbox/reconciliation is owned by
[publisher adoption](https://github.com/linuxfoundation/lfx-self-serve/issues/4615);
no event may represent a failed mutation.

## Retention, authorization, and rollout gates

- [Authorization policy](https://github.com/linuxfoundation/lfx-self-serve/issues/4617)
  owns the durable deleted-resource authorization anchor, current-grant
  revocation semantics, permitted personal fields, retention duration, and purge
  obligations. Do not guess a project auditor grant or arbitrary retention TTL.
  The project-level anchor in tests is illustrative, not the selected policy.
- Provision a bounded retention/purge mechanism and required index permissions
  before enabling writes. Append-only applies to normal ingestion, not an
  exemption from retention expiry or legally required erasure. Coordinate backup
  retention too; never enable a new indefinite personal-data store by default.
  Retention admission and the durable erasure fence must protect every redelivery,
  replay, restore, and reconciliation path before writes are enabled. Expired or
  erased events must not recreate history or actor references. Coordinate fence
  retention with replay/backup horizons; do not drop the fence while an older
  copy of the event can still be replayed. A history-erasure decision must not
  accidentally block deletion of the current search projection.
- Deploy mapping and disabled write support first; verify duplicate/conflict and
  partial-write recovery. Enable only approved participating publisher types
  after [authorization policy](https://github.com/linuxfoundation/lfx-self-serve/issues/4617)
  and [publisher adoption](https://github.com/linuxfoundation/lfx-self-serve/issues/4615).
  Then [history queries](https://github.com/linuxfoundation/lfx-self-serve/issues/4618) can expose an
  independently authorized, private/non-shared-cacheable history endpoint.
- History reads must deny anonymous access and missing history metadata, and
  must not inherit the public-resource bypass or actor identity as a read grant.
- Record the actual history start time per publisher/resource type on enablement,
  together with coverage gaps. It is not the deployment date of this draft.
  Previously overwritten revisions cannot be reconstructed from current search.
- Rollback must preserve the separate index. Do not silently downgrade an
  audit-participating consumer to a version that ignores audit fields and ACKs
  away events; pause delivery or retain them durably for replay first. Rebuild
  current search separately with reindex/backfill origin and no new audit change.

## Remaining implementation/test checkpoints

1. Finalize the versioned public types, policy-approved metadata, strict parsing,
   trusted origin classification, and publisher/query handoff; reject incomplete
   audit-bearing deletes. The legacy mismatch fallback is not the proposed audit
   envelope's completeness guarantee.
2. Implement/configure create-only storage, duplicate payload verification, and
   retry-aware dual writes without changing legacy current-resource writes.
3. Add lifecycle tests (create, multiple updates, delete), replay and both
   partial-write states, identity conflicts, timestamps/late ordering, suppressed
   reindex/sync events, downstream publication behavior, retention/erasure replay
   fencing, and prohibited-field exclusion.
4. Ship strict mapping, bounded retention/purge, monitoring/reconciliation, and
   concrete rollout/rollback instructions; validate against real OpenSearch.
