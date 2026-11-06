<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Append-only audit history: implementation design

**Status: opt-in indexer implementation for linuxfoundation/lfx-self-serve#4616;
production rollout is not enabled.** The authoritative wire contract is
[audit-v2-contract.md](audit-v2-contract.md). Runtime admission rejects audited
envelopes **by default**. When explicitly enabled with a reviewed policy, the
consumer follows the history-first, guarded current-write path. No production
publisher policy or history-query API ships with this repository. The
current-resource contract remains
[indexer-contract.md](indexer-contract.md).

The `HistoryRecord` builder and create-only OpenSearch adapter are wired into the
opt-in consumer through `internal/application/audit_workflow.go` and
`internal/container/container.go`. Their implementations are
`internal/domain/contracts/history.go` and
`internal/infrastructure/storage/history_repository.go`. The strict
mapping is [opensearch-resource-history-v1.json](../charts/lfx-v2-indexer-service/files/opensearch-resource-history-v1.json).
The chart's history index hook is opt-in and disabled by default; audited
ingestion is **not enabled**. The adapter verifies duplicates by immutable
payload fingerprint and does not copy transport credentials or raw current-resource
projection data into history; only validated audit snapshots are retained. The
fingerprint covers the source audit metadata, current projection data/tags/config,
resource type and action, excluding transport headers and ingestion time. The
mapping intentionally leaves `audit` unindexed (including user profiles and
snapshots). Query-service must approve the final fields before reads are
enabled. An explicit erasure overwrites the same document ID with only a
permanent `publisher`/`event_id`/`erased`/`erased_at` fence. Create-only replay
cannot resurrect the old payload. The fence is never age-purged. Coordinate
explicit erasure across source outboxes and backups before claiming full erasure.

`internal/domain/services/audit_admission.go` supplies a fail-closed authenticated
publisher/resource/origin/schema policy. `audit_policy_schema.go` loads a
review-approved, closed policy manifest; no production publisher policy or
snapshot registry is bundled. `internal/application/audit_workflow.go`
coordinates history-first writes, guarded projection, and authenticated replay
using injected ports. `internal/infrastructure/storage/audit_projection.go`
implements an atomic OpenSearch scripted update of the current document with
internal revision and identity fields. An authorized `backfill` may bootstrap
an existing legacy document; ordinary mutations fail closed on legacy current
documents lacking a source revision. Same-revision projections may refresh
current search without adding history; old revisions cannot replace newer ones.
Startup requires each current/history/receipt target to resolve to one backing
index. A multi-index current alias is unsafe even with a designated write index,
because the atomic guard cannot inspect copies in the other backing indices.
Alias rollover/retargeting requires pausing audited dispatch, preserving revision
metadata, and revalidating the target before delivery resumes.
The mapping update for existing `resources` indices and the consumer path are
opt-in, disabled by default, and fail startup without the approved registry,
private OpenSearch credentials and adopted resource types. Real-OpenSearch tests
cover concurrent revision/identity conflicts, projection ordering, and
erasure/replay races; their scope and rollout limitations are listed below.
Production publisher registration and restricted-role/staging verification are
still required before audited ingestion can be enabled. Query-service currently
uses explicit `SourceIncludes`, so these unindexed internal fields are not
exposed in its current-resource response.
All accepted origins reserve the same publisher/event identity in the separate
private `resource-audit-deliveries-v1` index before history/current writes;
projection operations write only a minimal operational receipt, never a
fabricated audit-history mutation. Same-revision projections use the immutable
source occurrence time (UTC, fixed nanosecond precision) and event identity as
a deterministic total order. A replay of an older operation cannot replace
a newer applied projection; reusing any event ID with changed content or a
different origin is rejected. The receipt contains no actor or resource payload
and has no age-based expiry. The current write stores only the latest ordering
key atomically with the projection; the receipt survives newer revisions.
The indexer emits a separate structured error when a **handler failure** occurs
on JetStream's last delivery attempt. This does not cover every exhausted
delivery: a final attempt abandoned while waiting for a worker during shutdown
is NAKed without reaching that handler-error log. Operators must configure
independent JetStream exhaustion monitoring and source-outbox reconciliation,
not rely on the presence or absence of that log, and retain the publisher's
durable repair source. None of these components establish an audit history start date.

