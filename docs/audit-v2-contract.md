<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Version 2 audit envelope contract

> **Attribution revision in progress:** the verification, retained-identity and cleanup requirements
> below specify the coordinated change tracked in
> [ops #1527](https://github.com/linuxfoundation/lfx-self-serve-ops/issues/1527).
> Public input types, ingestion verification, credential stripping, first-append
> identity resolution, replay checks and searchable history actor fields are
> implemented in the indexer. Query-service live enrichment is implemented in
> [query-service PR #91](https://github.com/linuxfoundation/lfx-v2-query-service/pull/91).
> The retired platform protocol is removed; current-document
> attribution and companion fixture cleanup remain outstanding. Audited production
> publishing remains disabled; do not adopt this revision with old readers.
> This revision replaces publisher-supplied profile snapshots; update publishers,
> indexer and history-query readers together before adoption.

This is the owner specification for `schema_version: 2` in
`types.IndexerMessageEnvelope`. The public types and preflight validators are
implemented in `pkg/types/audit.go`; public construction helpers live in
`pkg/types/audit_builder.go`. The consumer **rejects version 2 by default
before authentication, OpenSearch writes, cleanup, and post-index publication**.
Audited ingestion is opt-in and requires a reviewed, closed publisher/schema
policy and the separate private history index; no production publisher policy
is bundled. The history-query endpoint is owned by query-service. See the
[rollout guide](audit-rollout.md).

Do not enable publisher outbox delivery based on the public types alone.
Rejected messages follow the existing bounded JetStream retry/retention policy;
they are not held indefinitely until a future consumer can process them.

## Two different meanings of V2

- **Platform V2 traffic** uses `lfx.index.<object_type>` and past-tense actions.
  Existing messages on those subjects are unversioned, current-resource messages.
- **Envelope version 2** explicitly sets `schema_version: 2` and supplies `audit`.
  Its guarantees are additional to the existing platform protocol.
- The retired platform protocol is no longer subscribed to or processed.
  Platform V2 subject naming and audit-envelope versioning remain independent;
  neither legacy-backed resource names nor unversioned V2 publishers are removed.

The legacy/current contract is [indexer-contract.md](indexer-contract.md).
The opt-in storage/recovery implementation and remaining rollout gates are
described in [audit-history-design.md](audit-history-design.md).
Known publisher source evidence and the required staging verification are in
[publisher-compatibility.md](publisher-compatibility.md). No source inventory can
prove the absence of collisions in uninspected deployed clients or retained data.

## Compatibility and admission

| Wire input | Current consumer behavior |
| --- | --- |
| Neither `schema_version` nor `audit` present | Existing platform V2 current-resource behavior |
| `audit` present without a version, including `audit: null` | Reject; never downgrade to legacy |
| Version 2 with audit present | Reject by default; when enabled, require approved publisher/subject/origin/schema, then append/verify history before a revision-guarded current write |
| Version 2 with audit absent/null | Reject: audit is required |
| Explicit version 0, 1, or any other unsupported version | Reject |
| Null, string, array, object, or non-integer version | Reject |
| Any message on an unsupported subject | Reject; the consumer filters only platform V2 subjects |
| Case variants of the reserved discriminator names | Reject |

The fields are additive. Legacy Go publishers leave `SchemaVersion` at zero and
`Audit` nil; `omitempty` preserves their wire format. Explicit JSON zero/null is
not omission. There is deliberately no explicit envelope version 1 alias.

**Pre-adoption schema-v2 correction (ops #1527):** the contract owner confirmed
that the superseded profile-bearing representation has no compatibility-dependent
consumers or frozen outboxes, and explicitly approved retaining version 2 as a
one-time exception to the usual envelope-versioning rule. Ref-only version-2
events remain supported. Publisher profiles, including null profiles, are now
rejected by both wire decoding and `RestoreMessageV2`; they are not silently
converted into trusted identity. Regenerate obsolete development fixtures from
trusted source data under this contract rather than stripping fields from frozen
events. If an unexpected old outbox or consumer is discovered, stop adoption and
agree on a versioned compatibility plan before dispatch. This exception is not
permission to make future incompatible changes to an adopted version.

## Envelope and source identity

### Public Go construction library

Publishers use `types.NewMessageV2` with `MessageV2Options` and the approved
`AuditContentValidator` to construct and freeze a complete event. Simple source
metadata and scope use the public `AuditEvent` and `AuditScope` types.
Construction/restore applies the same strict decoding, limits, and contract
semantics as preflight; mutation schema approval is never defaulted.

`NewAuditAttribution` accepts refs and optional JWT evidence, never
publisher-supplied identity. The old profile builder/type have been removed.
`AuditActorIdentity` contains only nullable username/email; stored attribution
uses `actor_identity` and `effective_actor_identity` alongside verification flags.
`NewAuditSnapshots` still constructs approved
resource content; it is unrelated to actor identity.

The immutable `MessageV2` JSON contains no transport headers (`headers: null`).
Persist it and its resource type with the source mutation. `RestoreMessageV2`
rejects nonempty headers, preserves captured source data, and revalidates the
schema. `MessageV2.Envelope` creates an independent envelope with fresh service
authorization for dispatch; the public `ValidateV2`/`DecodeV2Envelope` still
require authorization. Neither a builder nor structural preflight authenticates
or authorizes a publisher. Constructors never allocate event identity/time/
revision, verify actor JWTs, resolve identity, publish, or enable consumer
admission. After the coordinated update, frozen attribution may include actor
JWTs without weakening the prohibition on transport headers. Builders validate
structure only; the consumer verifies evidence when it ingests the event.

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
| `origin` | `mutation`, `historical_import`, `reindex`, `backfill`, or `sync_echo` |
| `attribution` | Required initiating/effective refs and optional JWT evidence; verification and retained identity are indexer-owned output |
| `scope` | Required before/after ownership references according to the action |
| `content` | Approved before/after snapshots for mutations and historical imports; absent for projection-only origins |

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
They are equal without delegation. Use `user:<principal>`, where the ID is
exactly the Heimdall `principal` claim, or `service:<client name>`, whose signed
principal is `<client name>@clients`. Do not substitute an email or Auth0 subject
for the principal. A background mutation explicitly identifies a service actor;
missing authentication must never be converted into a system identity. The fresh
relay credential is not the original initiating user's credential.

Historical imports additionally accept `legacy:<source>/<opaque-id>` for an
actor that cannot be mapped confidently to an LFX principal or known M2M client.
`source` uses the publisher namespace grammar (for example `v1-platform` or
`v1-salesforce`); the complete `<source>/<opaque-id>` obeys the existing 256-byte
opaque-ID bound and character rules. This preserves source provenance, not a
username, service identity or authorization object. It is valid **only** for
`historical_import`, in either actor role, and must not carry JWT evidence.
No envelope fields or OpenSearch mapping fields are added. Older consumers and
readers reject this new actor kind; deploy compatible readers before dispatch.

### Publisher input and indexer-owned output

Publishers send the two refs and optional `actor_jwt` and `effective_actor_jwt`
strings. An envelope supplying `actor_verified`, `effective_actor_verified`,
`actor_profile` or `effective_actor_profile` is invalid, even if a flag is false
or a profile is null. Strict wire decoding must distinguish explicit false from
omission. Publishers cannot assert verification or retained identity.

Only the indexer sets verification booleans and retained user identities. Stored
attribution includes the refs, booleans, and, for users, an identity with **only
nullable `username` and `email`**. Service actors have no identity record.
Neither JWTs nor name/avatar snapshots belong in stored attribution.

Unresolved `legacy:` actors have an identity object with both `username` and
`email` explicitly null, and verification always false. The source-qualified
reference is retained unchanged. It never enters token validation, the identity
cache or auth-service lookup, even if a direct internal caller supplies evidence
or an email. Wire/builders reject JWT evidence on this kind rather than treating
it as an LFX principal. The indexer does not invent a human or machine identity.

### Verification at ingestion

Use the existing JWT validator, including issuer, audience, signature and
`JWT_CLOCK_SKEW` checks. Never treat unverified decoded claims as evidence.

| Flag | Rule |
| --- | --- |
| `actor_verified` | True only when `actor_jwt` validates and its `principal` matches the actor ref under the user/service rules above |
| `effective_actor_verified`, different refs | True only when `effective_actor_jwt` validates, its `principal` matches the effective actor ref, and the authenticated dispatch principal is a machine user |
| `effective_actor_verified`, equal refs | Exactly `actor_verified`; reuse the initiating actor's verification result |

A missing, expired, badly signed, or non-matching token sets false and logs a
WARN; it **never rejects the event**. Warn using fixed reasons and actor roles,
not tokens, decoded claims, raw validator errors, or user identity. Actor evidence
does not authorize the publisher, origin, subject, schema, or history read.

Dispatch `headers.authorization` is the publisher's own machine credential and
identifies it for the trusted-publisher policy. Audited messages do not use
`x-on-behalf-of`; delegation evidence comes from explicit attribution.

Validate tokens and strip them before fingerprint construction, history append,
delivery receipts, logs and post-indexing publication. The indexer must never
persist a JWT. Frozen outbox payloads may retain the actor JWTs (not dispatch
headers) until retired rows are pruned at the outbox retention limit. Publishers
must expect delayed work beyond `JWT_CLOCK_SKEW` to be unverified; clock skew is
not a token refresh or durable verification guarantee. Expired evidence must not
cause a committed event to be dropped.

### Retained identity at first append

Set a user's `username` to the principal ID in its ref. Take `email` from the
matched, validated actor token's email claim. Without valid matching evidence
or an email claim, resolve email by principal through auth-service:

- Subject: `lfx.auth-service.user_emails.read`.
- Request: `{"user":{"auth_token":"<principal>"}}`. Despite the field name,
  the owner supports principal/username input, not only tokens.
- Success: `{"success":true,"data":{"primary_email":"user-1@example.com"}}`.
  Retain the primary email, not alternate emails.
- Not found: `{"success":false,"error":"user not found"}`; retain null email.

The owner contract is `lfx-v2-auth-service/docs/subjects/user_emails.md`; the
implementation is `lfx-v2-auth-service/internal/service/message_handler.go`.
`user_metadata.read` supplies display metadata, not email.

Cache results in the backend selected for the FGA cache under backend-safe
`identity.` keys: 10-minute TTL for found users, 1 minute for not found. Backend
selection comes from ops #1526; identity operations are implemented by #1527.
Cache failures are misses, not deletion. NATS KV enforces identity expiry on read
inside the shared bucket's retention window; Valkey also applies a per-key TTL.
Identity-cache reads/writes are each bounded by 5 seconds (or a shorter cache or
delivery deadline), independently of the 5-second auth-service lookup bound.
Do not negative-cache timeouts, no-responders, malformed replies, or other errors.

A timeout/no-responder during a required uncached lookup is transient:
compatibility replies return `ERROR: transient: ...`, and durable delivery
retries the whole envelope. A valid matching token with email, or an available
cached result, avoids that dependency. A valid token with no email still needs
fallback resolution. Never use the publisher's or a mismatched token's email.

Resolve identity only for the first append. Replaying an already-appended event
verifies its immutable fingerprint without re-resolving identity. Equal actor
refs reuse one result. Concurrent first deliveries may resolve independently,
but create-only storage retains the winning append's identity, verification
flags and ingestion timestamp.

Username/email are the retained data-governance record even after account rename
or deletion. Do not copy them into current-resource documents, general logs,
metrics, tracing or anonymous responses. This is not permission to store an
arbitrary directory profile. Keep existing history authorization, indefinite
retention, targeted erasure/export and replay fences. Erasure covers both actor
copies, applicable snapshots, outbox/repair stores and backups; routine account
disappearance must not cascade-delete audit history.

### Live history presentation (query-service)

The API keeps the profile shape `availability`, `name`, `avatar_url`, `email`,
`username`. Email and username come only from the retained event. Resolve current
name/avatar at read time using `lfx.auth-service.user_metadata.read` with the raw
principal as the body; success fields `data.name` and `data.picture` provide the
display values. Do not store these in history, receipts or post-indexing events,
and do not fetch remote avatar image bytes.

| `availability` | Meaning | Name/avatar |
| --- | --- | --- |
| `available` | Live lookup succeeded | Current values, nullable if not supplied |
| `deleted` | Explicit user-not-found response | Both null |
| `unavailable` | Lookup failed, timed out, or had no responder | Both null |

All three statuses retain the stored email/username. Lookup failure never fails
the history request. Pass both verification flags through unchanged. Resolve at
most once per distinct actor per page across both roles, with an in-process
cache: 10 minutes found, 1 minute not found. Do not cache failures as deletion.
Enrich authorized events only; no extra per-user gate is added beyond existing
history authorization. Render display strings as text, not trusted HTML.

For `legacy:` actors, the compatible reader returns an `unavailable` profile
with null username/email/avatar and the display-only label `Unknown legacy user`.
This label is not persisted identity. No directory lookup is attempted, and the
stored false verification flag is preserved. Service actors still omit profiles.
The reader must reject verified/non-null legacy identities and legacy refs on
non-import records instead of disguising malformed history as an unknown actor.

Opaque IDs are 1–256 printable non-space ASCII characters, excluding `:`, `#`,
`@`, `{`, and `}`. Resource types use `[a-z][a-z0-9_]{0,63}`; scope/authorization
references use `<type>:<id>`;
relations use `[a-z][a-z0-9_]{0,63}`. These syntax checks do not prove authority.

The outbox stores approved event data and optional actor evidence, **not raw
transport headers or dispatch credentials**. At dispatch, the relay attaches
fresh service authentication in the existing
lowercase `headers.authorization` field. The indexer must bind that authenticated
producer to its publisher namespace, allowed resource subjects, origin choices,
and authority to attest actors/scope. A `publisher` string is not authentication.
Actor verification follows the rules above; references and retained identity
are never read grants. Structural preflight does not validate JWTs or resolve
identity. Runtime trust/binding remains an enablement prerequisite.

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
mismatch fallback is not permitted for audited deletes. Version 2 config strings
are literal: materialize intended values before storing the immutable outbox
event. IDs and authorization references must still be concrete and
syntactically valid; free text and contact profiles may contain literal
template-shaped prose such as `{{ .Values.image }}` or `}}`.

History authorization metadata is captured at mutation time, not reconstructed
from an overwritten current search document. The durable deleted-resource
authorization anchor, revocation rules, and permitted scope transitions belong
to [the authorization policy work](https://github.com/linuxfoundation/lfx-self-serve/issues/4617).
No project-level example grants access by itself. Missing metadata fails closed;
`public: true` must never bypass history authorization.

Top-level `data` still shapes the current search projection. It is not implicitly
an approved history snapshot. When included in an outbox payload, it must be the
publisher's sanitized indexer representation, not a raw database row containing
credentials. This revision changes audit ingestion, public types, history
mappings and deployment timing requirements. Current-resource attribution fields
have been removed: all nine fields (`created_by`, `created_by_principals`,
`created_by_emails`, and the corresponding `updated_by*` and `deleted_by*` fields)
are absent from the resources mapping template, `TransactionBody`, both projection
paths, and post-index event bodies. Timestamps remain. Existing index mappings may
retain unused fields and old documents may retain values until replacement; no
index rebuild or live deletion is required. `ParsePrincipals` no longer runs for
current-document attribution. Unversioned writes are not authenticated, as before
audit: the `authorization` header must be present but is not validated, because
existing publishers send service-identity strings rather than JWTs. Only audited
(version 2) messages are authenticated, by the audit workflow. Private history retains actor refs, username/email identity
snapshots, and verification flags with unchanged evidence verification behavior.

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

## Historical imports

`origin: "historical_import"` represents an available historical source change,
not a new live mutation or a current-resource bootstrap. It uses the existing
version-2 shape with **no additional fields**. Earlier consumers reject this
unknown origin; upgrade every consumer before granting an import policy. Do not
change the origin on a frozen event or reuse its identity under another origin.

- The authenticated publisher must have explicit `historical_import` permission
  for the subject resource type and approved closed snapshot schemas. Mutation
  permission does not imply import permission, nor does import permission grant
  mutation, reindex or backfill. `backfill` continues to omit content and never
  appends history.
- Import content obeys the same action-specific before/after snapshot rules as
  mutations. Identity, occurrence time, positive bounded source revision,
  actor refs, scope and complete concrete authorization config remain required.
  Source revisions must have an approved source-owned meaning and be frozen
  across retries; import revisions never establish, advance or reset the live
  current-resource revision lineage. Do not invent missing historical values to
  fit a live resource schema. Approve an appropriate versioned historical schema.
- The workflow reserves the publisher/event ID, verifies retained history or its
  erasure fence, and appends missing history through the same create-only adapter.
  Success/ACK requires completed history persistence or verification, **not just
  a receipt or PubAck**. Identical replay preserves the first ingestion time and
  attribution; changed immutable content/origin conflicts. A receipt can exist
  after a failed append and does not prove history was imported.
- There is **no current-resource stage**: imports never create an absent resource,
  overwrite a live resource/tombstone, modify current revision metadata, schedule
  cleanup or emit `lfx.{object_type}.{action}`. This holds even if the imported
  revision is higher than the live revision. The projection adapter also rejects
  this origin defensively. No current-resource lookup is used for admission.
- A history-only policy does not need a legacy projection fence for its resource
  type and does not establish live audit adoption/completeness. The existing
  global audited startup/provisioning gates remain unchanged. Live mutations,
  reindex and sync-echo still require fenced required types.
- Existing history authorization and erasure/replay fences remain mandatory.
  Use stable source-derived IDs and never re-ID an erased event to bypass a
  fence. Importing history creates no FGA grants. Missing or deleted anchors fail
  closed under the history-query authorization policy; do not guess a project
  grant to make deleted-committee history readable.

### Historical attribution mapping

The agreed V1 import policy prefers current display enrichment over retaining
historical name/avatar captures. Supplied actor profiles/identities/verification
flags remain rejected. The importer authenticates as its own machine publisher;
it does not mint Heimdall actor JWTs to manufacture evidence of past requests.

| Source attribution | Import representation |
| --- | --- |
| Confidently mapped LFX username | `user:<principal>` without actor JWT evidence. Retain the username; resolve current email at first append and current name/avatar at read time. These are not claims about historical profile values. |
| Known username, account absent | Preserve the user ref/username; null email on explicit absence. Read-time explicit absence gives `deleted` with null name/avatar. Do not rebind a possibly reassigned username without a trustworthy identity match. |
| Known M2M client | `service:<client-name>`, without the `@clients` suffix or a human profile. Preserve the actual client, not the importer. |
| No confident username/client mapping | `legacy:<source>/<opaque-id>`, retaining the recorded actor ID. Identity attributes are null, verification is false, and no directory lookup occurs. |

If the source actor ID itself is absent, use a stable, explicitly documented
source-row reference (for example `legacy:v1-audit-event/<source-event-id>`), not
a single invented user shared by unrelated events. The ref describes provenance,
not evidence that two records have the same initiating person. Missing profile
attributes remain null; generic display labels are never stored as usernames,
emails, names or avatars. Email-only matching must not silently bind history to
a current account. A source actor may have been a last modifier rather than the
deleter: preserve that limitation rather than promoting it to verified evidence.

Resolve mappings once before freezing an event. Later identity discovery or
directory changes must not rewrite attribution under that event ID; identical
replay retains the first append's identity/verification and bypasses resolution.
Transient directory failures remain retryable and are never classified as account
absence. Permanent erasure fences still apply to all imported actor copies,
resource snapshots, export/manifests and replay sources.

This does not approve any raw V1 resource snapshot or turn missing source values
into defaults. Closed historical resource schemas and source mapping/coverage
remain prerequisites for dispatch. Do not copy raw legacy rows into live schemas.

## Non-mutation operations

`reindex`, `backfill`, and `sync_echo` are projection-only origins and must omit
`audit.content`. They still require identity, source revision/time, attribution,
scope, config, and an authorized producer. They must not be used by untrusted
request fields to suppress auditing of a real mutation. Source revision remains
the authoritative resource revision, not a newly invented business change.
Computed values, rollups and third-party-shadowed data that must not create a
history revision use `origin: reindex` with `resource_revision` copied unchanged
from the authoritative resource. Do not increment it or manufacture a mutation
to refresh the projection. A real authoritative business change remains a
mutation even when a system initiates it.
Projection operations use **`audit.event_id`**, namespaced by **`audit.publisher`**,
just as mutations do: `(audit.publisher, audit.event_id)` is the delivery and
deduplication key for every origin. Assign a distinct stable ID to each logical
projection operation and preserve it, its captured payload, and its source
revision/time across retries. A fresh resource read or different operation needs
a new delivery ID; do not reuse a mutation's ID for projection work. IDs must
remain unique across all origins within the publisher namespace, not merely
within a resource type or origin. No separate projection identity field exists.
Projection deduplication must not fabricate a history record merely to store a
delivery receipt; its minimal create-only operational state lives in the
private `resource-audit-deliveries-v1` index, separate from mutation history.
The current-document source-revision guard atomically preserves a stable
same-revision projection ordering key so older replays cannot replace newer
applied projections. Delivery receipts also reserve publisher/event identities
across mutation and projection origins.

All origins are rejected by default runtime admission. Enabled processing must
authorize classification before selecting append versus projection-only paths,
and must not let an old projection overwrite newer current state. True source
mutations caused by imports or synchronization are still `mutation`; they are
not echoes merely because a system initiated them.

## Future persistence and idempotency obligations

An enabled mutation path must create/verify history first, then safely apply
current state, and ACK only after required durable processing succeeds. History
is separate from `resources`, with no current-resource janitor or public bypass.
The coordinated history mapping adds top-level `actor_ref` and
`effective_actor_ref` keyword fields and `actor_verified` and
`effective_actor_verified` boolean fields, populated at append time. All four
fields must remain indexed (omitted `index` defaults to true; explicit false
fails startup mapping validation). The private
`audit` object stays `enabled: false` and must contain no JWT. These history-only
fields do not add current-resource attribution fields.

Use `(publisher, event_id)` as collision-free history identity. Compare immutable
semantic content: action, subject resource type, projection data/tags/config,
and source audit metadata/content, including both actor refs. Exclude actor JWTs,
indexer-derived verification flags and retained identities, transport
authentication/tracing headers, and indexer-assigned ingestion timestamps.
Refreshed actor evidence must produce the same fingerprint and must not rewrite
the first append's attribution. Normalize object member order and
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

Before enabling writes, implement indefinite retention with targeted erasure/replay fences,
index permissions, exhausted-delivery monitoring, and reconciliation. Neither a
new event ID nor a new ingestion time may resurrect explicitly erased history.
Future retention-aware disposition must not accidentally prevent removal of a
deleted current resource; its explicit policy is a rollout prerequisite.

## Rollout, coexistence, and rollback

### Coordinated implementation of this revision

The cache backend from
[ops #1526](https://github.com/linuxfoundation/lfx-self-serve-ops/issues/1526)
has merged. Indexer-side attribution and query-service live enrichment are
implemented, and the retired platform protocol has been removed. Current-document
attribution/fixture cleanup and end-to-end rollout validation remain before #1527
can be completed.

Before enabling the revised contract:

- Update public types, strict wire preflight, builders and type comments together;
  reject publisher-supplied output fields, including false/null values.
- Add actor verification, credential stripping, first-append identity resolution
  and replay-without-resolution tests. Verify that refreshed evidence leaves
  fingerprints unchanged and that JWTs/name/avatar values do not escape into
  persisted attribution, receipts, general logs or post-indexing events.
- Update `opensearch-resource-history-v1.json` and mapping validation for the
  four searchable history attribution fields. Keep `audit` disabled for indexing.
- Update query-service's Goa design, generated code and
  `docs/audit-history-query.md` for retained identity, live display fields,
  availability and verification flags. Test lookup deduplication and failures.
- The retired subscription, configuration, header handling, transaction path and
  raw legacy-record payload field have been removed from the indexer. Its own
  Helm chart now provisions only the supported stream subject, and the application
  narrows the durable consumer filter. Check externally managed stream settings
  during rollout and finish mockdata's duplicated reset-mapping cleanup separately.
  This does not remove resource-service synchronization with legacy systems.
- Current-document attribution fields are removed as described above; do not
  replace them with history identities or verification fields in the current index.
- Run `make lint` and `make test` in each touched Go repository and validate
  chart/deployment/reset changes before rollout. Keep audited adoption disabled
  until these changes and the existing policy/provisioning gates are complete.

### Existing audited-ingestion rollout gates

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

The [history-query implementation](https://github.com/linuxfoundation/lfx-self-serve/issues/4618)
still needs the read-side attribution revision. Resource
[publisher adoption](https://github.com/linuxfoundation/lfx-self-serve/issues/4615)
and [authorization policy](https://github.com/linuxfoundation/lfx-self-serve/issues/4617)
remain separately owned. Contract publication is not an audit history start date.
