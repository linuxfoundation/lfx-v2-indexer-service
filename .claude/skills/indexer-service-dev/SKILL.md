---
name: indexer-service-dev
description: Auto-attaches when editing Go code in lfx-v2-indexer-service. Owns repo-local Go conventions for this Clean Architecture NATS consumer that writes OpenSearch documents and emits domain events. Covers layer boundaries, the logging wrapper, NATS subscriber and OpenSearch storage patterns, mocks layout, layer-specific test targets, license headers, formatter, and linter. Does not own the indexer event contract (see `docs/indexer-contract.md` and the `indexer-contract` rule) or cross-repo platform topology (see `/lfx-skills:lfx-platform-architecture`).
paths:
  - "**/*.go"
  - "go.mod"
  - "go.sum"
  - "Makefile"
  - "cmd/**"
  - "internal/**"
  - "pkg/**"
  - ".claude/skills/indexer-service-dev/**"
allowed-tools: Read, Glob, Grep, Edit, Write, Bash
---

<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Development Conventions (lfx-v2-indexer-service)

Repo-local Go conventions for the indexer service. This service consumes NATS index messages on `lfx.index.>` (V2) and `lfx.v1.index.>` (V1), writes documents into OpenSearch, and emits domain events on `lfx.{object_type}.{action}`. Conventions below apply to this repo's own Go code.

## What this skill owns vs. routes elsewhere

- This skill: how Go files in `cmd/`, `internal/`, and `pkg/` should be structured here.
- `docs/indexer-contract.md` (in this repo): authoritative event contract (envelope, `IndexingConfig`, OpenSearch document shape, schema evolution). Other services link here.
- `.claude/rules/indexer-contract.md` (in this repo): enforced contract policy on contract-sensitive paths (action constants, additive envelope changes, no new top-level OpenSearch fields without coordination).
- `.claude/skills/indexer-publishing/SKILL.md` (in this repo): publisher workflow used by both indexer-internal changes and by resource services that emit index events.
- `/lfx-skills:lfx-platform-architecture`: platform composition, V2 service classes, cross-service handoffs, NATS/KV ownership at the platform level.
- `/lfx-skills:lfx`: cross-repo routing and "where does X live".

## Clean Architecture layers (dependency rule: inner cannot import outer)

| Layer | Path | Holds |
|---|---|---|
| Domain | `internal/domain/` | Pure business logic; entities in `contracts/` (`LFXTransaction`, `TransactionBody`, `IndexingEvent`); core service in `services/indexer_service.go`. Note: `IndexerMessageEnvelope` and `IndexingConfig` live in `pkg/types/indexing_config.go` (public client surface) and are imported by `contracts/transaction.go`. |
| Application | `internal/application/` | Use-case orchestration. `message_processor.go` coordinates V2 and V1 workflows. |
| Infrastructure | `internal/infrastructure/` | Adapters: `messaging/` (NATS client + queue groups), `storage/` (OpenSearch), `auth/` (Heimdall JWT), `cleanup/` (background janitor), `config/`. |
| Presentation | `internal/presentation/handlers/` | Protocol concerns: `indexing_message_handler.go`, `health_handler.go`. |

Rules:

- `internal/domain/**` must not import `internal/infrastructure/**` or `internal/presentation/**`.
- External systems are reached only through interfaces declared in `internal/domain/contracts/`.
- Dependency injection lives in `cmd/lfx-indexer/main.go` (and the wiring in `internal/container/`). No global singletons.
- Place new business logic in domain services; place new transport wiring in presentation; place new external adapters in infrastructure.

## Generated code and contracts

- This repo has no `gen/` or `api/` directory (not a Goa service). Do not introduce one.
- Treat `internal/domain/contracts/`, `pkg/types/`, and OpenSearch document-building code as contract surfaces. The `indexer-contract` rule auto-attaches there and forbids inline action strings, in-place envelope mutation, and unilateral new top-level OpenSearch fields. Use the constants in `pkg/constants/`.
- When the envelope or document shape changes, update `docs/indexer-contract.md` in the same PR. Publishers across the platform read it.

## Logging

- Use the repo wrapper in `pkg/logging/` (built on `log/slog`). Do not call `fmt.Println`, `fmt.Printf`, `log.Print*`, or `log.Println` for runtime logging.
- Include stable structured fields when available: `request_id`, `principal`, `subject`, `object_type`, `object_id`, `action`, `message_id`, `operation`.
- Do not log tokens, bearer headers, JWT contents, private keys, or raw payloads that may contain PII.
- Honor `LOG_LEVEL` (set via env, e.g. `LOG_LEVEL=debug make run`).

## NATS subscriber patterns (this consumer)

