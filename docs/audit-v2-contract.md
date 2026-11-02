<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Version 2 audit envelope contract

This is the owner specification for `schema_version: 2` in
`types.IndexerMessageEnvelope`. The public types and preflight validators are
implemented in `pkg/types/audit.go`; public construction helpers live in
`pkg/types/audit_builder.go`. The current consumer **rejects version 2
before authentication, OpenSearch writes, cleanup, and post-index publication**.
It does not yet implement history storage, deduplication, source-revision guards,
publisher authorization, a schema registry, or a history query endpoint.

Do not enable publisher outbox delivery based on this contract-only release.
Rejected messages follow the existing bounded JetStream retry/retention policy;
they are not held indefinitely until a future consumer can process them.

## Two different meanings of V2

- **Platform V2 traffic** uses `lfx.index.<object_type>` and past-tense actions.
  Existing messages on those subjects are unversioned, current-resource messages.
- **Envelope version 2** explicitly sets `schema_version: 2` and supplies `audit`.
  Its guarantees are additional to the existing platform protocol.
- Platform V1 traffic remains on `lfx.v1.index.<object_type>` with present-tense
  actions. It must not carry the new envelope discriminator or audit block.

The legacy/current contract is [indexer-contract.md](indexer-contract.md).
The future storage/recovery design is [audit-history-design.md](audit-history-design.md).
Known publisher source evidence and the required staging verification are in
[publisher-compatibility.md](publisher-compatibility.md). No source inventory can
prove the absence of collisions in uninspected deployed clients or retained data.

## Compatibility and admission

| Wire input | Current consumer behavior |
| --- | --- |
| Neither `schema_version` nor `audit` present | Existing platform V1/V2 behavior |
| `audit` present without a version, including `audit: null` | Reject; never downgrade to legacy |
| Version 2 with audit present | Reject: audit processing is not enabled |
| Version 2 with audit absent/null | Reject: audit is required |
| Explicit version 0, 1, or any other unsupported version | Reject |
| Null, string, array, object, or non-integer version | Reject |
| Version/audit fields on a platform V1 subject | Reject |
| Case variants of the reserved discriminator names | Reject |

The fields are additive. Legacy Go publishers leave `SchemaVersion` at zero and
`Audit` nil; `omitempty` preserves their wire format. Explicit JSON zero/null is
not omission. There is deliberately no explicit envelope version 1 alias.

## Envelope and source identity

### Public Go construction library

Publishers use `types.NewMessageV2` with `MessageV2Options` and the approved
`AuditContentValidator` to construct and freeze a complete event. Nested helpers
`NewAuditActorProfile`, `NewAuditAttribution`, and `NewAuditSnapshots` construct
the closed profile/attribution/content structures without live lookups. Simple
source metadata and scope use the public `AuditEvent` and `AuditScope` types.
Full construction/restore applies the same strict decoding, limits, and contract
semantics as preflight; mutation schema approval is never defaulted.

The immutable `MessageV2` JSON contains no transport headers (`headers: null`).
Persist it and its resource type with the source mutation. `RestoreMessageV2`
rejects nonempty headers, preserves captured source data, and revalidates the
schema. `MessageV2.Envelope` creates an independent envelope with fresh service
authorization for dispatch; the public `ValidateV2`/`DecodeV2Envelope` still
require authorization. Neither a builder nor structural preflight authenticates
or authorizes a publisher. Constructors never allocate event identity/time/
revision, refresh profiles, publish, or enable consumer admission.