## Current implementation boundary

- Current resources still use `{object_type}:{object_id}` as their document ID.
  Updates overwrite previous current documents; deletes overwrite with
  tombstones. Authorized audited mutations also retain separate history records;
  projection-only operations reserve delivery identity but do not append history.
- Legacy deletes with a complete `IndexingConfig` preserve parent scope and history
  authorization metadata. They remain non-public and do not gain ordinary access
  fields. UID-only deletes still work, without invented scope or authorization.
  Fully parsed configs with mismatched IDs warn and fall back to a legacy
  tombstone; malformed config shapes or missing required config fields fail
  before writing. Legacy envelope tags and V1 data remain unchanged on tombstones
  and post-indexing events, but are not an approved audit-history payload.
- Audited mutation deletes require complete matching config, scope, and approved before
  snapshots. They never use the legacy mismatch/UID-only fallback. Adopted types
  reject both legacy envelope writes and platform-V1 writes.
- Audited cleanup requests retain a canonical-visibility requirement across
  up to 12 delayed visibility retries. An unrefreshed canonical tombstone cannot
  cause the janitor to declare a lone legacy live copy clean. If the canonical
  document is still absent after that budget, cleanup stops with a warning and
  requires reconciliation rather than searching forever. Legacy cleanup and
  asynchronous current-write refresh remain unchanged.
- Audit storage and mapping verification use a separate uninstrumented
  OpenSearch client, as does the shared janitor when audit is enabled. Ordinary
  indexing retains HTTP tracing. This keeps private document identifiers out of
  auto-generated request-URL span attributes while preserving authentication,
  connection/TLS defaults and timeouts. Exported-span tests exercise the actual
  container wiring with user-linked synthetic IDs, duplicate read-backs, HTTP
  rejection/transport failures and janitor writes; ordinary traced indexing is
  the positive control. Trace isolation is not a substitute for transport
  encryption or deployed index-role isolation.
- The ingestion timestamp and generated processing/log message IDs are not stable
  domain-event identities. Neither may be used for audit deduplication.
- Query-service's current search/count API operates on current resources, not
  audit events. Its owned contract is
  `lfx-v2-query-service/docs/query-service-contract.md`. No query-service change
  is included in this work.

## Version 2 event contract and producer handoff

Use the [version 2 contract](audit-v2-contract.md) rather than a second envelope
definition here. It adds explicit version/identity/time/origin, required source
revision, actor/effective actor refs and denormalized user profiles, before/after
scope, and approved snapshots while
leaving existing messages compatible. It requires complete deletion context.
The previous metadata-only proposal is superseded: metadata alone records that
a change happened, but cannot provide the requested prior resource values.

Mutation content requires an approved publisher/resource-owned snapshot schema;
no production registry is supplied yet. Never copy the current transaction body
or raw resource `Data` wholesale into history. The contract decoder rejects
unknown metadata, duplicate keys, unsafe shapes, and unapproved snapshot content
through the caller's required schema validator. Runtime identity/delegation trust
is a separate required gate, not a property of structural validation.

Attribution explicitly captures name, avatar URL, email, and username for both
user actors. Nullable attributes and a capture-availability marker preserve known
facts without fabricating missing data or depending on an account still existing.
These profile snapshots are immutable event metadata, not arbitrary directory
objects and not the outbox relay's current profile. Retention/erasure and index
permissions must cover them, both actor copies, and all replay/repair sources.
An avatar URL is not an archived image; the indexer must not fetch it.

