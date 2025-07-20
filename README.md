# LFX V2 Indexer Service

A high-performance, indexer service for the LFX V2 platform that processes resource transactions into OpenSearch with comprehensive NATS message processing and queue group load balancing.

## 📋 Overview

The LFX V2 Indexer Service is responsible for:
- **Message Processing**: NATS stream processing with queue group load balancing across multiple instances
- **Transaction Enrichment**: JWT authentication, data validation, and principal parsing with delegation support
- **Search Indexing**: High-performance OpenSearch document indexing with optimistic concurrency control  
- **Data Consistency**: Event-driven janitor service for conflict resolution (production-proven pattern)
- **Dual Format Support**: Both LFX v2 (past-tense actions) and legacy v1 (present-tense actions) message formats
- **Health Monitoring**: Kubernetes-ready health check endpoints
- **Clean Architecture**: Maintainable, testable code following clean architecture principles

## 🚀 Quick Start

### Prerequisites
- **Go 1.23+**
- **NATS Server** (message streaming)  
- **OpenSearch/Elasticsearch** (document indexing)
- **Heimdall JWT Service** (authentication)

### Environment Setup
```bash
# Core Services (Required)
export NATS_URL=nats://nats:4222
export OPENSEARCH_URL=http://localhost:9200
export JWKS_URL=http://localhost:4457/.well-known/jwks

# Message Processing
export NATS_QUEUE=lfx.indexer.queue
export OPENSEARCH_INDEX=resources

# Optional Configuration  
export LOG_LEVEL=info
export PORT=8080
```

### Install & Run
```bash
# Install dependencies
go mod download

# Development mode
make run

# Build and run
make build-local
./bin/lfx-indexer

# Direct Go commands
go run ./cmd/lfx-indexer
go build -o bin/lfx-indexer ./cmd/lfx-indexer

# Command-line options
./bin/lfx-indexer -help
./bin/lfx-indexer -check-config
```

### Health Check
```bash
# Kubernetes probes
curl http://localhost:8080/livez    # Liveness probe
curl http://localhost:8080/readyz   # Readiness probe  
curl http://localhost:8080/health   # General health
```

## 🏗️ Architecture Overview

The service follows Clean Architecture principles with clear separation of concerns:

```
┌─────────────────────────────────────────────────────────────────┐
│                    LFX Indexer Service                         │
├─────────────────────────────────────────────────────────────────┤
│  Entry Points (cmd/ - Standard Go Layout)                      │
│  └─ cmd/lfx-indexer/main.go - Pure dependency injection       │
├─────────────────────────────────────────────────────────────────┤
│  Presentation Layer (NATS Protocol + Health Checks)            │
│  ├─ IndexingMessageHandler - Unified V2/V1 message processing │
│  ├─ BaseMessageHandler - Shared NATS response logic          │
│  └─ HealthHandler - Kubernetes health probes                  │
├─────────────────────────────────────────────────────────────────┤
│  Application Layer (Orchestration)                             │
│  └─ MessageProcessor - Complete workflow coordination          │
├─────────────────────────────────────────────────────────────────┤
│  Domain Layer (Business Logic)                                 │
│  ├─ IndexerService - Consolidated transaction + health logic  │
│  ├─ LFXTransaction Entity - Domain model with validation      │
│  └─ Repository Interfaces - Clean abstractions                │
├─────────────────────────────────────────────────────────────────┤
│  Infrastructure Layer (External Services)                      │
│  ├─ MessagingRepository - NATS client with queue groups       │
│  ├─ StorageRepository - OpenSearch client                     │
│  ├─ AuthRepository - JWT validation (Heimdall integration)    │
│  ├─ JanitorService - Event-driven conflict resolution         │
│  └─ Container - Pure dependency injection                     │
└─────────────────────────────────────────────────────────────────┘
                                 │
                    ┌─────────────▼──────────────┐
                    │   External Dependencies    │
                    │                           │
                    │  NATS ←→ OpenSearch ←→ JWT │
                    │                           │
                    └───────────────────────────┘
```

## 📊 Data Flow Architecture

