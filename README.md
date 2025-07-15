# LFX V2 Indexer Service

A high-performance Indexer Service for the LFX V2 platform that indexes resource transactions into OpenSearch. Built with clean architecture principles for maintainability and testability.

## 📋 Overview

The LFX V2 Indexer Service is responsible for:
- Processing resource transaction messages from NATS streams with queue group load balancing
- Enriching and validating transaction data with JWT authentication
- Indexing resources into OpenSearch for search capabilities
- Providing minimal HTTP endpoints for Kubernetes health checks
- Background maintenance tasks (janitor) for data consistency
- Supporting both LFX v2 and legacy v1 message formats
- Propagates data events to the rest of the platform (pending)

### Architecture

This service follows **clean architecture** principles with clear separation of concerns:

```
┌─────────────────────────────────────────────────────────────────┐
│                    LFX Indexer Service                         │
├─────────────────────────────────────────────────────────────────┤
│  Presentation Layer (Health Checks Only)                       │
│  ├─ /livez  - Kubernetes liveness probe                       │
│  ├─ /readyz - Kubernetes readiness probe                      │
│  └─ /health - General health check                            │
├─────────────────────────────────────────────────────────────────┤
│  Application Layer (Use Cases)                                 │
│  ├─ IndexingUseCase - Process NATS messages with queue groups │
│  └─ JanitorUseCase - Background maintenance tasks             │
├─────────────────────────────────────────────────────────────────┤
│  Domain Layer (Business Logic)                                 │
│  ├─ TransactionService - Core business logic                  │
│  ├─ Transaction Entity - Data structures                      │
│  └─ Repository Interfaces - Abstractions                      │
├─────────────────────────────────────────────────────────────────┤
│  Infrastructure Layer (External Services)                      │
│  ├─ NATS Repository - Message handling & queue groups         │
│  ├─ OpenSearch Repository - Document indexing                 │
│  └─ JWT Repository - Authentication                           │
└─────────────────────────────────────────────────────────────────┘
                                 │
                    ┌─────────────▼──────────────┐
                    │   External Dependencies    │
                    │                           │
                    │  NATS ←→ OpenSearch ←→ JWT │
                    │                           │
                    └───────────────────────────┘
```

### Message Flow

```
NATS Message → Queue Group → Validation → Enrichment → OpenSearch
                                 │
                                 ▼
                      Janitor Cleanup ← Background Process
```

### Components

- **Main Service** (`main.go`): Application entry point and lifecycle management
- **Domain Layer** (`internal/domain/`): Business entities, value objects, and domain services
- **Application Layer** (`internal/application/`): Use cases and application services
- **Infrastructure Layer** (`internal/infrastructure/`): External services and repositories
- **Presentation Layer** (`internal/presentation/`): Minimal health check handlers for Kubernetes
- **Configuration** (`internal/infrastructure/config/`): Environment-based configuration management
- **Dependency Injection** (`internal/container/`): IoC container and dependency wiring

## 📋 Prerequisites

- **Go 1.23+**
- **NATS Server** (for message streaming)
- **OpenSearch/Elasticsearch** (for indexing)
- **Heimdall** (for JWT authentication)

## 🚀 Quick Start

### 1. Environment Setup

```bash
# Set required environment variables
export NATS_URL=nats://nats:4222
export NATS_QUEUE=lfx.indexer.queue
export OPENSEARCH_URL=http://localhost:9200
export OPENSEARCH_INDEX=resources
export JWKS_URL=http://localhost:4457/.well-known/jwks

# Optional configuration
export LOG_LEVEL=debug
export PORT=8080
```

### 2. Install Dependencies

```bash
go mod download
```

### 3. Run the Service

```bash
# Development mode
make run

# Or build and run
make build-local
./bin/lfx-indexer
```

### 4. Health Check Endpoints

The service exposes minimal HTTP endpoints for Kubernetes:

```bash
# Liveness probe (always returns OK if service is running)
curl http://localhost:8080/livez

# Readiness probe (always returns OK if service is running)
curl http://localhost:8080/readyz

# General health endpoint (always returns OK if service is running)
curl http://localhost:8080/health
```

## 🔧 Configuration

All configuration is done via environment variables:

### Core Services
- `NATS_URL` - NATS server URL (default: `nats://nats:4222`)
- `OPENSEARCH_URL` - OpenSearch endpoint (default: `http://localhost:9200`)
- `JWKS_URL` - JWT validation endpoint (required)

### Message Processing
- `NATS_INDEXING_SUBJECT` - NATS subject for indexing (default: `lfx.index.>`)
- `NATS_V1_INDEXING_SUBJECT` - NATS subject for V1 indexing (default: `lfx.v1.index.>`)
- `NATS_QUEUE` - NATS queue group for load balancing (default: `lfx.indexer.queue`)
- `OPENSEARCH_INDEX` - OpenSearch index name (default: `resources`)

