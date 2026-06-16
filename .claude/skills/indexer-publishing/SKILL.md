---
name: indexer-publishing
description: >
  Use for any change on the indexer write path: a resource service emitting to
  the LFX indexer (`lfx.index.<resource_type>`; consumed by `lfx.index.>`),
  building or changing an
  `IndexerMessageEnvelope`, touching `IndexingConfig`, modifying OpenSearch
  document fields, or editing the indexer's own ingestion code
  (`internal/domain/services/indexer_service.go`,
  `internal/application/message_processor.go`). Read access (querying) is
  owned by `lfx-v2-query-service`, not this skill.
allowed-tools: Read, Glob, Grep, Edit
paths:
  - 'internal/domain/**'
  - 'internal/application/**'
  - 'internal/presentation/**'
  - 'pkg/constants/**'
  - 'docs/indexer-contract.md'
  - 'docs/client-guide.md'
---

# Indexer Publishing

The indexer is generic. Publishers tell it everything via the envelope. Follow this workflow for any change that touches the contract.

## Workflow

1. **Read the authoritative contract** in
   `references/indexer-patterns.md` (link to
   `docs/indexer-contract.md`). Confirm the change does not
   introduce a new top-level OpenSearch field, rename an existing one, or
   change the `IndexingConfig` shape.
2. **Identify the publisher** (usually a resource service's
   `internal/infrastructure/nats/messaging_publish.go` or equivalent). For
   indexer-internal changes, the source is `internal/domain/services/` and
   `internal/application/message_processor.go`.
3. **Validate the envelope** every call path produces:
   - `Action` uses a `constants.Action*` constant, never a string literal.
   - `Headers` carries lower-case `authorization` and `x-on-behalf-of` keys
     from request context.
   - `Data` is the full resource on create/update, the bare UID string on
     delete.
   - Create/update messages include a non-nil `IndexingConfig` that populates `ObjectID`,
     `AccessCheckObject`, `AccessCheckRelation`, `HistoryCheckObject`, and
     `HistoryCheckRelation`. Otherwise the indexer rejects the message while
     parsing `IndexingConfig`.
4. **Choose the right search field** for new attributes per the table in
   `references/indexer-patterns.md` (`NameAndAliases`, `Tags`, `Fulltext`).
   Adding new keys inside `data` requires no schema change.
5. **For deletes**, send `Action = ActionDeleted` and `Data` = the UID
   string. The handler will reject a full-resource delete.
6. **Update the per-service contract doc** at `docs/indexer-contract.md` in
   the owning service in the same PR as the publisher change.
7. **For indexer-internal changes**, ensure NATS replies remain `"OK"` on
   success and `"ERROR: <details>"` on failure, and that domain events on
   `lfx.{object_type}.{action}` continue to fire after every successful
   OpenSearch write.
8. **Schema evolution**: adding a top-level OpenSearch field is breaking.
   Coordinate with the query-service team and the indexer contract owner before
   changing storage or publisher code.

## Anti-patterns

- Hardcoded subject strings outside `pkg/constants/`.
- Sending `IndexingConfig: nil` and expecting the indexer to fill defaults.
- Mixing V1 (`lfx.v1.index.>`, present-tense actions) and V2 (`lfx.index.>`,
  past-tense actions) handler logic.
- Writing OpenSearch documents directly from a publisher. Indexer-service owns
  current-document writes under the `object_ref` document ID and emits the
  post-index domain event.

## References

- `references/indexer-patterns.md`: the authoritative event contract.
