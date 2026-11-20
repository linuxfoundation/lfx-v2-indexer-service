<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Go Conventions Checklist (lfx-v2-indexer-service)

Quick checklist form. The narrative lives in `../SKILL.md`. Use this file in a code review pass.

## Layer boundaries

- [ ] No import from `internal/domain/**` into `internal/infrastructure/**` or `internal/presentation/**`.
- [ ] External systems (NATS, OpenSearch, Heimdall) reached only through interfaces in `internal/domain/contracts/`.
- [ ] New external adapters land in `internal/infrastructure/<adapter>/`.
- [ ] Dependency injection edits stay in `cmd/lfx-indexer/main.go` and `internal/container/`.

## Contracts and constants

- [ ] No inline action string literals. Use `pkg/constants/` (`ActionCreated`, `ActionUpdated`, `ActionDeleted`).
- [ ] No new top-level OpenSearch field or in-place envelope mutation without updating `docs/indexer-contract.md` and coordinating with `lfx-v2-query-service`.
- [ ] Subject strings (`lfx.index.>`, `lfx.{object_type}.{action}`) live in `pkg/constants/` or runtime config.

## Logging

- [ ] Uses `pkg/logging/` wrapper. No `fmt.Println`, `fmt.Printf`, `log.Print*`, or `log.Println` in runtime paths.
- [ ] Includes stable fields where available: `request_id`, `principal`, `subject`, `object_type`, `object_id`, `action`, `message_id`, `operation`.
- [ ] Never logs JWTs, bearer headers, secrets, or raw payloads that may contain PII.

## NATS handler

- [ ] Replies `"OK"` on success, `"ERROR: <details>"` on failure when the message carries a reply subject; no reply is sent for a plain publish.
- [ ] Rejects unsupported subjects; audit admission stays in the message processor.
- [ ] Production uses the `indexer-consumer` JetStream durable on `index-events`, filtered by `NATS.IndexingSubject`. Queue-group subscriptions (`lfx.indexer.queue`) are compatibility helpers only.
- [ ] Connection drain runs through the existing shutdown path; no ad-hoc `nc.Close()`.

## OpenSearch storage

- [ ] Storage code goes through the storage repository interface, not the OpenSearch client directly.
- [ ] Indexer writes go through `contracts.DocumentIndexer.Index` (`StorageRepository` or a `BatchIndexer` wrapper) with `object_ref` as the OpenSearch document ID and `latest: true`.
- [ ] Search-shaped fields come from `IndexingConfig`. New keys inside `data` are free; new top-level fields are not.

## Domain events

- [ ] Preserve best-effort `lfx.{object_type}.{action}` events for legacy current writes. Audit v2 publishes only after current-body replacement, not receipts/history, guarded no-ops, identity-only updates or replay that only restores history. Use `IndexingEvent` from `internal/domain/contracts/events.go`.
- [ ] Publish failures are logged but never fail the OpenSearch write path.

## Auth

- [ ] Handler layer extracts auth headers. Domain services read from request context only, never headers directly.
- [ ] V2 paths validate JWT via Heimdall; no unsigned legacy-header bypass.
- [ ] `x-on-behalf-of` is forwarded through context.

## Errors

- [ ] Uses the existing domain error type and helpers. No parallel sentinel-error family.
- [ ] Wraps upstream errors so `errors.Is` and `errors.Unwrap` still work.
- [ ] Translates errors to NATS reply at the handler boundary, not in domain services.

## Tests

- [ ] `*_test.go` co-located with the code under test.
- [ ] Mocks live in `internal/mocks/`.
- [ ] Table-driven tests for branching behavior.
- [ ] Supported V2 envelopes, unsupported subjects/actions, and legacy-backed resource names covered.
- [ ] Layer-specific target runs clean: `make test-domain`, `make test-application`, `make test-infrastructure`, `make test-presentation`. `make test` clean before handoff.

## Formatting, lint, headers

- [ ] `make fmt` and `make lint` clean. Prefer `make quality` pre-PR.
- [ ] New Go files carry the Copyright + SPDX `MIT` header.
- [ ] Exported symbols documented where the linter requires it.
- [ ] Repo-owned docs and contracts updated in the same PR as behavior changes.
