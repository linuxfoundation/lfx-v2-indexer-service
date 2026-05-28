<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Indexer Event Contract (Authoritative)

This is the **owner document** for the indexer event envelope, the OpenSearch document
shape, and the rules the indexer enforces on incoming messages. Other services link
here rather than copy.

The indexer subscribes to `lfx.index.>` (V2) and `lfx.v1.index.>` (V1) NATS subjects
on a queue group, writes documents into OpenSearch, and emits domain events on
`lfx.{object_type}.{action}`. It is fully generic; resource services tell it
everything it needs via the message payload.

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

V1 publishers use `lfx.v1.index.{resource_type}` and present-tense actions
(`create`/`update`/`delete`); V2 uses past-tense (`created`/`updated`/`deleted`).
The service subscribes to `lfx.v1.index.>` for legacy traffic. Use the constants
in `pkg/constants/` rather than string literals.

## Envelope: `IndexerMessageEnvelope`

Defined in the public `pkg/types/indexing_config.go` package (imported by
`internal/domain/contracts/transaction.go`).

```go
type IndexerMessageEnvelope struct {
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

| Condition | Outcome |
|---|---|
| Missing `IndexingConfig` on create/update | Message rejected with error reply |
| Empty `ObjectID` | Rejected (no primary key) |
| Empty `AccessCheckObject` or `AccessCheckRelation` | Rejected during `IndexingConfig` parsing |
| Empty `HistoryCheckObject` or `HistoryCheckRelation` | Rejected during `IndexingConfig` parsing |
| Unknown `action` value | Rejected with `unknown action` |
| Missing lower-case `authorization` header on V2 messages | Rejected during header validation |
| `data` not present on create/update | Rejected |
| `data` not a string on delete | Rejected |
| Subject suffix empty (`lfx.index.` with no type) | Rejected (must have a non-empty resource type) |
| Subject suffix contains `.`, `*`, `>`, whitespace, or equals `index` | Rejected (invalid or reserved object type) |

NATS replies are mandatory: handlers reply `OK` on success or `ERROR: <details>` on
failure. Publishers should use request/reply to confirm processing during writes that
require acknowledgement.

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
| `history_check_query` | `{HistoryCheckObject}#{HistoryCheckRelation}` | Used by query-service for audit views |
| `latest` | Set by indexer | `true` for current version only |
| `created_at`, `updated_at`, `deleted_at` | server timestamp by action | Audit and sorting timestamps |
| `created_by`, `updated_by`, `deleted_by` | parsed principals | Human-readable principal refs |
| `created_by_principals`, `updated_by_principals`, `deleted_by_principals` | parsed principals | Principal IDs for filtering/audit |
| `created_by_emails`, `updated_by_emails`, `deleted_by_emails` | parsed principals | Principal email addresses |
| `data` | `IndexerMessageEnvelope.Data` on create/update | Full resource data (schema-free `flat_object`) |
| `v1_data` | V1 payload only | Legacy Platform DB data retained for V1 imports |

### Field-naming rules

- All top-level field names are `snake_case`. No camelCase, no dashes, no spaces.
- Reserve `object_*`, `access_check_*`, `history_check_*`, `parent_refs`, `latest`,
  `sort_name`, `name_and_aliases`, `public`, `data`, `v1_data`, `tags`, `fulltext`,
  `contacts`, and the audit fields (`created_*`, `updated_*`, `deleted_*`) for this
  contract. Resource-specific data lives inside `data`.
- Anything in `data` is dynamic. OpenSearch maps new keys via `flat_object`, but
  you cannot rely on per-field analyzers there. Use the search-shaped fields above
  when you need analysis or aggregation.

### Current-document and janitor behavior

The current write path uses the OpenSearch Index API with `object_ref` as the document ID.
Each create, update, or delete writes the current document for that object reference and
sets `latest: true`; deletes write a tombstone-shaped document with `deleted_at` and
delete principals. The janitor still scans for duplicate `latest: true` hits with the
same `object_ref` and flips older duplicates to `latest: false` using optimistic update
parameters. Queries must filter `latest: true` to see current data.

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

## Schema Evolution Policy

- **Adding fields inside `data`**: free. `flat_object` accepts new keys without a
  mapping change. If the field needs to be searchable/filterable, route it through
  `IndexingConfig` (Tags / NameAndAliases / Fulltext) instead of relying on `data`.
- **Adding top-level fields to the OpenSearch document**: requires an indexer-service
  change. Do not invent new top-level fields in a publisher.
- **Renaming or removing top-level fields**: breaking. Coordinate with the indexer
  team and the query-service team; existing documents must be re-indexed.
- **Changing `IndexingConfig` shape**: breaking for all publishers. Coordinate.
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
4. The publisher waits for `OK` on the reply when the write must be confirmed
   (or accepts fire-and-forget for non-critical writes; document which).
5. Headers carry lower-case `authorization` and `x-on-behalf-of` keys so audit principals are
   recorded on the indexed document.

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