See the [Go client guide](client-guide.md#go-client-example) and
[compile-tested, offline-by-default example](../examples/audit-v2/main.go).

### Source fields

Keep existing `action`, `headers`, `data`, `tags`, and `indexing_config` fields.
Add `SchemaVersion int` and `Audit *AuditEvent`. Use action constants, not new
action names. Resource type comes from the subject; resource ID comes from
`indexing_config.object_id`. Do not repeat independently selectable identities
inside the audit block.

| Audit field | Meaning and validation |
| --- | --- |
| `publisher` | Namespace bound to an authenticated producer; `[a-z][a-z0-9_-]{0,63}` |
| `event_id` | Immutable publisher-wide delivery identity for any origin; `[A-Za-z0-9][A-Za-z0-9._-]{0,127}` |
| `occurred_at` | Required nonzero RFC3339 source occurrence time, independent of ingestion time |
| `resource_revision` | JSON integer from 1 through 9223372036854775807; monotonic for the resource |
| `origin` | `mutation`, `reindex`, `backfill`, or `sync_echo` |
| `attribution` | Required initiating/effective refs and denormalized user profile snapshots |
| `scope` | Required before/after ownership references according to the action |
| `content` | Approved before/after snapshots for mutations; absent for projection-only origins |

For mutation origins, the publisher assigns event identity, occurrence time,
revision, and payload once in the same PostgreSQL transaction as the committed
mutation. Transport
retries, replay, and indexer partial-write recovery preserve them. A request ID,
indexer attempt ID, ingestion timestamp, or JetStream sequence is not a substitute
for publisher-wide event identity. A JetStream sequence cannot deduplicate two
publications of the same outbox event.

A relay's `PubAck` establishes broker delivery, not history persistence. Retain
the original repair source for the agreed recovery window or durably transfer
failed work before broker retention expires. Broker-delivered outbox status must
not be mistaken for an audit-complete acknowledgment.

Resource revisions must never reset for a reused resource identity. Allocate a
new resource UID if its revision lineage cannot be maintained. Revisions order
changes to one resource, not all resources in a committee or project. An explicit
operation/correlation contract would be needed for multi-resource transactions;
the event ID is not a claim that a whole multi-step workflow was atomic.

The decoder preserves integers without conversion through `float64`, rejects
unknown metadata fields and noncanonical field casing, duplicate JSON keys,
trailing JSON values, envelopes over 1 MiB, and nesting deeper than 64 levels.
Transport payload limits may be smaller after accounting for headers. Errors
must not echo payload values, tokens, or rejected snapshot data.
Typed `json.RawMessage` resource objects and snapshots also reject invalid UTF-8
before parsing, so typed and wire preflight cannot disagree after replacement
decoding. Structural scanning tracks only bounded metadata paths; opaque
resource/snapshot/contact-profile keys are never copied into descendant paths.
Duplicate-key and depth checks still apply inside those opaque objects.

## Attribution and authentication

`attribution.actor_ref` identifies the initiating actor;
`attribution.effective_actor_ref` identifies the effective/on-behalf-of actor.
They are equal without delegation. Each is a stable `user:<opaque-id>` or
`service:<opaque-id>` correlation reference, not an instruction to look up a live
account. A background system mutation explicitly identifies a service actor;
missing authentication must never be converted into a system identity. The
fresh relay/publisher credential is not the original initiating user.

### Denormalized user attribution

For every `user:` actor, include a required `actor_profile` or
`effective_actor_profile`, respectively. These preserve the requested **name,
avatar URL, email, and username** independently of whether that account still
exists. Retain the refs alongside the snapshots; username/email changes must
not rebind a stable user ref to a different person. Both profiles must match
when the refs match; capture once and reuse the result without a second lookup.
Delegated operations capture the initiating and effective users separately.

Each profile is a closed object with all five keys present:

| Profile field | Meaning / validation |
| --- | --- |
| `availability` | `captured` if at least one attribute was captured; `unavailable` if none could be captured |
| `name` | Display name as observed at the source operation; string up to 1024 UTF-8 bytes, or null |
| `avatar_url` | Stable, credential-free absolute HTTPS avatar URL up to 2048 bytes, or null; no userinfo, query, or fragment |
| `email` | Bare email mailbox (`addr-spec`) up to 320 bytes, or null; no name-addr wrappers; valid quoted local parts are preserved |
| `username` | Username as observed at the source operation, up to 256 UTF-8 bytes, or null |

Unavailable attributes are **explicit nulls**, not omitted fields, empty strings,
or instructions to enrich later. Non-null values must be nonempty valid UTF-8
without control characters. A `captured` profile may be partial; an `unavailable`
profile must have all four attributes null. A transient lookup failure or an
already absent user must not force a publisher to fabricate personal data or
drop a committed mutation: record the available verified attributes, or the
explicit unavailable profile. Never synthesize a username from an opaque ref.
Service actors omit/null the human profile instead of inventing a human email
or username. All other profile keys, including arbitrary directory/profile maps,
phone/address fields, headers, and credentials, are rejected.

The trusted publisher captures these values from verified request/directory/cache
context when it commits the source operation and immutable outbox event. They
are the values recorded then, not a claim that a cache was a fresh directory
read. Capture before deleting relevant user/context data where possible. Do not
require that the actor account still exists when delivering or reading history.
Do not refresh profiles from the directory, from the relay's service identity,
or from the latest resource on retry/replay/read. Repeating an event ID with a
renamed/re-enriched profile changes immutable content and is a conflict, not a
permitted refresh. A history UI should display the recorded snapshot, including
explicit unavailability, rather than silently replace it with current details.
Render the strings as text, not trusted HTML. Retained outbox/history records
must not depend on a live-user foreign key or cascade away solely because an
account disappears; approved privacy-erasure workflows are a separate policy.

Avatar URLs preserve the observed **URL string**, not immutable image bytes;
the remote image may later change or disappear. The indexer/validators do not
fetch URLs. If a provider only supplies a signed/tokenized/query-bearing URL,
capture an approved stable query-free equivalent or null; do not strip arbitrary
parameters or persist credentials. Durable avatar image archival would require
a separately designed storage/retention path and is not included here.

These four personal attributes are explicitly part of the audit attribution
contract for historical request accountability; this is not approval to persist
an arbitrary user profile. They must not be copied into ordinary current-resource
documents, general logs, metrics, tracing, or public/anonymous history responses.
Profile data is not an authorization grant. Future storage/read implementations
must restrict it to history-authorized callers, define bounded retention and
erasure/export obligations, and avoid general full-text indexing of these fields
by default. Approved resource snapshots remain a separate schema allowlist.
Controlled erasure is an exception to ordinary append-only ingestion: it must
cover both actor copies and applicable snapshots/outbox/repair stores, coordinate
backup retention, and fence replay so erased profiles cannot be resurrected.
Routine account disappearance is not a dependency failure for historical reads;
retention/erasure decisions are governed by the approved policy, not live lookup.
`availability` describes source capture, not later redaction. Future stored/read
representations must distinguish policy-redacted data from source-unavailable
data instead of rewriting that historical capture claim.

Opaque IDs are 1–256 printable non-space ASCII characters, excluding `:`, `#`,
`@`, `{`, and `}`. Resource types use `[a-z][a-z0-9_]{0,63}`; scope/authorization
references use `<type>:<id>`;
relations use `[a-z][a-z0-9_]{0,63}`. These syntax checks do not prove authority.

The outbox stores approved event data, **not user JWTs or raw headers**. At
dispatch, the relay attaches fresh service authentication in the existing
lowercase `headers.authorization` field. The indexer must bind that authenticated
producer to its publisher namespace, allowed resource subjects, origin choices,
and authority to attest actors/scope. A `publisher` string is not authentication.
Delegation and the binding between each ref and its captured profile must be
authorized/attested separately, not inferred from arbitrary client metadata.

Legacy header-derived principal extraction is unchanged. The new attribution is
not derived from an expired original request token and is never a read grant.
The runtime trust/binding implementation is an enablement prerequisite, not
something the public structural validators implement.

## Scope, deletion, and current-resource data

`scope.before` and `scope.after` are arrays of ownership resource references.
They are generic: for example, a resource may be scoped by project and committee.
They are not caller-selected FGA grants. A scope move records both old and new
ownership; history-read policy must govern former/private/cross-project states.

| Action | Top-level `data` | Scope | Mutation snapshots |
| --- | --- | --- | --- |
| `created` | Resource object | Nonempty `after`; no `before` references | Required `after`; omit `before` |
| `updated` | Resource object | Nonempty `before` and `after` | Required `before` and `after` |
| `deleted` | Bare resource UID | Nonempty `before`; no `after` references | Required `before`; omit `after` |

Every version 2 message, **including deletes**, requires a complete
`IndexingConfig` with concrete object ID, ordinary access object/relation, and
history object/relation. A delete UID must match the config ID. The legacy
mismatch fallback is not permitted for audited deletes. Version 2 **never
expands config strings**: materialize intended substitutions before storing the
immutable outbox event. IDs and authorization references must still be concrete
and syntactically valid; free text and contact profiles may contain literal
template-shaped prose such as `{{ .Values.image }}` or `}}`. Future version 2
processing must not route config through the legacy template expander.

History authorization metadata is captured at mutation time, not reconstructed
from an overwritten current search document. The durable deleted-resource
authorization anchor, revocation rules, and permitted scope transitions belong
to [the authorization policy work](https://github.com/linuxfoundation/lfx-self-serve/issues/4617).
No project-level example grants access by itself. Missing metadata fails closed;
`public: true` must never bypass history authorization.

Top-level `data` still shapes the current search projection. It is not implicitly
an approved history snapshot. When included in an outbox payload, it must be the
publisher's sanitized indexer representation, not a raw database row containing
credentials. Ordinary search document fields and query behavior are unchanged
by this contract-only release.

## Approved content schemas

Only `content.representation: "snapshots"` is defined initially. `content.schema`
names a separately versioned publisher/resource-owned schema (maximum 128 ASCII
token characters with the same syntax as event IDs). Each present `before` or
`after` must be a JSON object; null, arrays, and explicit absent-side nulls are
not accepted. The approved schema determines the exact fields and types.

`ValidateV2` and `DecodeV2Envelope` require an `AuditContentValidator` for mutations.
It must reject an unregistered schema, a publisher/resource ownership mismatch,
or snapshots violating its field allowlist, required fields, and privacy rules.
Nil validators reject mutation content. No production resource schemas/registry
are supplied in this release; the unit-test schema is not policy approval.

Never install a permit-all callback. A sensitive-key denylist is insufficient.
Never retain credentials, tokens, raw headers, arbitrary request bodies, or
unapproved profiles. Snapshot schema versions are immutable once adopted;
changing permitted fields/semantics requires an explicitly supported schema
version and coordinated retention/privacy review.

## Non-mutation operations

`reindex`, `backfill`, and `sync_echo` are projection-only origins and must omit
`audit.content`. They still require identity, source revision/time, attribution,
scope, config, and an authorized producer. They must not be used by untrusted
request fields to suppress auditing of a real mutation. Source revision remains
the authoritative resource revision, not a newly invented business change.
Projection operations use **`audit.event_id`**, namespaced by **`audit.publisher`**,
just as mutations do: `(audit.publisher, audit.event_id)` is the delivery and
deduplication key for every origin. Assign a distinct stable ID to each logical
projection operation and preserve it, its captured payload, and its source
revision/time across retries. A fresh resource read or different operation needs
a new delivery ID; do not reuse a mutation's ID for projection work. IDs must
remain unique across all origins within the publisher namespace, not merely
within a resource type or origin. No separate projection identity field exists.
Projection deduplication must not fabricate a history record merely to store a
delivery receipt; its operational state and source-revision guard are separate
implementation concerns required before enabling projection processing.

All origins are currently rejected by runtime admission. Future processing must
authorize classification before selecting append versus projection-only paths,
and must not let an old projection overwrite newer current state. True source
mutations caused by imports or synchronization are still `mutation`; they are
not echoes merely because a system initiated them.

## Future persistence and idempotency obligations

An enabled mutation path must create/verify history first, then safely apply
current state, and ACK only after required durable processing succeeds. History
is separate from `resources`, with no current-resource janitor or public bypass.
No history mapping or new current-document top-level fields are introduced here.

Use `(publisher, event_id)` as collision-free history identity. Compare immutable
semantic content: action, subject resource type, projection data/tags/config,
and all audit metadata/content, including both profile snapshots and their
availability. Exclude transport authentication/tracing headers
and indexer-assigned ingestion timestamps. Normalize object member order and
timestamp instants, preserve exact numbers and meaningful array order. A retry
must not change `ingested_at`, which is established on first successful append.

- Same identity and same immutable content: verify the existing append and
  resume/complete missing current-state work without duplicating history.
- Same identity and different immutable content: fail before overwriting either
  history or current state; make publisher identity misuse observable.
- Old source revision: preserve an admissible late mutation in history, but do
  not replace a newer current projection. Equal revisions with divergent content
  require conflict detection too, not last-write-wins.
- Partial writes and unknown timeout outcomes: retry with the same event, not a
  fresh ID. There is no cross-index transaction and no exactly-once claim.

Retain an ordering guard even on current tombstones. Suppressed projection events
must not invent history or bypass ordering. Stable downstream notification identity
and replay semantics require coordination with subscribers; the existing
best-effort post-index event is not a substitute for the audit event.

Before enabling writes, implement bounded retention, erasure/replay fences,
index permissions, exhausted-delivery monitoring, and reconciliation. Neither a
new event ID nor a new ingestion time may resurrect purged/erased history.
Future retention-aware disposition must not accidentally prevent removal of a
deleted current resource; its explicit policy is a rollout prerequisite.

## Rollout, coexistence, and rollback

1. Deploy these public types and fail-closed discriminator guards first. Keep
   audited publishers disabled; old messages continue their current behavior.
2. Implement history storage/recovery, trusted publisher admission, approved
   snapshot schemas, policy/retention, revision guards, and operations support.
3. Upgrade **all** consumers sharing the current indexing subjects before
   publishing version 2 there. Older indexers ignore unknown fields and can ACK
   away an audit requirement. This release's guard cannot protect older replicas.
4. If mixed-version rollout is unavoidable, separately provision an isolated
   audited subject/stream/consumer with appropriate publisher permissions. No
   isolated namespace, stream, subscription, or enablement flag ships here.
5. Enable only approved publishers/resource types. Record coverage start times
   and gaps. Legacy traffic remains supported globally, but legacy mutations for
   an audited identity cannot establish completeness or silently bypass revision
   guards. Enforce an explicit audit-required policy for adopted scopes before
   claiming complete history.
6. On rollback, disable/pause publisher delivery and durably retain outbox work
   before restoring a consumer that ignores the new discriminator. Do not rely
   on the indexer's bounded retry window as long-term holding storage.

Complete audited processing remains
[indexer work](https://github.com/linuxfoundation/lfx-self-serve/issues/4616), with
[publisher adoption](https://github.com/linuxfoundation/lfx-self-serve/issues/4615),
[authorization policy](https://github.com/linuxfoundation/lfx-self-serve/issues/4617),
and [history queries](https://github.com/linuxfoundation/lfx-self-serve/issues/4618)
owned separately. Contract publication is not an audit history start date.