```mermaid
graph TB
    %% External message sources
    V2_APPS["V2 Applications<br/>(Projects API, Orgs API, etc.)"]
    V1_APPS["V1 Legacy Applications<br/>(Platform DB Events)"]
    
    %% NATS Infrastructure
    NATS_SERVER["NATS Server<br/>nats://nats:4222"]
    V2_SUBJECT["V2 Subject: lfx.index.*<br/>(lfx.index.project, lfx.index.organization)"]
    V1_SUBJECT["V1 Subject: lfx.v1.index.*<br/>(lfx.v1.index.project)"]
    QUEUE_GROUP["Queue Group: lfx.indexer.queue<br/>(Load Balancing)"]
    
    %% Service Instances
    INSTANCE1["LFX Indexer Instance 1"]
    INSTANCE2["LFX Indexer Instance 2"]
    INSTANCE3["LFX Indexer Instance N..."]
    
    %% Clean Architecture Layers
    MAIN_GO["cmd/lfx-indexer/main.go<br/>(Entry Point & DI)"]
    CONTAINER["Container<br/>(Dependency Injection)"]
    MSG_REPO["MessagingRepository<br/>(NATS Wrapper)"]
    
    %% Presentation Layer
    UNIFIED_HANDLER["IndexingMessageHandler<br/>(Unified V2 + V1 Messages)"]
    HEALTH_HANDLER["HealthHandler<br/>(Kubernetes Probes)"]
    
    %% Application Layer
    MESSAGE_PROCESSOR["MessageProcessor<br/>(Workflow Coordination)"]
    
    %% Domain Layer
    INDEXER_SVC["IndexerService<br/>(Business Logic + Health)"]
    AUTH_REPO["AuthRepository<br/>(JWT Validation)"]
    TRANSACTION_ENTITY["LFXTransaction<br/>(Domain Model)"]
    
    %% Infrastructure Layer
    STORAGE_REPO["StorageRepository<br/>(OpenSearch Client)"]
    OPENSEARCH["OpenSearch Cluster<br/>resources index"]
    
    %% Janitor System
    JANITOR_SVC["JanitorService<br/>(Background Cleanup)"]
    
    %% External Services
    JWT_SERVICE["Heimdall JWT Service<br/>(Token Validation)"]
    
    %% Message Flow
    V2_APPS -->|"Publish V2 Messages"| V2_SUBJECT
    V1_APPS -->|"Publish V1 Messages"| V1_SUBJECT
    
    V2_SUBJECT --> NATS_SERVER
    V1_SUBJECT --> NATS_SERVER
    
    NATS_SERVER -->|"Queue Group Distribution"| QUEUE_GROUP
    QUEUE_GROUP --> INSTANCE1
    QUEUE_GROUP --> INSTANCE2  
    QUEUE_GROUP --> INSTANCE3
    
    %% Service Architecture Flow
    INSTANCE1 --> MAIN_GO
    MAIN_GO --> CONTAINER
    CONTAINER --> MSG_REPO
    CONTAINER --> HEALTH_HANDLER
    
    MSG_REPO -->|"Both V2 + V1"| UNIFIED_HANDLER
    
    %% Processing Flow
    UNIFIED_HANDLER --> MESSAGE_PROCESSOR
    MESSAGE_PROCESSOR --> INDEXER_SVC
    MESSAGE_PROCESSOR --> JANITOR_SVC
    
    INDEXER_SVC --> TRANSACTION_ENTITY
    INDEXER_SVC --> AUTH_REPO
    INDEXER_SVC --> STORAGE_REPO
    
    STORAGE_REPO --> OPENSEARCH
    AUTH_REPO --> JWT_SERVICE
    
    %% Styling
    classDef application fill:#e3f2fd,stroke:#1976d2,stroke-width:2px
    classDef domain fill:#f3e5f5,stroke:#7b1fa2,stroke-width:2px
    classDef infrastructure fill:#e8f5e8,stroke:#388e3c,stroke-width:2px
    classDef external fill:#fff3e0,stroke:#f57c00,stroke-width:2px
    
    class MESSAGE_PROCESSOR,UNIFIED_HANDLER application
    class INDEXER_SVC,TRANSACTION_ENTITY,AUTH_REPO domain
    class CONTAINER,MSG_REPO,STORAGE_REPO,JANITOR_SVC infrastructure
    class V2_APPS,V1_APPS,JWT_SERVICE,OPENSEARCH,NATS_SERVER external
```

## 🔄 Message Processing Sequence

