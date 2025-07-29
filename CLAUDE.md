# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

This is a **Clean Architecture** implementation processing NATS messages into OpenSearch documents. The service handles both modern V2 messages (`lfx.index.*`) and legacy V1 messages (`lfx.v1.index.*`) with queue group load balancing.

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
NATS → MessagingRepository → IndexingMessageHandler → MessageProcessor → IndexerService → [Enricher] → OpenSearch
```

### Critical Patterns

**Message Routing**: Subject prefix determines version (`lfx.index.*` = V2, `lfx.v1.index.*` = V1)

**Enricher System**: Extensible data processing based on object type

- Current: `ProjectEnricher`, `ProjectSettingsEnricher`
- Register new enrichers in `IndexerService.NewIndexerService()`

**Authentication**:

- V2: JWT tokens via `Authorization` header (validated against Heimdall)
- V1: Simple `X-Username`/`X-Email` headers
- Delegation: `X-On-Behalf-Of` support

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

### Clean Architecture Rules

- Domain layer cannot import from infrastructure/presentation layers
- All external dependencies must use repository interfaces
- Business logic stays in domain services
- Dependency injection only in `cmd/lfx-indexer/main.go`

### Adding New Object Types

1. Add constant to `pkg/constants/messaging.go`
2. Create enricher in `internal/enrichers/` implementing `Enricher` interface
3. Register enricher in `IndexerService.NewIndexerService()`
4. Add tests for the new enricher

### Message Processing Requirements

- Always reply to NATS messages with "OK" or "ERROR: details"
- Log comprehensive context for debugging (action, object_type, message_id)
- Handle both V2 (past-tense actions) and V1 (present-tense actions) formats
- Support base64-encoded data fields

### Testing Strategy

- Use layer-specific make targets for focused testing
- Mock external dependencies using interfaces in `internal/mocks/`
- Test both V2 and V1 message formats where applicable
- Include race detection in all test runs

## Key Files for Understanding the System

- `internal/domain/services/indexer_service.go`: Core business logic
- `internal/application/message_processor.go`: Message workflow orchestration  
- `internal/domain/contracts/transaction.go`: Core business entities
- `internal/enrichers/registry.go`: Enricher registration and lookup
- `internal/infrastructure/config/app_config.go`: Configuration management
- `cmd/lfx-indexer/main.go`: Dependency injection and service startup
