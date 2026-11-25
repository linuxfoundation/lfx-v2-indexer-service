<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Indexer Event Contract (Authoritative)

This is the **owner document** for the indexer event envelope, the OpenSearch document
shape, and the rules the indexer enforces on incoming messages. Other services link
here rather than copy.

The indexer consumes the platform V2 indexing subject (default `lfx.index.>`,
overridable via `nats.indexingSubject`) from a durable JetStream consumer
(`index-events` stream), writes documents into
OpenSearch, and emits domain events on `lfx.{object_type}.{action}`. It is fully
generic; resource services tell it everything it needs via the message payload.
It also answers core-NATS requests on `lfx.indexer.index.{object_type}`, which
apply an index write and an FGA operation together and reply only once both have
landed; see [Request/reply delivery](#requestreply-delivery-lfxindexerindexobject_type).

## Envelope versions and availability

Existing messages omit both `schema_version` and `audit` and retain their
current-resource behavior. The **platform V2** subject name is not an envelope
schema version. The new **audit envelope version 2** is specified in
[audit-v2-contract.md](audit-v2-contract.md), with public types, construction
helpers, and preflight validators. Runtime processing is **disabled by default**.
It is rejected before indexing/authentication side effects until explicitly
enabled with a trusted publisher/schema policy and private history index; see
[rollout requirements](audit-rollout.md).
The versioned attribution contract accepts actor refs and optional JWT evidence,
never publisher profiles, retained identity or verification flags. The indexer
verifies evidence, strips credentials and retains only username/email on first
append. Name/avatar are a query-service live-enrichment responsibility. Retained
identity is private history metadata, not current-resource document data. See
the audit contract for coordinated read-side rollout and outstanding cleanup.

Historical audit ingestion uses the separately policy-authorized
`audit.origin: "historical_import"`. It appends/verifies approved history only;
it never writes current resources/tombstones, advances their revision guards,
schedules cleanup or emits resource-change events. It is not `backfill`, which
remains projection-only. Existing policies do not grant the new origin. No
envelope/config fields or OpenSearch mapping fields are added. See
[historical imports](audit-v2-contract.md#historical-imports) for the contract and
[operational prerequisites](audit-rollout.md#historical-imports).

Unresolved historical actors may use `legacy:<source>/<opaque-id>` instead of
pretending a legacy ID is a current LFX username. This actor kind is restricted to
`historical_import`, forbids actor JWT evidence, and retains null identity fields
with false verification and no directory lookup. Known users keep the existing
current-profile enrichment; M2M clients remain service actors. This is additive
actor-reference vocabulary, not a new envelope/config or mapping field. Upgrade
history readers for the new kind before dispatch; see
[historical attribution mapping](audit-v2-contract.md#historical-attribution-mapping).

`schema_version` and `audit` are reserved, lowercase discriminator fields. A
present audit block without a version (including null), unsupported/invalid
versions, and unsupported subjects are rejected rather than
silently treated as legacy. This protects only consumers containing this guard;
do not publish audited messages to older replicas during a rolling deployment.
Before deploying the new reservation guard, complete the source/staging checks
in [publisher-compatibility.md](publisher-compatibility.md); known local publishers
have no declared collision, but deployed and external traffic is not proven here.

## Subject Conventions

Publish a V2 index message on `lfx.index.{resource_type}`. Use lowercase, underscore-
separated resource types. The subject suffix becomes `object_type` in the indexed
document. Do not invent ad-hoc variants for the same logical type:

```text
lfx.index.committee
lfx.index.committee_member
lfx.index.vote
lfx.index.survey
lfx.index.project_settings
```

Only past-tense actions (`created`/`updated`/`deleted`) are accepted. Use the
constants in `pkg/constants/` rather than string literals. Unversioned current
messages remain supported; retiring the old platform protocol does not require
audit-envelope adoption. Legacy-backed resource types such as `v1_meeting` still
publish on `lfx.index.v1_meeting` and are unaffected.

## Envelope: `IndexerMessageEnvelope`

Defined in the public `pkg/types/indexing_config.go` package (imported by
`internal/domain/contracts/transaction.go`).

```go
type IndexerMessageEnvelope struct {
    SchemaVersion  int                     `json:"schema_version,omitempty"` // omitted for existing traffic
    Audit          *AuditEvent             `json:"audit,omitempty"`          // version 2 contract; processing disabled
    Action         constants.MessageAction `json:"action"`           // "created", "updated", "deleted"
    Headers        map[string]string       `json:"headers"`          // auth headers from request context
    Data           any                     `json:"data"`             // full resource struct; bare UID string for deletes
    Tags           []string                `json:"tags,omitempty"`
    IndexingConfig *IndexingConfig         `json:"indexing_config,omitempty"`
}
```

Field rules the indexer enforces:

- `Action` must be one of the action constants. Unknown actions are rejected.
- `Headers` should carry lower-case JSON keys `authorization` and `x-on-behalf-of`
  from the request context. They become the audit principal in the OpenSearch document.
- `Data` is stored as-is in the `data` OpenSearch field (`flat_object`, schema-free).
- **Deletes**: set `action = "deleted"` and `Data` = the plain UID string. Do not send
  the full resource on a delete.

Always use action constants. Never hardcode strings:

```go
constants.ActionCreated  // "created"
constants.ActionUpdated  // "updated"
constants.ActionDeleted  // "deleted"
```

## Required: `IndexingConfig`

`IndexingConfig` is how a publisher tells the indexer how to build a well-structured
OpenSearch document. **All publishers must include it on every create/update.**
Deletes do not need it because the delete payload is only the object ID. Create/update
messages without it are rejected by the indexer (`indexing_config is required`).

Deletes may include a complete `IndexingConfig` captured by the publisher before
the resource is removed. Its `ObjectID` must match the UID in `Data` to retain
context. A mismatch logs a structured warning and discards the config context,
but still writes the UID-based legacy tombstone so deleted live data is not left
searchable. The delete path retains only `ParentRefs`,
`HistoryCheckObject`, `HistoryCheckRelation`, and the derived `history_check_query`
from this config. It does **not** copy `Public`, ordinary access-check fields,
search fields, contacts, or resource data onto the tombstone. This preserves the
existing non-public, ordinary-access-denied tombstone behavior. Required-field
validation is unchanged: all five required fields below must be supplied with
concrete values.
Explicit `null` is treated as absent; present non-null config values that are
not JSON objects are rejected before writes for all actions, including deletes.
Malformed or incomplete configs still reject the delete and can leave the prior
live document indexed until the publisher corrects and republishes the message;
the UID-only fallback applies only to a fully parsed config with a mismatched ID.

Existing top-level envelope `Tags` still flow into tombstones and post-indexing
events. The exclusions above apply to fields copied from `IndexingConfig`, not
a new payload-scrubbing policy. This is not permission to copy arbitrary payloads
into the separate audit-history store. The retired raw platform-record payload
is no longer represented in transactions, emitted events or new mapping templates;
existing mapped fields may remain unused without an index rebuild.

UID-only deletes remain compatible, but have no history authorization or scope
metadata. These fields do not by themselves preserve revisions or establish
working authorization after FGA tuples are deleted. Append-only storage and the
deleted-resource access policy remain separate work; see the
[audit-history implementation design](audit-history-design.md).

```go
type IndexingConfig struct {
    // Required: identifies the resource and how to check access
    ObjectID             string `json:"object_id"`              // resource UUID
    AccessCheckObject    string `json:"access_check_object"`    // e.g. "vote:abc-123"
    AccessCheckRelation  string `json:"access_check_relation"`  // e.g. "viewer"
    HistoryCheckObject   string `json:"history_check_object"`   // e.g. "vote:abc-123"
    HistoryCheckRelation string `json:"history_check_relation"` // e.g. "auditor"

    // Search and discovery fields
    Public         *bool         `json:"public,omitempty"`           // true = skip auth check for anonymous
    SortName       string        `json:"sort_name,omitempty"`        // sortable name
    NameAndAliases []string      `json:"name_and_aliases,omitempty"` // typeahead / search-as-you-type
    ParentRefs     []string      `json:"parent_refs,omitempty"`      // e.g. ["project:xyz", "committee:abc"]
    Tags           []string      `json:"tags,omitempty"`             // exact-match filtering
    Fulltext       string        `json:"fulltext,omitempty"`         // free-text search blob
    Contacts       []ContactBody `json:"contacts,omitempty"`
}
```

### What the indexer rejects

> **Note on retry behaviour:** Under the JetStream consumer every non-nil handler
> error results in a NAK with exponential-backoff jitter. This includes the
> validation conditions listed below. The consumer is configured with
> `MaxDeliver: 5` (1 initial delivery + up to 4 redeliveries); the four NAK
> intervals (≤1s, ≤2s, ≤4s, ≤8s) give a retry window of at most ~15 seconds.
> After 5 delivery attempts JetStream stops redelivering to this consumer; the
> message remains in the stream until `maxAge` (24 h) or `maxBytes` (10 GiB)
> evicts it. There is currently no terminal-ACK path that skips retries for
> malformed messages — improvement tracked as a follow-up.

| Condition | Outcome |
|---|---|
| Missing `IndexingConfig` on create/update | Message NAKed; up to 5 delivery attempts (~15 s total), then JetStream stops redelivering |
| Empty `ObjectID` | NAKed and retried (no primary key) |
| Empty `AccessCheckObject` or `AccessCheckRelation` | NAKed and retried during `IndexingConfig` parsing |
| Empty `HistoryCheckObject` or `HistoryCheckRelation` | NAKed and retried during `IndexingConfig` parsing |
| `{{` in `ObjectID`, `AccessCheck*`, `HistoryCheck*`, or any `ParentRefs` entry (template references are not supported) | NAKed and retried during `IndexingConfig` parsing. Every other field is stored literally, including `{{ ... }}` text. See [literal values](client-guide.md#literal-values-in-indexing_config) |
| Unknown `action` value | NAKed and retried (`unknown action`) |
| Missing lower-case `authorization` header on V2 messages | NAKed and retried during header validation |
| Invalid, expired, or unverifiable authorization JWT | NAKed and retried before any current-document write or event |
| `data` not present on create/update | NAKed and retried |
| `data` not a string on delete | NAKed and retried |
| Subject suffix empty (`lfx.index.` with no type) | NAKed and retried (must have a non-empty resource type) |
| Subject suffix contains `.`, `*`, `>`, whitespace, or equals `index` | NAKed and retried (invalid or reserved object type) |

**Request/reply (synchronous) is not supported.** The indexer now consumes
messages from a JetStream durable stream. Callers that use `conn.Request()` on a
JetStream-captured subject receive a `PubAck` JSON persistence acknowledgment from
the NATS server — **not** an application-level processing result from the indexer.
The message is stored in the stream and processed asynchronously; there is no way
to determine whether indexing succeeded from the publish call. JetStream also
overwrites the NATS `Reply` field with its internal ACK address (`$JS.ACK...`), so
the original publisher reply inbox is not reachable from the consumer.
Existing publishers may continue `conn.Publish()` (fire-and-forget). A reliable
outbox relay should instead use the JetStream publishing API and wait for its
`PubAck` before recording delivery; that acknowledgment confirms broker storage,
not successful indexing. Do not use core request/reply as an application result.
Consumer delivery guarantees are provided by the JetStream stream: messages that fail processing are NAKed with
exponential backoff with `MaxDeliver: 5` (1 initial delivery + up to 4 redeliveries;
four NAK intervals: ≤1s, ≤2s, ≤4s, ≤8s; total retry window ~15 s). After 5 delivery
attempts JetStream stops redelivering to this consumer; the message remains in the
stream until the age limit (24 h) or size limit (10 GiB) evicts it.

### Choosing search fields

| Field | When to populate |
|---|---|
| `NameAndAliases` | Primary name/title users search for (typeahead) |
| `Tags` | Values used for exact filtering (e.g. `project_uid:abc`, `status:active`) |
| `Fulltext` | Any text content users might search within |
| `ParentRefs` | Parent resource refs (always include if the resource belongs to a project) |
| `data` only | Fields that are display-only and never searched |

### Full example (voting-service pattern)

```go
public := vote.Public
indexingConfig := &indexerTypes.IndexingConfig{
    ObjectID:             vote.UID,
    AccessCheckObject:    fmt.Sprintf("vote:%s", vote.UID),
    AccessCheckRelation:  "viewer",
    HistoryCheckObject:   fmt.Sprintf("vote:%s", vote.UID),
    HistoryCheckRelation: "auditor",
    SortName:             vote.Name,
    NameAndAliases:       []string{vote.Name},
    ParentRefs:           []string{fmt.Sprintf("project:%s", vote.ProjectUID)},
    Tags:                 vote.Tags(),
    Fulltext:             fmt.Sprintf("%s %s", vote.Name, vote.Description),
    Public:               &public,
}

msg := indexerTypes.IndexerMessageEnvelope{
    Action:         constants.ActionCreated,
    Headers:        headersFromContext(ctx),
    Data:           vote,
    IndexingConfig: indexingConfig,
}
```

## OpenSearch Document Shape (the indexer writes)

The indexer is the only writer of the document. It writes using `object_ref` as the
OpenSearch document ID. Field naming below is fixed; do not introduce new top-level
fields without coordination with this service.
Audited current writes URL-escape the document-ID path segment so reserved
characters in an opaque resource ID cannot alter routing or alias another
document. The stored `object_ref` and resource ID remain unchanged.

| Field | Populated from | Purpose |
|---|---|---|
| `object_ref` | `{type}:{id}` | Primary identifier (e.g. `committee:abc-123`) |
| `object_type` | NATS subject suffix | Filtering queries by resource type |
| `object_id` | `IndexingConfig.ObjectID` | UUID lookup |
| `parent_refs` | `IndexingConfig.ParentRefs` | Hierarchy navigation |
| `sort_name` | `IndexingConfig.SortName` | Sorting results |
| `name_and_aliases` | `IndexingConfig.NameAndAliases` | Typeahead search field |
| `tags` | top-level envelope `Tags` plus `IndexingConfig.Tags` | Exact-match filtering |
| `fulltext` | `IndexingConfig.Fulltext` | Free-text search blob |
| `contacts` | `IndexingConfig.Contacts` | Contact search/display metadata |
| `public` | `IndexingConfig.Public` | Anonymous queries filter on this; skips FGA check |
| `access_check_object` | `IndexingConfig.AccessCheckObject` | Compatibility field; prefer `access_check_query` |
| `access_check_relation` | `IndexingConfig.AccessCheckRelation` | Compatibility field; prefer `access_check_query` |
| `history_check_object` | `IndexingConfig.HistoryCheckObject` | Compatibility field; prefer `history_check_query` |
| `history_check_relation` | `IndexingConfig.HistoryCheckRelation` | Compatibility field; prefer `history_check_query` |
| `access_check_query` | `{AccessCheckObject}#{AccessCheckRelation}` | Used by query-service to build FGA check |
| `history_check_query` | `{HistoryCheckObject}#{HistoryCheckRelation}` | History authorization metadata; no history query endpoint is implemented yet |
| `latest` | Set by indexer | `true` for current version only |
| `audit_source_revision`, `audit_event_key`, `audit_event_fingerprint` | Audited current write only (disabled by default) | Internal atomic source-revision ordering and retry identity; unindexed, not returned by query-service's explicit source includes |
| `audit_mutation_key`, `audit_mutation_fingerprint` | Audited current write only (disabled by default) | Preserve mutation identity across same-revision projection rebuilds; unindexed |
| `audit_projection_order_time`, `audit_projection_order_key` | Audited current write only (disabled by default) | Stable same-revision ordering for authorized reindex/backfill/sync projections; unindexed |
| `created_at`, `updated_at`, `deleted_at` | server timestamp by action (legacy); `audit.occurred_at` (UTC) for audited writes | Source-action timing for audited resources, not indexer ingestion time |
| `data` | `IndexerMessageEnvelope.Data` on create/update | Full resource data (schema-free `flat_object`) |

Current documents and post-index event bodies no longer contain the nine top-level
`created_by`, `updated_by`, `deleted_by`, `*_by_principals`, or `*_by_emails`
attribution fields. Both unversioned and audited projections retain their action
timestamps. JWT authentication remains required; the unversioned path validates
the lower-case `authorization` credential directly, without calling
`ParsePrincipals` or collecting `x-on-behalf-of` identities. Audit actors,
username/email snapshots, and verification flags remain confined to private
history and retain their separate verification policy. Resource-owned keys inside
`data` and publisher-supplied contact/search fields are unchanged; this is not a
general payload-scrubbing policy.

New resources-index mappings omit these attribution fields. Existing mappings
may retain unused entries, and documents not yet replaced may retain old values.
No index rebuild or live field deletion is required for this cleanup; the next
current-body replacement omits them. Audit history is not migrated or rewritten.

### Field-naming rules

- All top-level field names are `snake_case`. No camelCase, no dashes, no spaces.
- Reserve `object_*`, `access_check_*`, `history_check_*`, `parent_refs`, `latest`,
  `sort_name`, `name_and_aliases`, `public`, `data`, `tags`, `fulltext`,
  `contacts`, and the audit fields (`created_*`, `updated_*`, `deleted_*`) for this
  contract. Resource-specific data lives inside `data`.
- Anything in `data` is dynamic. OpenSearch maps new keys via `flat_object`, but
  you cannot rely on per-field analyzers there. Use the search-shaped fields above
  when you need analysis or aggregation.

### Current-document and janitor behavior

The current write path uses the OpenSearch Bulk API, with `object_ref` as the document ID
of each item. Concurrent single-document writes from multiple NATS handlers are coalesced
into one bulk request by `internal/infrastructure/storage.BatchIndexer`: a batch flushes as
soon as either `OpenSearch.BatchMaxSize` operations have queued or `OpenSearch.BatchMaxWait`
has elapsed since the first operation in the batch queued, whichever comes first. Each
caller still only sees its own item's result from the bulk response — one document's failure
in a batch does not fail the others sharing that request. A flush is detached from any single
caller's request context (it may be batching operations from several callers at once), so it
runs under its own bounded timeout (`OpenSearch.Timeout`) instead of inheriting a deadline
from one of them; `NATS.AckWait`'s derived default and validation both account for a message
being queued up to `BatchMaxWait` before its batch's flush even starts, on top of that flush
timeout (see `internal/infrastructure/config/app_config.go`). None of this changes the
per-document write shape: each create, update, or delete still writes the current document
for that object reference and sets `latest: true`; deletes still write a tombstone-shaped
document with `deleted_at` and no top-level attribution. The janitor still scans for duplicate
`latest: true` hits with the same `object_ref` and flips older duplicates to `latest: false`
using optimistic update parameters. Queries must filter `latest: true` to see current data.
For audited current writes, the janitor is queued even after a guarded no-op.
Audited cleanup preserves a canonical-visibility requirement across up to 12 delayed
visibility retries: if the guarded canonical document is not yet searchable, it does not
declare a single visible legacy copy clean or choose a legacy winner. Once
older `latest:true` duplicates and the canonical document are visible, it prefers
the canonical revision-guarded document over legacy tombstones. The private history and
delivery-receipt indices never enter the janitor queue.
If the visibility budget expires, cleanup emits a warning requiring operator
reconciliation and stops retrying that request instead of polling indefinitely.

### Domain events emitted after a successful write

```text
lfx.{object_type}.{action}
```

Payload (`internal/domain/contracts/events.go`, `IndexingEvent`):

```json
{
  "document_id": "project:abc-123",
  "object_id":   "abc-123",
  "object_type": "project",
  "action":      "created",
  "timestamp":   "2026-03-05T19:57:25.679Z",
  "body": { ... }
}
```

`body` is the full `TransactionBody` written to OpenSearch. Publish failures here are
**non-blocking**: the OpenSearch write is unaffected; the error is logged.
Audited writes, once enabled, also include optional `event_id` as
`{publisher}:{publisher_event_id}` for stable downstream deduplication. Legacy
events omit that field. A guarded no-op retry, including replay that restores
missing history without replacing current state, does
not publish a new current-resource event.
When a same-revision reindex/backfill/sync projection arrives before its mutation,
the mutation still appends history and records its identity atomically, but does
not replace the refreshed current body or publish a replacement-body event.
Both arrival orders retain the projection body and the mutation identity.
Actor and effective/on-behalf-of identities remain confined to private history;
neither actor refs nor identity snapshots are copied into current documents or
downstream event bodies as top-level attribution.

Publisher namespace is a delivery-identity namespace, **not** a revision
namespace. All admitted publishers that can address the same `(object_type,
object_id)` must use one authoritative source revision sequence. Projection
workers copy that source revision; they must not invent independent counters.
Disjoint resources may have different publishers. Overlapping publishers are
approved only when they coordinate that shared source authority; startup does
not infer coordination or enforce one publisher per type.

## Request/reply delivery: `lfx.indexer.index.{object_type}`

A producer that must know its write has landed, for example before it lets a
client upload to a resource it just created, sends one request carrying the
resource's index projection and its access change, and treats the reply as the
result.

### Transport

- Core NATS request/reply on `lfx.indexer.index.{object_type}` (env
  `NATS_INDEXER_INDEX_SUBJECT`, default `lfx.indexer.index.>`), queue group
  `lfx.indexer.queue`. The object type rules are the same as for
  `lfx.index.{object_type}`.
- No JetStream stream or consumer captures the subject. The `index-events`
  stream binds `lfx.index.>`, which does not match it, and the indexer refuses
  to start if the two subjects overlap.
- A request without a reply subject is still processed, but nobody learns the
  result. Use request/reply.

### Request body

The body is `types.IndexRequest`: the `IndexerMessageEnvelope` above, unchanged,
plus two optional fields.

```json
{
  "action": "updated",
  "headers": {"authorization": "Bearer ..."},
  "data": {"uid": "...", "name": "..."},
  "indexing_config": {"object_id": "...", "access_check_object": "committee:...", "...": "..."},
  "fga": {
    "object_type": "committee",
    "operation": "update_access",
    "data": {"uid": "...", "public": false, "relations": {"writer": ["<username>"]}, "references": {"project": ["..."]}}
  },
  "sync": true
}
```

- `indexing_config`, `data`, `headers`, `tags`, `schema_version` and `audit`
  follow every rule in the sections above. The index half is the same
  processing as `lfx.index.*`: the same validation, the same audit workflow and
  audit-required-types fence (only an audited `mutation` adds a history
  revision), and the same post-indexing domain events.
- `fga` is one lfx-v2-fga-sync generic access operation (`object_type`,
  `operation`, `data`) with the same four operations: `update_access`,
  `delete_access`, `member_put` and `member_remove`. See
  `lfx-v2-fga-sync/docs/fga-sync-contract.md` for their fields.
- `sync: true` makes the index write wait for an OpenSearch refresh, so the
  document is searchable as soon as the reply is `OK`. Without it the document
  becomes searchable within the normal refresh interval. A replay of an audited
  write that was already applied is a no-op update, which may reply `OK`
  before the earlier write is searchable; it becomes searchable within the
  normal refresh interval.
- At least one of `indexing_config` and `fga` must be present, except that a
  delete is always index work, with or without `indexing_config`; its `data`
  must be the bare UID string. A request that carries `data` is also always
  index work, so a create or update with `data` and `fga` but no
  `indexing_config` is rejected rather than applied as FGA-only. A request
  with only one half applies only that half; an FGA-only request carries no
  `indexing_config` and no `data`.
- `fga` and `sync` are accepted only on this subject. On `lfx.index.*` an audit
  version 2 envelope carrying them is rejected as an unknown field.
- A request with `fga` and `audit.origin` `historical_import` replies
  `ERROR: rejected: fga: ...` before either half runs, because importing
  history creates no FGA grants.

### Reply body

| Reply | Meaning | Producer action |
|---|---|---|
| `OK` | Every half present in the request has landed | Clear the durable marker |
| `ERROR: transient: <half>: <text>` | Retrying can succeed | Retry the same request with backoff |
| `ERROR: rejected: <half>: <text>` | The request is invalid; a retry fails the same way | Drop and alert |
| no reply before the producer's timeout | Unknown | Retry the same request; replays converge |

`<half>` is `index`, `audit` or `fga`. `audit` names a failure of the audit
delivery receipt or history store; `index` names everything else on the index
side. A reply carries exactly one error and never reports partial success.

The two halves run concurrently, and neither cancels the other when it fails,
so a replay finds both as far along as possible. When both fail, the reply
reports a `rejected` error before a `transient` one, then `index`, `audit`,
`fga` in that order.

Requests the indexer rejects before applying anything include malformed JSON
(`ERROR: rejected: index: invalid request JSON`), a repeated key at the top
level or inside `indexing_config` (compared case-insensitively, as the JSON
decoder matches field names), a
request with neither block
(`ERROR: rejected: index: neither indexing_config nor fga is present`), a
missing `indexing_config.object_id`, an FGA-only request without
`fga.data.uid`, and an envelope the index path refuses to build a transaction
from. The index half also rejects an envelope that fails validation: an
unknown action, a missing object type, invalid `data` or `headers`, a missing
`indexing_config` on a create or update, an invalid object id, and an audit
version 2 envelope that fails strict decoding. An audited request whose
publisher and event id were erased from history replies
`ERROR: rejected: audit: ...`, because a replay meets the same erasure. An
audited request whose publisher and event id were already used with different
content also replies `ERROR: rejected: audit: ...`, because a replay meets the
same conflict.
Errors from storage,
authentication or other dependencies of the index half are `transient`.

The FGA half writes to the OpenFGA server at `OPENFGA_API_URL`, in the store
named `OPENFGA_STORE_NAME` (default `lfx-core`, looked up in the background at
startup) or `OPENFGA_STORE_ID`. Writes use the store's latest authorization
model unless `OPENFGA_AUTH_MODEL_ID` pins one. Its write semantics are
lfx-v2-fga-sync's:

- The tuples are written on `fga.object_type`, which may differ from the
  subject's object type (a `key_contact` request writes on
  `project_membership`).
- `relations` values are usernames; the indexer adds the `user:` prefix.
- Tuples OpenFGA refuses with a validation error are dropped and the rest are
  written, as fga-sync does.
- The payloads fga-sync terminates as invalid reply `rejected`: an empty
  `object_type`, an unknown `operation`, malformed `data`, a missing `uid` or
  `username`, empty `relations` or an empty relation name on `member_put`,
  and a `type:id` reference with an empty type or id. A reference value
  without `:` is taken as an id of the reference type, unvalidated, as
  fga-sync does. Every other OpenFGA failure is `transient` and replies
  `ERROR: transient: fga: openfga request failed`; the detail is logged, not
  returned, because it can carry the store id, the API URL or usernames.

Without `OPENFGA_API_URL`, or until the store is found, every request carrying
`fga` replies `ERROR: transient: fga: ...`. Index-only requests are unaffected.

### Ordering

- Requests for the same `object_ref` that reach the same replica are applied in
  arrival order. `object_ref` is `{object_type}:{data}` for a delete (the
  tombstone is written under that UID), `{object_type}:{indexing_config.object_id}`
  otherwise, or `{object_type}:{fga.data.uid}` when the request has no index
  half. A combined request is ordered by its index id even
  when `fga.data.uid` differs, as it does for resources whose access object is
  a parent. Requests for different resources run concurrently, so a slow FGA
  write for one resource does not delay another.
- The queue group spreads one resource's requests across replicas, so across
  replicas ordering relies on the producer: wait for the reply to one request
  before sending the next request for the same resource.
- Operations on one OpenFGA object (`fga.object_type:fga.data.uid`) run one at
  a time across all replicas, even when they come from requests for different
  resources. Each holds a lease in the NATS KV bucket `OPENFGA_LOCK_BUCKET`
  (default `indexer-fga-locks`) from its first read to its last write, so two
  full syncs of a shared object cannot interleave. A lease lives at most 30
  seconds, and the bucket TTL must be at least 60 seconds; a request that
  cannot get one before its timeout replies
  `ERROR: transient: fga: another request is writing this object`, and one
  that cannot reach the bucket, or finds its TTL below 60 seconds, replies
  `ERROR: transient: fga: fga write lease unavailable`.
- Each replica runs at most `NATS_WORKER_COUNT` requests at a time; beyond that
  it stops taking new requests until one finishes.

### Timeouts

The indexer gives up on a request after `INDEXER_REQUEST_TIMEOUT` and replies
`ERROR: transient: <half>: timeout`, naming the half still running. The default
is the JetStream ack-wait derivation (OpenSearch timeout plus batch max wait
plus a 10 s margin, or the larger audited budget when audit is enabled) plus the
8 s FGA call timeout: 48.2 s with the default settings. A shorter explicit value
is refused at startup. Producers must wait longer than this before treating a
missing reply as lost; otherwise a request that is still being applied is
retried while it may yet succeed.

The timeout runs from when the request arrives, so time spent queued behind
earlier requests for the same resource counts against it. A request whose
timeout runs out while queued replies `ERROR: transient: <half>: timeout`
without writing anything.

### Cache invalidation

After every successful FGA operation, and after a failed one that had already
written or deleted a tuple, the indexer records an invalidation on its FGA
cache backend before replying, so no access check is answered from a result
cached before the change. A successful operation that changed nothing still
invalidates: it may be the replay of a request whose invalidation failed. If
the invalidation cannot be recorded the reply is
`ERROR: transient: fga: cache invalidation failed`, and the replay records it.
With `CACHE_BACKEND=none` there is no cache to invalidate, so lfx-v2-fga-sync's
cache never learns of these writes. Before any producer sends `fga` on this
subject, point the indexer at the same cache fga-sync uses: the same
`CACHE_BACKEND`, and the same `CACHE_BUCKET` on the same NATS server for
`natskv`, or the same `VALKEY_ADDR` for `valkey`.

### Idempotence and retry

Replaying an identical request converges: OpenSearch overwrites by document id,
an FGA full sync diffs against the current tuples, and the audit append keeps
its delivery-receipt and fingerprint guards. Retry `transient` replies and
missing replies with backoff; never retry `rejected`.

### Reference helper

`types.SendIndexRequest(ctx, nc, objectType, req, timeout)` sends the request,
waits up to `timeout` for the reply, and returns `types.Success`, `types.Retry`
or `types.Drop`. `types.ClassifyIndexReply` applies the same classification to a
reply body received some other way.

```go
outcome, err := types.SendIndexRequest(ctx, nc, "committee", types.IndexRequest{
    IndexerMessageEnvelope: envelope,
    FGA:                    fgaOperation,
    Sync:                   true,
}, 60*time.Second)
switch outcome {
case types.Success:
    // clear the outbox row
case types.Retry:
    // keep the row; retry with backoff
case types.Drop:
    // drop the row and alert; err carries the reply
}
```

## Schema Evolution Policy

- **Adding fields inside `data`**: free. `flat_object` accepts new keys without a
  mapping change. If the field needs to be searchable/filterable, route it through
  `IndexingConfig` (Tags / NameAndAliases / Fulltext) instead of relying on `data`.
- **Adding top-level fields to the OpenSearch document**: requires an indexer-service
  change. Do not invent new top-level fields in a publisher.
- **Renaming or removing top-level fields**: breaking. Coordinate with the indexer
  team and the query-service team; determine whether existing documents require
  reindexing. The coordinated current-attribution cleanup above permits unused
  mappings and old document fields to remain until normal replacement.
- **Changing `IndexingConfig` shape**: breaking for all publishers. Coordinate.
- **Audit envelope version 2**: additive public fields with strict opt-in
  semantics, not an in-place change to existing fields. The runtime rejects it
  by default; see [availability and rollout](audit-v2-contract.md).
- **Adding a new resource type**: no indexer change needed. Publish on
  `lfx.index.<new_type>` with a valid envelope and `IndexingConfig`. Domain events
  `lfx.<new_type>.created/updated/deleted` are emitted automatically.

## Publisher Validation Checklist

Before merging a change to publisher code in a resource service, verify:

1. Every create/update path sends a non-nil `IndexingConfig` with `ObjectID`,
   `AccessCheckObject`, `AccessCheckRelation`, `HistoryCheckObject`, and
   `HistoryCheckRelation` populated.
2. Deletes use `action = "deleted"` and `Data` = the UID string (not the resource).
3. The subject matches the resource type that other services already use; check
   `pkg/constants/` for the canonical constant.
4. Legacy publishers may retain `conn.Publish()`; use acknowledged JetStream
   publishing for durable outbox delivery. On `lfx.index.*`, neither persistence
   acknowledgment nor core request/reply reports successful indexer processing;
   only an `OK` reply on `lfx.indexer.index.*` does.
5. Headers carry the lower-case `authorization` credential for JWT authentication.
   `x-on-behalf-of` does not populate current-document attribution. Audited actor
   evidence belongs in `audit.attribution`, under its separate verification policy.
6. Existing traffic omits both new discriminator fields. Do not enable audited
   delivery until the complete version 2 rollout in
   [audit-v2-contract.md](audit-v2-contract.md#rollout-coexistence-and-rollback)
   is complete.

## Adding a New Field to an Existing Resource

Adding a new field to the `data` payload requires no OpenSearch schema changes. `data`
is a `flat_object` and OpenSearch handles new keys dynamically.

If the new field should also be **searchable**, update the `IndexingConfig` construction
in the resource service's NATS publisher to include it in the appropriate search field
(`NameAndAliases`, `Tags`, or `Fulltext`).

## Per-Service Contract Docs

Services that follow the indexer contract pattern keep a `docs/indexer-contract.md` at
the repo root. This is the authoritative reference for that service's data schemas,
tags, access control config, parent references, and fulltext fields, derived directly
from the source code.

**Read this before writing or modifying indexing code for an existing service.** It
tells you what is already indexed and how, so you don't duplicate tags, miss required
fields, or break existing query patterns.

**Update it in the same PR as any indexing change.** The doc must stay in sync with
the code.

Use an existing resource service as a shape-only example when adding a contract
to a new service. Do not copy service-specific schemas, tags, access config, or
parent references; the target service owns its own contract.