```mermaid
sequenceDiagram
    participant NATS as NATS Server
    participant MR as MessagingRepository  
    participant UH as IndexingMessageHandler
    participant MP as MessageProcessor
    participant IS as IndexerService
    participant AR as AuthRepository
    participant JWT as JWT Service
    participant TE as LFXTransaction
    participant SR as StorageRepository
    participant OS as OpenSearch
    participant JS as JanitorService

    Note over NATS,JS: Clean Architecture Message Processing Flow

    %% Message Arrival & Presentation Layer
    NATS->>MR: NATS Message arrives<br/>Subject: lfx.index.project OR lfx.v1.index.project<br/>Queue: lfx.indexer.queue
    
    MR->>UH: HandleWithReply(ctx, data, subject, reply)
    Note over UH: Route based on subject prefix<br/>(V2 vs V1 format)
    
    %% Application Layer Coordination
    UH->>MP: ProcessIndexingMessage(ctx, data, subject)
    Note over MP: Generate messageID<br/>Log reception metrics
    
    %% Transaction Creation & Validation
    MP->>TE: createTransaction(data, subject)
    TE->>TE: Parse message format<br/>Extract object type from subject<br/>Validate required fields
    TE-->>MP: LFXTransaction entity
    
    %% Domain Layer Business Logic
    MP->>IS: ProcessTransaction(ctx, transaction, index)
    
    Note over IS: Consolidated processing:<br/>• EnrichTransaction()<br/>• GenerateTransactionBody()<br/>• Index document
    
    %% Authentication & Authorization
    IS->>AR: ParsePrincipals(ctx, headers)
    AR->>JWT: ValidateToken(ctx, token)
    JWT-->>AR: Principal{Principal, Email}
    AR-->>IS: []Principal with delegation support
    
    %% Data Enrichment & Validation
    IS->>IS: EnrichTransaction()<br/>ValidateObjectType()<br/>GenerateTransactionBody()
    
    %% Document Indexing
    IS->>SR: Index(ctx, index, docID, body)
    SR->>OS: POST /resources/_doc/{docID}<br/>with optimistic concurrency
    
    alt Successful Index
        OS-->>SR: 201 Created
        SR-->>IS: Success with DocumentID
        
        %% Background Conflict Resolution
        IS-->>MP: ProcessingResult{Success: true, DocumentID}
        MP->>JS: CheckItem(documentID)
        Note over JS: Background cleanup<br/>Event-driven processing
        
    else Index Conflict/Error
        OS-->>SR: 409 Conflict / 4xx Error
        SR-->>IS: Error with details
        IS-->>MP: ProcessingResult{Success: false, Error}
    end
    
    %% Response Handling
    MP-->>UH: Processing result
    
    alt Success
        UH->>UH: reply([]byte("OK"))
        Note over UH: Log success metrics
    else Error
        UH->>UH: reply([]byte("ERROR: details"))
        Note over UH: Log error with context
    end
    
    UH-->>MR: Acknowledge message
    MR-->>NATS: Message processed
```

## 🔧 Configuration & Environment

### Core Environment Variables

```bash
# Required Services
NATS_URL=nats://nats:4222                    # NATS server URL
OPENSEARCH_URL=http://localhost:9200         # OpenSearch endpoint
JWKS_URL=http://localhost:4457/.well-known/jwks  # JWT validation endpoint

# Message Processing
NATS_INDEXING_SUBJECT=lfx.index.>           # V2 subject pattern
NATS_V1_INDEXING_SUBJECT=lfx.v1.index.>    # V1 subject pattern  
NATS_QUEUE=lfx.indexer.queue                # Queue group name
OPENSEARCH_INDEX=resources                   # OpenSearch index name

# NATS Connection Settings
NATS_MAX_RECONNECTS=10                       # Max reconnection attempts
NATS_RECONNECT_WAIT=2s                       # Wait time between reconnects
NATS_CONNECTION_TIMEOUT=10s                  # Initial connection timeout

# JWT Configuration  
JWT_ISSUER=heimdall                          # JWT issuer validation
JWT_AUDIENCES=["audience1","audience2"]      # Allowed audiences (JSON array)
JWT_CLOCK_SKEW=6h                           # Clock skew tolerance

# Server Configuration
PORT=8080                                    # Health check server port
LOG_LEVEL=info                               # Logging level (debug,info,warn,error)
LOG_FORMAT=text                              # Log format (text,json)

# Janitor Service (Background Cleanup)
JANITOR_ENABLED=true                         # Enable janitor service
```

### NATS Subject Patterns & Load Balancing

| Subject Pattern | Purpose | Example | Load Balancing |
|----------------|---------|---------|----------------|
| `lfx.index.*` | V2 resource indexing | `lfx.index.project`, `lfx.index.organization` | Queue group distribution |
| `lfx.v1.index.*` | V1 legacy support | `lfx.v1.index.project` | Queue group distribution |

**Queue Group**: `lfx.indexer.queue`
- **Load Balancing**: Automatic distribution across service instances
- **Durability**: Messages processed exactly once per queue group  
- **Fault Tolerance**: Failed instances don't lose messages

