# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> **Central LFX skills:**
> - Start with `/lfx-skills:lfx` for cross-repo tasks, "where does X live" questions, owner/peer repo routing, or missing checkouts.
> - Use `/lfx-skills:lfx-platform-architecture` after routing when you need platform composition, V2 service classes, write/read/access-check/indexing flows, NATS/KV ownership, or handoff points across FGA, indexer, query, Heimdall, OpenFGA, Helm, or ArgoCD.
> - The repo-local `indexer-service-dev` skill auto-attaches on Go paths (`cmd/`, `internal/`, `pkg/`, `**/*.go`, `Makefile`, `go.mod`, `go.sum`). It owns the layer boundaries, logging wrapper, NATS subscriber pattern, OpenSearch storage pattern, mocks layout, layer-specific test targets, errors, formatting, lint, and license headers for this repo.
> - This repo OWNS the indexer event contract. Other V2 services route here to publish index events. The authoritative contract lives in `docs/indexer-contract.md`; the publisher workflow is the local `.claude/skills/indexer-publishing/` skill; contract-sensitive paths are covered by the local `.claude/rules/indexer-contract.md` rule.
> - Repo-owned docs under `docs/` are canonical for the generic indexer contract and caller examples.
> - If the plugin is missing, install with `/plugin marketplace add linuxfoundation/lfx-skills` then `/plugin install lfx-skills@lfx-skills`.

## Essential Commands

### Development Workflow

```bash
# Setup and build
make setup                    # Download dependencies and setup environment
make build-local             # Build for local development
make run                     # Run service locally with environment setup

# Code quality (run before committing)
make lint                    # Run golangci-lint (install if needed)
make test                    # Run all tests with race detection and coverage
make quality                 # Run fmt, vet, lint, and test together

# Layer-specific testing
make test-domain             # Test business logic only
make test-infrastructure     # Test external integrations
make test-application        # Test workflow coordination
make test-presentation       # Test message handlers

# Production builds
make build                   # Build for Linux deployment (Docker)
make docker-build           # Build container image
make helm-install           # Deploy using Helm chart
```

### Debugging and Health Checks

```bash
# Configuration validation
./bin/lfx-indexer -check-config

# Health endpoints
curl http://localhost:8080/livez    # Kubernetes liveness probe
curl http://localhost:8080/readyz   # Kubernetes readiness probe
curl http://localhost:8080/health   # General health status

# Debug logging
LOG_LEVEL=debug make run
```

## Architecture Overview

This is a **Clean Architecture** implementation processing NATS messages into OpenSearch documents. The service handles both modern V2 messages (`lfx.index.>`) and legacy V1 messages (`lfx.v1.index.>`) with queue group load balancing.

## Agent Guidance

Repo-owned guidance is split between contract docs and the local skills:

- `docs/indexer-contract.md`: authoritative event contract (envelope, `IndexingConfig`, OpenSearch document fields, schema evolution policy). Other V2 services link here rather than copy.
- `docs/client-guide.md`: publisher-facing usage walkthrough (for publishers in other services).
- `.claude/skills/indexer-service-dev/`: repo-local Go conventions (auto-attaches on Go paths).
- `.claude/skills/indexer-publishing/`: workflow for any change on the indexer write path (both indexer-internal and publishers in other repos).
- `.claude/rules/indexer-contract.md`: enforced policy on contract-sensitive paths (action constants, additive envelope changes, no new top-level OpenSearch fields without coordination).

Read the contract doc before changing index message handling or advising resource services on indexer publishing.

## Consumed Cross-Repo Contracts

This repo depends on contracts owned elsewhere. Do not copy or infer them from
local examples. Read the owner file before changing access coordination, query
coordination, or publisher guidance.

- Generic FGA envelope and access-check contract:
  `lfx-v2-fga-sync/docs/fga-sync-contract.md`
- Query-service read behavior over indexed documents:
  `lfx-v2-query-service/docs/query-service-contract.md`
- Per-resource indexer emission contracts:
  `<resource-service>/docs/indexer-contract.md`

Use `/lfx-skills:lfx` if an owner repo is missing locally, the path has moved,
or the task needs additional peer repos.