### NATS Connection
- `NATS_MAX_RECONNECTS` - Maximum reconnection attempts (default: `10`)
- `NATS_RECONNECT_WAIT` - Wait time between reconnects (default: `2s`)
- `NATS_CONNECTION_TIMEOUT` - Connection timeout (default: `30s`)

### Health Check Server
- `PORT` - Health check server port (default: `8080`)
- `READ_TIMEOUT` - HTTP read timeout (default: `5s`)
- `WRITE_TIMEOUT` - HTTP write timeout (default: `5s`)

### Background Services
- `JANITOR_ENABLED` - Enable janitor cleanup (default: `true`)
- `JANITOR_INTERVAL` - Cleanup interval (default: `5m`)
- `JANITOR_BATCH_SIZE` - Batch size for cleanup (default: `100`)

### OpenSearch Configuration
- `OPENSEARCH_USERNAME` - OpenSearch username (optional)
- `OPENSEARCH_PASSWORD` - OpenSearch password (optional)
- `OPENSEARCH_TIMEOUT` - Request timeout (default: `30s`)

### JWT Configuration
- `JWT_ISSUER` - JWT issuer for validation
- `JWT_AUDIENCE` - JWT audience for validation

## 🧪 Testing

```bash
# Run all tests
make test

# Run tests by layer
make test-domain
make test-application
make test-infrastructure
make test-presentation

# Run with coverage
make test-coverage

# Architecture compliance tests
make arch-test
```

## 🏗️ Development

### Project Structure

```
lfx-indexer-service/
├── main.go                         # Application entry point
├── internal/                       # Private application code
│   ├── domain/                     # Business logic layer
│   │   ├── entities/              # Business entities
│   │   ├── valueobjects/          # Value objects
│   │   ├── repositories/          # Repository interfaces
│   │   └── services/              # Domain services
│   ├── application/               # Application logic layer
│   │   └── usecases/              # Use cases and application services
│   ├── infrastructure/            # External services layer
│   │   ├── config/                # Configuration management
│   │   ├── opensearch/            # OpenSearch client
│   │   ├── nats/                  # NATS messaging with queue groups
│   │   └── jwt/                   # JWT authentication
│   ├── presentation/              # Presentation layer
│   │   └── handlers/              # Health check handlers
│   ├── container/                 # Dependency injection
│   └── mocks/                     # Mock implementations
├── Dockerfile                     # Container definition
├── run.sh                         # Development script
├── go.mod                         # Go dependencies
└── README.md                      # This file
```

### Building

```bash
# Local development
make build-local

# Cross-platform build
make build

# Docker build
make docker-build
```

### Code Quality

```bash
# Format code
make fmt

# Run linting
make lint

# Security checks
make security

# All quality checks
make quality
```

## 🐳 Docker

```bash
# Build Docker image
make docker-build

# Run in container
make docker-run

# Stop container
make docker-stop
```

### Health Status

The service provides simple health status through all health endpoints:

```json
{
  "status": "ready",
  "probe": "readiness"
}
```

All health endpoints (`/livez`, `/readyz`, `/health`) return `200 OK` if the service is running. For operational monitoring, rely on:

- **NATS server metrics** for message processing visibility
- **OpenSearch cluster health** for indexing status
- **Application logs** for troubleshooting and monitoring

### Logging

Structured logging with configurable levels:

```bash
# Set log level
export LOG_LEVEL=debug  # debug, info, warn, error

# Set log format
export LOG_FORMAT=json  # json, text
```

## 🔒 Security

- **JWT Authentication**: All messages validated against Heimdall
- **TLS Support**: Configurable TLS for all external connections
- **Input Validation**: Comprehensive validation of all inputs
- **Error Sanitization**: Safe error messages that don't leak sensitive data

## 📊 Performance

- **Queue Group Load Balancing**: Multiple instances share message processing load via NATS queue groups
- **Concurrent Processing**: Handles multiple NATS messages concurrently
- **Batch Operations**: Efficient batch processing for OpenSearch operations
- **Memory Management**: Optimized memory usage with proper cleanup
- **Connection Pooling**: Reuses connections for better performance

## 🔧 Operational Monitoring

Since this is a NATS message processor, monitoring should focus on:

- **NATS Metrics**: Message rates, queue depths, consumer lag
- **OpenSearch Health**: Index performance, cluster status
- **Application Logs**: Processing errors, authentication failures
- **System Metrics**: CPU, memory, network usage

The service logs all processing activities and errors for operational visibility.

## 🤝 Contributing

1. **Follow Clean Architecture**: Maintain clear separation between layers
2. **Write Tests**: Comprehensive unit tests for all components
3. **Document Changes**: Update README and code comments
4. **Security First**: Validate all inputs and sanitize outputs

## 📄 License

This project is licensed under the MIT License - see the LICENSE file for details.