## 📁 Project Structure

```
├── cmd/                           # Application entry points (Standard Go Layout)
│   └── lfx-indexer/              # Main indexer service
│       ├── main.go               # Service entry point & dependency injection
│       ├── cli.go                # CLI command handling
│       └── server.go             # HTTP server setup
├── internal/                      # Private application code
│   ├── application/              # Application layer (use cases)
│   │   ├── message_processor.go  # Message processing coordination
│   │   └── message_processor_test.go # Application layer tests
│   ├── domain/                   # Domain layer (business logic)
│   │   ├── contracts/            # Domain contracts/interfaces
│   │   │   ├── messaging.go      # Messaging repository interface
│   │   │   └── storage.go        # Storage repository interface
│   │   ├── entities/             # Domain entities
│   │   │   └── transaction.go    # LFX transaction entity & validation
│   │   └── services/             # Domain services
│   │       ├── indexer_service.go # Core indexer business logic
│   │       └── indexer_service_test.go # Domain service tests
│   ├── infrastructure/           # Infrastructure layer
│   │   ├── auth/                 # Authentication
│   │   │   ├── auth_repository.go # JWT validation implementation
│   │   │   └── auth_repository_test.go # Auth tests
│   │   ├── config/               # Configuration management
│   │   │   ├── app_config.go     # Application configuration
│   │   │   └── cli_config.go     # CLI configuration
│   │   ├── janitor/              # Background cleanup service
│   │   │   ├── janitor_service.go # Event-driven conflict resolution
│   │   │   └── janitor_service_test.go # Janitor tests
│   │   ├── messaging/            # NATS messaging
│   │   │   ├── messaging_repository.go # NATS client wrapper
│   │   │   └── messaging_repository_test.go # Messaging tests
│   │   └── storage/              # OpenSearch storage
│   │       ├── storage_repository.go # OpenSearch client wrapper
│   │       └── storage_repository_test.go # Storage tests
│   ├── presentation/             # Presentation layer
│   │   └── handlers/             # Message and HTTP handlers
│   │       ├── health_handler.go # Kubernetes health check endpoints
│   │       ├── health_handler_test.go # Health handler tests
│   │       ├── indexing_message_handler.go # NATS message handler
│   │       └── indexing_message_handler_test.go # Handler tests
│   ├── enrichers/                # Data enrichment utilities
│   │   ├── project_enricher.go   # Project-specific enrichment
│   │   └── registry.go           # Enricher registry
│   ├── container/                # Dependency injection
│   │   ├── container.go          # DI container implementation
│   │   └── container_test.go     # Container tests
│   └── mocks/                    # Mock implementations
│       └── repositories.go       # Repository mocks for testing
├── pkg/                          # Public packages (reusable)
│   ├── constants/                # Shared constants
│   │   ├── app.go                # Application constants
│   │   ├── auth.go               # Authentication constants
│   │   ├── errors.go             # Error constants
│   │   ├── health.go             # Health check constants
│   │   └── messaging.go          # Messaging constants
│   └── logging/                  # Logging utilities
│       ├── logger.go             # Logger implementation
│       ├── logger_test.go        # Logger tests
│       └── testing.go            # Test logging utilities
├── deployment/                   # Deployment configurations
│   └── deployment.yaml          # Kubernetes deployment
├── docs/                         # Documentation
│   └── MOCK_DATA_SUPPORT_PLAN.md # Mock data support documentation
├── Dockerfile                    # Container definition
├── Makefile                      # Build automation
├── go.mod                        # Go module definition
├── go.sum                        # Go module checksums
├── run.sh                        # Development run script
└── README.md                     # This file
```

### Layer Responsibilities

| Layer | Components | Responsibilities |
|-------|------------|-----------------|
| **Entry Point** | `cmd/lfx-indexer/main.go` | Pure application startup and dependency injection |
| **Presentation** | `IndexingMessageHandler`, `HealthHandler` | NATS protocol concerns, message parsing, response handling, health checks |
| **Application** | `MessageProcessor` | Workflow coordination, use case orchestration |
| **Domain** | `IndexerService`, `LFXTransaction`, Repository Interfaces | Business logic, domain rules, data validation |
| **Infrastructure** | `Container`, `MessagingRepository`, `StorageRepository`, `AuthRepository`, `JanitorService` | External service integration, data persistence, event-driven processing |

## 🧑‍💻 Development

### Build Targets