- Subscribe via the repo's `MessagingRepository` interface (`internal/infrastructure/messaging/`). Do not call the raw NATS client from handlers or services.
- Both V2 (`lfx.index.>`) and V1 (`lfx.v1.index.>`) subjects bind to the same queue group `lfx.indexer.queue` so only one replica processes each message when scaled horizontally.
- Subject prefix selects the version. Route on prefix in the handler, not on payload sniffing.
- Reply only when the incoming message includes a reply subject (request/reply): `"OK"` on success, `"ERROR: <details>"` on failure. A plain `Publish` with no reply inbox receives no reply. The handler builds a reply func only when `msg.Reply` is non-empty (see `internal/infrastructure/messaging/messaging_repository.go`).
- Drain NATS connections during graceful shutdown through the existing shutdown path in `cmd/lfx-indexer/main.go`. Do not bypass it.
- Subject strings live in `pkg/constants/` or runtime config. Never hardcode `lfx.index.>`, `lfx.v1.index.>`, or `lfx.{object_type}.{action}` at call sites.

For the consumer subjects, queue group, and outbound event semantics in one place, see `references/nats-messaging.md`.

## OpenSearch storage patterns

- Storage logic lives in `internal/infrastructure/storage/`. Domain services depend on the storage repository interface, not on the OpenSearch client directly.
- The current write path calls `StorageRepository.Index` with `object_ref` as the OpenSearch document ID and `latest: true`. The background janitor (`internal/infrastructure/cleanup/`) reconciles duplicate `latest: true` hits for the same `object_ref` by flipping older hits to `latest: false`. Queries filter `latest: true`.
- Top-level document fields and their sources are fixed by the contract document. Do not add a new top-level field in this repo's storage code without updating `docs/indexer-contract.md` and coordinating with `lfx-v2-query-service`.
- `data` is `flat_object`. New keys inside `data` are free. Search-shaped fields (`name_and_aliases`, `tags`, `fulltext`, `sort_name`, `parent_refs`) come from `IndexingConfig`.

## Domain events (outbound)

- After every successful OpenSearch write, publish `lfx.{object_type}.{action}` with the `IndexingEvent` payload from `internal/domain/contracts/events.go`.
- Publish failures here are non-blocking: log the error, do not fail the OpenSearch write path.
- The full envelope, payload, and subscription rules are documented in `docs/indexer-contract.md`.

## Authentication

- V2 messages carry a JWT in the lower-case `authorization` header key validated against Heimdall (`internal/infrastructure/auth/`).
- V1 messages carry `x-username` and `x-email` headers.
- `x-on-behalf-of` is supported for delegation. Forward it through context.
- Middleware (the handler layer) owns auth extraction. Service-layer code should not read headers directly; it reads from request context via typed keys.

## Errors

- Use the existing domain error type and helper constructors. Do not introduce a parallel sentinel-error family.
- Wrap upstream errors so `errors.Is` and `errors.Unwrap` still work.
- Translate domain errors to NATS reply strings (`"OK"` or `"ERROR: <details>"`) at the handler boundary, not in domain services.

## Tests

- Co-locate `*_test.go` files with the code under test.
- Depend on interfaces for external systems: messaging, storage, auth, ID mappers. Mocks live in `internal/mocks/`. Add new mocks there.
- Use table-driven tests for branching behavior; prefer one test function per exported method with cases in the table.
- Test both V2 and V1 message formats wherever the code path differs.
- Run the appropriate layer-specific target during development; run `make test` (race detection + coverage) before handoff.

| Target | Scope |
|---|---|
| `make test-domain` | Business logic in `internal/domain/`. |
| `make test-application` | Workflow coordination in `internal/application/`. |
| `make test-infrastructure` | External adapters in `internal/infrastructure/`. |
| `make test-presentation` | Handlers in `internal/presentation/`. |
| `make test` | All of the above, race detection, coverage. |

## Formatting, linting, license headers

- Run `make fmt` (gofmt + goimports) and `make lint` (`golangci-lint`, configured in `.revive.toml` and the linter setup) before committing Go changes.
- `make quality` runs fmt, vet, lint, and test together. Prefer it for pre-PR checks.
- New Go files must carry the existing license header (Copyright The Linux Foundation, SPDX `MIT`). Check an existing sibling file for the exact format.
- Document exported Go symbols when the linter requires it. Implementation comments only where the code is not self-explanatory.
- Update repo-owned docs and contracts in the same PR as code that changes behavior.

## References

- `references/go-development-conventions.md` (~70 lines): condensed Go conventions checklist for quick review.
- `references/nats-messaging.md` (~40 lines): indexer consumer subjects, queue group, reply contract, and outbound event subject pattern.
