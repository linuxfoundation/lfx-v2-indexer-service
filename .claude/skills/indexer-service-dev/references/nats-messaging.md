<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# NATS Messaging (indexer-service consumer)

**Read first:** `/lfx-skills:lfx-platform-architecture` for NATS/KV ownership
and platform handoff context, then path-scoped `indexer-service-dev` guidance for NATS
coding conventions. The notes below cover only what is specific to
indexer-service as the consumer of index events and the emitter of domain
events.

## indexer-service subscriptions

The indexer subscribes with a single wildcard per version, both bound to the same
queue group so only one replica handles each message when scaled horizontally.

| Subject pattern | Version | Queue group | Auth |
| --- | --- | --- | --- |
| `lfx.index.>` | V2 | `lfx.indexer.queue` | JWT (`authorization` header key) |
| `lfx.v1.index.>` | V1 (legacy) | `lfx.indexer.queue` | `x-username` / `x-email` headers |

Subject prefix selects the message version. V2 messages use past-tense actions
(`created`, `updated`, `deleted`); V1 messages use present-tense actions. Both
subjects and the queue group are overridable via `NATS_INDEXING_SUBJECT`,
`NATS_V1_INDEXING_SUBJECT`, and `NATS_QUEUE`.

## Reply and outbound events

- Reply: `"OK"` on success, `"ERROR: <details>"` on failure, sent only when the
  message includes a reply subject (request/reply). A plain `Publish` with no reply
  inbox receives no reply, so use request/reply to detect handler failure.
- After every successful OpenSearch write the service publishes a domain event
  on `lfx.{object_type}.{action}` (e.g. `lfx.project.created`). Publish errors
  are non-blocking. See `internal/domain/contracts/events.go` for the
  `IndexingEvent` payload.

For the index envelope (`IndexerMessageEnvelope`) and `IndexingConfig` fields,
see `lfx-v2-indexer-service/docs/indexer-contract.md`. Both
structs are defined in `lfx-v2-indexer-service/pkg/types/indexing_config.go`
(public client surface) and imported by `internal/domain/contracts/transaction.go`.