The committee PostgreSQL migration should commit the mutation and an immutable
outbox event in one database transaction. A relay attaches fresh service
authentication and waits for JetStream persistence acknowledgment; no user JWTs
or raw auth headers belong in the durable outbox/history payload. Retrying an
outbox row preserves its event ID and source revision. An outbox in PostgreSQL
cannot make an authoritative state write still performed in NATS KV atomic;
coverage starts only for mutation paths actually using the transactional handoff.

Multiple approved publishers may address a resource type, but every overlapping
resource identity must share one authoritative revision sequence. Publisher
names do not scope the current-document guard: it compares revisions for
`(object_type, object_id)`. A separate projection publisher must copy the
authoritative source revision, not generate a competing counter. Operators must
approve this coordination or disjoint resource-ID ownership when registering
policies; the indexer cannot infer it from a publisher's name.

## Implemented storage and recovery

The history adapter uses the explicitly provisioned `resource-history-v1` index;
never add history records to `resources`, set `latest` on them, or run the
current-document janitor against them. Its schema is strict (`dynamic: strict`), with
keyword event/resource/scope/auth fields and date fields for occurrence and
ingestion times. Coordinate exact field names and mapping with query-service
before enabling history reads. Actor references and denormalized
profile snapshots are sensitive and require restricted index access. Approved
audit events, including captured profiles and approved snapshots, are retained
indefinitely without time-based expiry; keep personal data out of general
full-text search by default.

Snapshot validation and freezing remain defensive at record construction, even
after admission. This repeats some bounded validation/fingerprinting work on the
at-most-1-MiB envelope. Optimize it only after profiling, preserving immutable
copies and trusted admission rather than introducing an unchecked constructor
that accepts arbitrary caller-supplied fingerprints.

OpenSearch create-only writes use a deterministic document ID derived from
publisher namespace plus event ID. A duplicate reads back the immutable identity
and fingerprint, rather than blindly treating HTTP 409 as success: an identity
reused with different content is an error. The fingerprint excludes ingestion time,
which is assigned on first successful creation and must not change on replay.
Erasure replaces the payload in that same document slot with a permanent minimal
fence. Create-only appends cannot overwrite the fence; a conflicting append
verifies it and returns `HistoryErasedError`. This applies to normal delivery
and history repair, including replay after a completed erasure.
Publishers/reconciliation must not reassign the erased mutation a new identity.
The fence must not retain actor references or erased payloads and must remain
for as long as any original event can be replayed (indefinitely under this
retention policy).

The workflow reserves delivery identity first, appends mutation history, and then
attempts the revision-guarded current write. Projection-only origins skip the
history append. Success/ACK requires each applicable stage to succeed, including
valid guarded no-ops. Recovery behaves as follows:

| State after an attempt | Retry/reconciliation behavior |
| --- | --- |
| Neither write succeeded | Retry the same event identity; create-only storage verifies any existing receipt/history/fence |
| History succeeded, current failed | Verify matching existing history; retry current write |
| Current exists but history is absent (e.g. reconciliation of older partial work) | Check the erasure fence; if permitted, create missing history from the retained original event, not from current search data |
| Both succeeded but ACK was lost | Match existing history; current replay follows the current-resource ordering policy |
| Same identity, different immutable content | Fail; alert for publisher identity misuse; never overwrite history |
| History was explicitly erased | Reject that mutation ID before attempting current state; reconcile current state with a separate authorized projection if needed |

There is no cross-index transaction. An event in history records a publisher's
committed mutation, not proof that its current search projection has caught up.
Two distinct publisher events claiming the same source revision may therefore
both be retained while the divergent current projection is rejected. This is
evidence of inconsistent source claims, not two successful current writes.
Reconcile the authoritative source/publisher defect; a pre-check would race and
must not replace history-first persistence or mutate append-only history into a
search-projection status table.
Do not roll back a successful append if the current projection write fails.
Recovery uses normal authenticated replay through this same workflow; there is
no separate `RepairHistory` method or operator repair endpoint. When current
state is already applied, the guarded no-op restores only the missing history
and emits no replacement-body event.
An in-flight delivery that already verified history before erasure may finish
its current write; erasure is not a cross-index transaction or an in-flight
delivery cancellation mechanism. The permanent fence prevents history payload
resurrection, and deliveries observing it fail before touching current state.