```bash
# Local development
make build-local                    # Build for current OS
make run                           # Run with environment setup
make fmt                           # Format code
make lint                          # Run linters
make test                          # Run tests

# Production build
make build                         # Build for Linux (deployment)
make docker-build                  # Build container image
make docker-push                   # Push to registry

# Testing by layer
make test-domain                   # Domain layer tests
make test-application              # Application layer tests  
make test-infrastructure          # Infrastructure layer tests
make test-presentation             # Presentation layer tests
make coverage                      # Generate coverage report
```

### Testing Message Processing

```bash
# Test V2 message format
nats pub lfx.index.project '{
  "action": "created",
  "headers": {"Authorization": "Bearer token"},
  "data": {
    "id": "test-project-123",
    "uid": "test-project-123", 
    "name": "Test Project",
    "slug": "test-project",
    "public": true
  }
}' --server nats://your-nats-server:4222

# Test V1 message format  
nats pub lfx.v1.index.project '{
  "action": "create",
  "data": {
    "id": "test-project-456",
    "name": "Legacy Project"
  },
  "v1_data": {
    "legacy_field": "legacy_value"
  }
}' --server nats://your-nats-server:4222

# Monitor processing
nats sub "lfx.index.>" --server nats://your-nats-server:4222
```

## 🚨 Troubleshooting

### Common Issues

**1. NATS Connection Failures**
```bash
# Check NATS connectivity
nats server check --server $NATS_URL

# Verify queue configuration
nats consumer info lfx.indexer.queue --server $NATS_URL

# Test basic pub/sub
nats pub test.subject "hello" --server $NATS_URL
nats sub test.subject --server $NATS_URL
```

**2. OpenSearch Issues**
```bash
# Check OpenSearch health
curl $OPENSEARCH_URL/_cluster/health

# Verify index exists
curl $OPENSEARCH_URL/resources/_mapping

# Check recent documents
curl $OPENSEARCH_URL/resources/_search?size=5&sort=@timestamp:desc
```

**3. JWT Validation Failures**
```bash
# Check Heimdall connectivity
curl $JWKS_URL

# Enable debug logging for auth details
LOG_LEVEL=debug ./bin/lfx-indexer
```

### Performance Monitoring

**Health Endpoints:**
```bash
curl http://localhost:8080/livez    # Kubernetes liveness
curl http://localhost:8080/readyz   # Kubernetes readiness  
curl http://localhost:8080/health   # General health status
```

**Debug Configuration:**
```bash
# Enable comprehensive debugging
export LOG_LEVEL=debug
export LOG_FORMAT=json

# Monitor message processing
LOG_LEVEL=debug make run
```

## 🔒 Security

**JWT Authentication:**
- **Heimdall Integration**: All messages validated against JWT service
- **Multiple Audiences**: Configurable audience validation
- **Principal Parsing**: Authorization header and X-On-Behalf-Of delegation support
- **Machine User Detection**: `clients@` prefix identification

**Input Validation:**
- **Domain Validation**: LFXTransaction entity validates all inputs
- **Subject Format Validation**: String prefix validation with enhanced error messages
- **Data Structure Validation**: Comprehensive JSON schema validation

**Error Handling:**
- **Safe Error Messages**: No sensitive data leakage
- **Structured Logging**: Detailed error context for debugging
- **Graceful Degradation**: Continue processing on non-critical failures

## 🐳 Deployment

### Docker
```bash
# Build container
make docker-build

# Run container
docker run -p 8080:8080 \
  -e NATS_URL=nats://nats:4222 \
  -e OPENSEARCH_URL=http://opensearch:9200 \
  -e JWKS_URL=http://heimdall:4457/.well-known/jwks \
  lfx-indexer-service:latest
```

### Kubernetes
```yaml
# See deployment/deployment.yaml for complete configuration
apiVersion: apps/v1
kind: Deployment
metadata:
  name: lfx-indexer-service
spec:
  replicas: 3
  selector:
    matchLabels:
      app: lfx-indexer-service
  template:
    spec:
      containers:
      - name: lfx-indexer
        image: lfx-indexer-service:latest
        ports:
        - containerPort: 8080
        env:
        - name: NATS_URL
          value: "nats://nats:4222"
        - name: OPENSEARCH_URL
          value: "http://opensearch:9200"
        livenessProbe:
          httpGet:
            path: /livez
            port: 8080
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8080
```

## 📄 License

This project is licensed under the MIT License - see the LICENSE file for details.

---

**Contributing**: Please follow the clean architecture principles and include comprehensive tests for any new features.