### Layer Boundaries (Dependency Rule: Inner layers cannot depend on outer layers)

1. **Domain Layer** (`internal/domain/`): Pure business logic and interfaces
   - `contracts/`: Core entities (`LFXTransaction`, `TransactionBody`) and repository interfaces
   - `services/indexer_service.go`: Central business logic (1100+ lines)

2. **Application Layer** (`internal/application/`): Use case orchestration
   - `message_processor.go`: Coordinates V2/V1 message processing workflows

3. **Infrastructure Layer** (`internal/infrastructure/`): External service adapters
   - `messaging/`: NATS client with queue group support
   - `storage/`: OpenSearch document indexing
   - `auth/`: JWT validation against Heimdall service
   - `cleanup/`: Background conflict resolution

4. **Presentation Layer** (`internal/presentation/handlers/`): Protocol concerns
   - `indexing_message_handler.go`: NATS message handling
   - `health_handler.go`: Kubernetes health endpoints

### Message Processing Flow

```
NATS → MessagingRepository → IndexingMessageHandler → MessageProcessor → IndexerService → OpenSearch
                                                                                                              ↓
                                                                                                    NATS Event Published
                                                                                                  lfx.{object_type}.{action}
```

### Domain Events (Outbound)

After every successful OpenSearch write the service publishes a NATS event on
`lfx.{object_type}.{action}`. Envelope shape, `IndexingEvent` payload, wildcard
subscriptions, and failure semantics are documented in the authoritative
`docs/indexer-contract.md`. Cross-service topology lives in the
central `/lfx-skills:lfx-platform-architecture` skill.

### Critical Patterns

**Message Routing**: Subject prefix determines version (`lfx.index.` = V2, `lfx.v1.index.` = V1)

**Authentication**:

- V2: JWT tokens via the lower-case `authorization` header key in the NATS JSON envelope (validated against Heimdall)
- V1: Simple `x-username`/`x-email` headers
- Delegation: `x-on-behalf-of` support

**Configuration**: CLI flags > Environment variables > Defaults

## Required Environment Variables

```bash
# Core services (required)
NATS_URL=nats://nats:4222
OPENSEARCH_URL=http://localhost:9200
JWKS_URL=http://localhost:4457/.well-known/jwks

# Message processing
NATS_QUEUE=lfx.indexer.queue
OPENSEARCH_INDEX=resources
NATS_INDEXING_SUBJECT=lfx.index.>
NATS_V1_INDEXING_SUBJECT=lfx.v1.index.>

# Optional configuration
PORT=8080
LOG_LEVEL=info
JANITOR_ENABLED=true
```

## Development Guidelines

Layer boundaries, NATS handler patterns, OpenSearch storage patterns, logging, errors, tests, formatter, lint, and license headers are all owned by `.claude/skills/indexer-service-dev/SKILL.md` (auto-attaches on Go paths).

### Adding New Object Types

No indexer changes are needed for new object types. The indexer is data-agnostic: publishers send messages with any non-empty `object_type` and a valid `indexing_config` block; the indexer stores and indexes the document without resource-specific logic. Domain events (`lfx.{object_type}.created/updated/deleted`) are emitted automatically.

Adding a new object type lives on the publisher side. See the `indexer-publishing` skill and `docs/indexer-contract.md`.

## Key Files for Understanding the System

- `README.md`: Full project-structure tree, mermaid sequence and data-flow diagrams, CLI flag reference, and troubleshooting guide. Read first when orienting in the repo.
- `internal/domain/services/indexer_service.go`: Core business logic
- `internal/application/message_processor.go`: Message workflow orchestration
- `internal/domain/contracts/transaction.go`: Core business entities (`LFXTransaction`, `TransactionBody`)
- `internal/domain/contracts/events.go`: `IndexingEvent`, the outbound domain event payload
- `pkg/types/indexing_config.go`: Public client surface (`IndexerMessageEnvelope`, `IndexingConfig`)
- `internal/infrastructure/config/app_config.go`: Configuration management
- `cmd/lfx-indexer/main.go`: Dependency injection and service startup
