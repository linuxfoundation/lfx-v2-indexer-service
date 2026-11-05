<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# NATS Messaging (indexer-service consumer)

**Read first:** `/lfx-skills:lfx-platform-architecture` for NATS/KV ownership
and platform handoff context, then path-scoped `indexer-service-dev` guidance for NATS
coding conventions. The notes below cover only what is specific to
indexer-service as the consumer of index events and the emitter of domain
events.

## indexer-service subscriptions

The deployed consumer uses the shared `index-events` JetStream stream and
`indexer-consumer` durable consumer, filtering both versioned subject patterns.
Core-NATS queue/reply helpers remain for compatibility, not the deployed audit path.

| Subject pattern | Version | Legacy helper queue group | Auth |
| --- | --- | --- | --- |
| `lfx.index.>` | V2 | `lfx.indexer.queue` | JWT (`authorization` header key) |
| `lfx.v1.index.>` | V1 (legacy) | `lfx.indexer.queue` | `x-username` / `x-email` headers |

Subject prefix selects the message version. V2 messages use past-tense actions
(`created`, `updated`, `deleted`); V1 messages use present-tense actions. Both
subjects and the queue group are overridable via `NATS_INDEXING_SUBJECT`,
`NATS_V1_INDEXING_SUBJECT`, and `NATS_QUEUE`.

## Reply and outbound events

- JetStream capture returns a broker persistence `PubAck`, not the indexer's
  processing result. Do not use core request/reply to infer successful audited
  processing. Legacy reply helpers use `"OK"`/`"ERROR: <details>"` only when
  explicitly invoked with a reply callback.
- Legacy current writes attempt `lfx.{object_type}.{action}` (for example,
  `lfx.project.created`). Audit v2 attempts it only after actual current-body
  replacement; receipt/history appends, guarded no-ops and metadata-only updates
  intentionally do not publish. Failures are non-blocking. See
  `internal/domain/contracts/events.go` and the owner contract for the payload.

For the index envelope (`IndexerMessageEnvelope`) and `IndexingConfig` fields,
see `lfx-v2-indexer-service/docs/indexer-contract.md`. Both
structs are defined in `lfx-v2-indexer-service/pkg/types/indexing_config.go`
(public client surface) and imported by `internal/domain/contracts/transaction.go`.