### Downstream post-indexing events

The workflow attempts `lfx.{object_type}.{action}` after an actual audited
current-body replacement. Publication remains best-effort/non-blocking, with an
additive stable `event_id` of `{publisher}:{publisher_event_id}`. Legacy events
retain their existing behavior and omit that field. Neither path promises
exactly-once or durable notification delivery:

| Recovery path | Implemented post-indexing publication behavior |
| --- | --- |
| Neither write succeeded | No publication until the required writes succeed; then attempt publication after the successful current write |
| History succeeded, current failed | Do not publish for the failed current write; attempt publication after a successful retry of the current write |
| Current exists, history absent | Replay the original event through normal processing; if its current projection is a guarded no-op, restore history without publishing. If current state is actually replaced, attempt publication |
| Both succeeded, ACK lost | The guarded current retry is a no-op and sends no new notification; an earlier failed best-effort publication is not repaired by that replay |
| Identity conflict | Fail before the current write; do not publish |
| Older source revision | Retain its admissible history, skip current replacement, and do not publish |
| Mutation arrives after a same-revision refreshed projection | Append history and record mutation identity without replacing the body or publishing an obsolete body |

Subscriber deduplication can use the validated publisher namespace/event ID.
Do not use the existing `document_id` (shared by all mutations of a resource) or
processing timestamp as that identity. Distinguish mutation identity from a projection
rebuild/repair notification so suppression of reindex audit records does not
silently suppress the existing post-indexing notification. If reliable
publication is required, it needs a separate durable handoff.
Application tests cover history-first partial failures, recovery, ACK-loss/late
replays, history restoration through normal replay, projection suppression, erasure admission, and
non-blocking publish failure. Storage integration tests exercise the real
OpenSearch adapters; these are distinct from a full publisher-to-consumer test.

Legacy current-resource writes have no source-revision guard. The separate
audited projection adapter uses an atomic scripted update; adopted resource
types must refuse legacy writes after audited delivery starts or a legacy
overwrite could erase its revision metadata. A source backfill bootstraps
pre-existing documents before their first audited mutation. Real-OpenSearch
tests cover the script under concurrent updates and tombstones; staging still
needs to verify the selected publisher, bootstrap, and deployed roles before
enabling. Retain admissible late mutations in history even if their current
projection is skipped. Do not claim chronological correctness for legacy types.
Mutation, reindex and sync-echo policies must target legacy-fenced required
types. Only a backfill-only policy may prepare an unfenced type, without adding
history coverage. Legacy writes can still remove that preparatory metadata;
rebootstrap after pausing/fencing them before treating that type as adopted.

The existing JetStream retry limit and 24-hour/10-GiB stream retention are not
an indefinite recovery queue. Add durable failed-event monitoring/reconciliation
and define recovery ownership before claiming lossless audit delivery. Publisher
outbox/reconciliation is owned by
[publisher adoption](https://github.com/linuxfoundation/lfx-self-serve/issues/4615);
no event may represent a failed mutation.

## Retention, authorization, and rollout gates

- [Authorization policy](https://github.com/linuxfoundation/lfx-self-serve/issues/4617)
  closed with the existing committee history relation as the read check;
  deleted-committee access fails closed when the committee's tuples are removed.
  Do not guess a project auditor grant. The project-level anchor in tests is
  illustrative, not the selected policy.
- Approved audit records, including profiles and snapshots, have no age-based
  expiry. Provision a private index with restricted permissions and no automatic
  time-based delete policy. Explicit targeted erasure, when required, is an
  exception to append-only ingestion: atomically replace the retained payload
  with a durable minimal erasure fence and coordinate retained copies and backups.
  The fence must cover every redelivery, replay, restore, and reconciliation
  path indefinitely so an erased event cannot recreate its personal data.
  Erased mutation IDs cannot drive current writes, including deletes; if current
  search still needs a tombstone, reconcile it using a separate authenticated
  projection ID rather than reusing the erased mutation or fabricating history.
- The history-index setup hook is disabled by default. Before enabling it,
  provision roles for the indexer's required write/verification/erasure operations
  and separately authorized history-query reads. Ordinary resource readers must
  not have history/receipt access; the chart's generic
  OpenSearch connection does not itself enforce index-level security. Verify
  strict mappings on pre-existing indices; the hook only creates absent indices.
- Deploy mapping and disabled write support first; verify duplicate/conflict and
  partial-write recovery. Enable only approved participating publisher types
  after [publisher adoption](https://github.com/linuxfoundation/lfx-self-serve/issues/4615).
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

## Validation coverage and remaining rollout work

The indexer implementation includes the versioned contract, strict preflight and
authenticated publisher/schema admission, delivery receipts, create-only history,
revision-guarded current writes, erasure fences, legacy coexistence gates, index
mapping verification, and the [rollout/recovery runbook](audit-rollout.md).

`internal/infrastructure/storage/audit_integration_test.go` covers real
OpenSearch history/replay/erasure, source revisions, same-revision arrival
ordering, and reserved-character document identities. The concurrent suites in
`audit_concurrency_integration_test.go` use synchronized worker starts to check:

- Competing source revisions, including newer tombstones and updates newer than
  a deletion: the greatest revision wins while each mutation retains history.
- Identical concurrent deliveries: exactly one history append and current-body
  replacement, with stable first-ingestion time.
- Equal-revision mutation conflicts and changed-content delivery-ID reuse:
  one current winner; no silent identity replacement.
- A mutation racing same-revision projection rebuilds: deterministic projection
  ordering with the mutation identity retained, and no-op replays.
- History creation/redelivery racing erasure: only the minimal permanent fence
  remains after completion, and subsequent replays are rejected.
- A deterministic erasure between an append's HTTP 409 and its verification GET:
  verification observes the fence and refuses the redelivery.

CI runs the suites against an isolated disposable OpenSearch instance and repeats
the concurrent suites three additional times with Go race detection. These
bounded races are regression evidence, not proof of all distributed schedules.
CI disables the OpenSearch security plugin: it does **not** prove deployed role
isolation, production publisher coverage, or end-to-end audit completeness.

`internal/infrastructure/cleanup/audit_cleanup_integration_test.go` also disables
automatic index refresh to hold a canonical tombstone outside search while a
legacy live copy remains visible. It checks audited cleanup schedules a retry,
preserves that request's visibility policy, and retires the legacy copy after an
explicit refresh makes the canonical document searchable.

Before enabling production delivery:

1. Complete [publisher adoption](https://github.com/linuxfoundation/lfx-self-serve/issues/4615),
   approve resource snapshot schemas, and register the actual publisher policy.
2. Provision and verify restricted runtime/provisioning roles, private indices,
   and indefinite retention. Test negative history/receipt access using ordinary
   reader roles. Operator-facing erasure/export tooling is deferred from launch;
   if erasure is exercised later, coordinate source copies and backups as well.
3. Configure exhausted-delivery alerts and durable source-outbox reconciliation
   ownership; the indexer's log and normal replay path are not a durable recovery queue.
4. Complete staging/canary, legacy backfill/bootstrap, fleet coexistence, and
   rollback checks. Record actual per-publisher/resource coverage start dates.
5. Coordinate the owned retained-event/query contract with
   [history queries](https://github.com/linuxfoundation/lfx-self-serve/issues/4618);
   the history endpoint and PCC changes are outside this ticket's implementation.
