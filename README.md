# LFX V2 Indexer Service

A high-performance, Indexer Service for the LFX V2 platform that processes resource transactions into OpenSearch with comprehensive NATS message processing and queue group load balancing.

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

# Development mode (uses Makefile)
make run

# Build and run using Make
make build-local
./bin/lfx-indexer

# Direct Go commands (new cmd structure)
go run ./cmd/lfx-indexer
go build -o bin/lfx-indexer ./cmd/lfx-indexer

# Command-line options
./bin/lfx-indexer -help
./bin/lfx-indexer -version
./bin/lfx-indexer -check-config
```

### Health Check
```bash
# Kubernetes probes
curl http://localhost:8080/livez    # Liveness probe
curl http://localhost:8080/readyz   # Readiness probe  
curl http://localhost:8080/health   # General health
```

## 🏗️ Architecture & Data Flow


```
┌─────────────────────────────────────────────────────────────────┐
│                    LFX Indexer Service                         │
├─────────────────────────────────────────────────────────────────┤
│  Entry Points (cmd/ - Standard Go Layout)                      │
│  └─ cmd/lfx-indexer/main.go - Pure dependency injection       │
├─────────────────────────────────────────────────────────────────┤
│  Presentation Layer (NATS Protocol + Health Checks)            │
│  ├─ IndexingMessageHandler - V2 message processing            │
│  ├─ V1IndexingMessageHandler - V1 legacy support             │
│  ├─ BaseMessageHandler - Shared NATS response logic          │
│  └─ HealthHandler - Kubernetes health probes                  │
├─────────────────────────────────────────────────────────────────┤
│  Application Layer (Use Case Orchestration)                    │
│  ├─ MessageProcessingUseCase - Complete workflow coordination │
│  └─ IndexingUseCase - Message-to-transaction processing       │
├─────────────────────────────────────────────────────────────────┤
│  Domain Layer (Business Logic)                                 │
│  ├─ TransactionService - Core transaction processing          │
│  ├─ LFXTransaction Entity - Domain model with validation      │
│  ├─ Simple Subject Parsing - Clean constants    │
│  └─ Repository Interfaces - Clean abstractions                │
├─────────────────────────────────────────────────────────────────┤
│  Infrastructure Layer (External Services)                      │
│  ├─ MessageRepository - NATS client with queue groups         │
│  ├─ TransactionRepository - OpenSearch client                 │
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

### Comprehensive Message Processing Flow

```mermaid
graph TB
    %% External message sources
    V2_APPS["V2 Applications<br/>(Projects API, Orgs API, etc.)"]
    V1_APPS["V1 Legacy Applications<br/>(Platform DB Events)"]
    
    %% NATS Infrastructure
    NATS_SERVER["NATS Server<br/>nats://nats:4222"]
    V2_SUBJECT["V2 Subject: lfx.index.><br/>(lfx.index.project, lfx.index.organization)"]
    V1_SUBJECT["V1 Subject: lfx.v1.index.><br/>(lfx.v1.index.project)"]
    QUEUE_GROUP["Queue Group: lfx.indexer.queue<br/>(Load Balancing)"]
    
    %% Service Instances
    INSTANCE1["LFX Indexer Instance 1"]
    INSTANCE2["LFX Indexer Instance 2"]
    INSTANCE3["LFX Indexer Instance N..."]
    
    %% Clean Architecture Layers
    MAIN_GO["cmd/lfx-indexer/main.go<br/>(Entry Point)"]
    CONTAINER["Container<br/>(Pure Dependency Injection)"]
    MSG_REPO["MessageRepository<br/>(NATS Wrapper)"]
    
    %% Presentation Layer
    BASE_HANDLER["BaseMessageHandler<br/>(Shared NATS Response Logic)"]
    V2_HANDLER["IndexingMessageHandler<br/>(V2 Messages)"]
    V1_HANDLER["V1IndexingMessageHandler<br/>(V1 Messages)"]
    
    %% Application Layer  
    MSG_PROC_UC["MessageProcessingUseCase<br/>(Coordination Layer)"]
    INDEXING_UC["IndexingUseCase<br/>(Message Orchestration)"]
    
    %% Domain Layer
    TRANSACTION_SVC["TransactionService<br/>(Business Logic + Fixed Subject Parsing)"]
    AUTH_REPO["AuthRepository<br/>(JWT Validation)"]
    
    %% Infrastructure Layer
    OPENSEARCH_REPO["TransactionRepository<br/>(OpenSearch Client)"]
    OPENSEARCH["OpenSearch Cluster<br/>resources index"]
    
    %% Janitor System
    JANITOR_QUEUE["Janitor Queue<br/>(Channel-based)"]
    JANITOR_WORKERS["Janitor Workers<br/>(Background Processing)"]
    JANITOR_SVC["JanitorService<br/>(Event-driven Conflict Resolution)"]
    
    %% External Services
    JWT_SERVICE["Heimdall JWT Service<br/>(Token Validation)"]
    
    %% Message Flow
    V2_APPS -->|"Publish V2 Messages"| V2_SUBJECT
    V1_APPS -->|"Publish V1 Messages"| V1_SUBJECT
    
    V2_SUBJECT --> NATS_SERVER
    V1_SUBJECT --> NATS_SERVER
    
    NATS_SERVER -->|"Queue Subscribe"| QUEUE_GROUP
    
    %% Load Balancing
    QUEUE_GROUP -.->|"Load Balance"| INSTANCE1
    QUEUE_GROUP -.->|"Load Balance"| INSTANCE2
    QUEUE_GROUP -.->|"Load Balance"| INSTANCE3
    
    %% Focus on single instance flow
    INSTANCE1 --> MAIN_GO
    MAIN_GO --> CONTAINER
    CONTAINER --> MSG_REPO
    
    MSG_REPO -->|"V2: lfx.index.*"| V2_HANDLER
    MSG_REPO -->|"V1: lfx.v1.index.*"| V1_HANDLER
    
    %% BaseMessageHandler Composition
    BASE_HANDLER -.->|"Embedded in"| V2_HANDLER
    BASE_HANDLER -.->|"Embedded in"| V1_HANDLER
    
    %% Application Layer Coordination
    V2_HANDLER --> MSG_PROC_UC
    V1_HANDLER --> MSG_PROC_UC
    
    MSG_PROC_UC --> INDEXING_UC
    MSG_PROC_UC -->|"Queue Cleanup"| JANITOR_SVC
    
    INDEXING_UC --> TRANSACTION_SVC
    
    %% Business Logic Flow
    TRANSACTION_SVC -->|"1. Fixed Subject Parsing (No Duplication)"| TRANSACTION_SVC
    TRANSACTION_SVC -->|"2. Validate JWT Headers"| AUTH_REPO
    AUTH_REPO -->|"Validate Token"| JWT_SERVICE
    TRANSACTION_SVC -->|"3. Enrich Data"| TRANSACTION_SVC
    TRANSACTION_SVC -->|"4. Generate Index Body"| TRANSACTION_SVC
    TRANSACTION_SVC -->|"5. Index Document"| OPENSEARCH_REPO
    
    OPENSEARCH_REPO --> OPENSEARCH
    
    %% Janitor Integration
    JANITOR_SVC --> JANITOR_QUEUE
    JANITOR_QUEUE --> JANITOR_WORKERS
    JANITOR_WORKERS -->|"Conflict Resolution"| OPENSEARCH
```

## 🔄 Message Processing Flow

### Complete Processing Sequence

```mermaid
sequenceDiagram
    participant NATS as NATS Server
    participant MR as MessageRepository  
    participant IMH as IndexingMessageHandler
    participant BMH as BaseMessageHandler
    participant MPUC as MessageProcessingUseCase
    participant IUC as IndexingUseCase
    participant TS as TransactionService
    participant AR as AuthRepository
    participant JWT as JWT Service
    participant TR as TransactionRepository
    participant OS as OpenSearch
    participant JS as JanitorService

    Note over NATS,JS: Clean Architecture Message Processing Flow

    %% Message Arrival & Presentation Layer
    NATS->>MR: NATS Message arrives<br/>Subject: lfx.index.project<br/>Queue: lfx.indexer.queue
    
    MR->>IMH: HandleWithReply(ctx, data, subject, reply)
    
    %% Application Layer Coordination
    IMH->>MPUC: ProcessIndexingMessage(ctx, data, subject)
    MPUC->>IUC: HandleIndexingMessage(ctx, data, subject)
    
    %% Domain Layer Business Logic
    IUC->>TS: CreateTransaction + EnrichTransaction
    
    Note over TS: Simple subject parsing:<br/>strings.CutPrefix(subject, "lfx.index.")<br/>(Fixed duplicate parsing issue)
    
    TS->>AR: ParsePrincipals(ctx, headers)
    AR->>JWT: ValidateToken(ctx, token)
    JWT-->>AR: Principal{Principal, Email}
    AR-->>TS: []Principal
    
    TS->>TS: ValidateObjectType() + GenerateTransactionBody()
    TS->>TR: Index(ctx, index, docID, body)
    TR->>OS: POST /resources/_doc/{docID}
    OS-->>TR: 201 Created
    TR-->>TS: Success
    
    TS-->>IUC: ProcessingResult{Success: true}
    IUC-->>MPUC: Success
    
    %% Janitor Coordination
    MPUC->>JS: CheckItem(objectRef)
    Note over JS: Background conflict resolution
    
    MPUC-->>IMH: Success
    
    %% Presentation Layer Response
    IMH->>BMH: RespondSuccess(reply, subject)
    BMH-->>MR: reply([]byte("OK"))
    MR-->>NATS: Acknowledge message
```

### **Layer Responsibilities**

| Layer | Components | Responsibilities |
|-------|------------|-----------------|
| **Entry Point** | cmd/lfx-indexer/main.go | Pure application startup and dependency injection |
| **Presentation** | BaseMessageHandler, IndexingMessageHandler, V1IndexingMessageHandler, HealthHandler | NATS protocol concerns, message parsing, response handling, health checks |
| **Application** | MessageProcessingUseCase, IndexingUseCase | Workflow coordination, use case orchestration |
| **Domain** | TransactionService, LFXTransaction, Simple Subject Parsing | Business logic, domain rules, data validation |
| **Infrastructure** | Container, MessageRepository, TransactionRepository, AuthRepository, JanitorService | External service integration, data persistence, event-driven processing |

## 📊 NATS Configuration & Subjects

### Subject Patterns & Load Balancing

| Subject Pattern | Purpose | Example | Load Balancing |
|----------------|---------|---------|----------------|
| `lfx.index.>` | V2 resource indexing | `lfx.index.project`, `lfx.index.organization` | Queue group distribution |
| `lfx.v1.index.>` | V1 legacy support | `lfx.v1.index.project` | Queue group distribution |

**Queue Group**: `lfx.indexer.queue`
- **Load Balancing**: Automatic distribution across service instances
- **Durability**: Messages processed exactly once per queue group  
- **Fault Tolerance**: Failed instances don't lose messages

### Message Testing

You can test message processing using NATS CLI:

```bash
# Create project message
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

# Monitor processing
nats sub "lfx.index.>" --server nats://your-nats-server:4222
```

## 🔧 Configuration

### Environment Variables

```bash
# Core Services (Required)
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

# OpenSearch Settings
OPENSEARCH_TIMEOUT=30s                       # Request timeout

# Server Configuration
PORT=8080                                    # Health check server port
LOG_LEVEL=info                               # Logging level (debug,info,warn,error)
LOG_FORMAT=text                              # Log format (text,json)

# Janitor Service (Background Cleanup)
JANITOR_ENABLED=true                         # Enable janitor service (default: true)

# Janitor Implementation Details:
# - Queue Size: 50 items (hardcoded for stability)
# - Workers: 1 event-driven goroutine 
# - Retry Delay: 5-10 seconds random (hardcoded)
# - Processing: Event-driven via NATS messages (no intervals)
```

### Configuration Validation

The service validates all configuration on startup:

```bash
# Check configuration without starting service
./bin/lfx-indexer -check-config

# Debug configuration issues
LOG_LEVEL=debug ./bin/lfx-indexer -check-config
```

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

# Testing
make test-domain                   # Domain layer tests
make test-application              # Application layer tests  
make test-infrastructure          # Infrastructure layer tests
make test-presentation             # Presentation layer tests
make coverage                      # Generate coverage report

# Code quality
make security                      # Security scanning
make deps-graph                    # Dependency analysis
make complexity                    # Code complexity check
```

### Direct Go Commands

```bash
# Using the new cmd structure
go run ./cmd/lfx-indexer           # Run directly
go build -o bin/lfx-indexer ./cmd/lfx-indexer  # Build directly
go test ./...                      # Run all tests

# Testing specific layers
go test ./internal/domain/...      # Domain tests
go test ./internal/application/... # Application tests
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

# Verify JWT configuration
echo $JWT_AUDIENCES
echo $JWT_CLOCK_SKEW

# Enable debug logging for auth details
LOG_LEVEL=debug ./bin/lfx-indexer
```

**4. Subject Parsing Issues (Fixed)**
Previous versions had duplicate subject parsing causing errors like:
```
failed to parse subject: invalid subject format: project (expected prefix: lfx.index.)
```
This has been **fixed** by removing duplicate parsing in TransactionService.

### Debug Configuration
```bash
# Enable comprehensive debugging
export LOG_LEVEL=debug
export LOG_FORMAT=json

# Reduce janitor buffer for debugging
export JANITOR_QUEUE_SIZE=10
export JANITOR_WORKERS=1

# Enable NATS connection debugging  
export NATS_VERBOSE=true
```

### Performance Monitoring

**Health Endpoints:**
```bash
curl http://localhost:8080/livez    # Kubernetes liveness
curl http://localhost:8080/readyz   # Kubernetes readiness  
curl http://localhost:8080/health   # General health status
```


## 🔒 Security

**JWT Authentication:**
- **Heimdall Integration**: All messages validated against JWT service
- **Multiple Audiences**: Configurable audience validation
- **Principal Parsing**: Authorization header and X-On-Behalf-Of delegation support
- **Machine User Detection**: `clients@` prefix identification

**Input Validation:**
- **Domain Validation**: LFXTransaction entity validates all inputs
- **Subject Format Validation**: Simple string prefix validation
- **Data Structure Validation**: Comprehensive JSON schema validation

**Error Handling:**
- **Safe Error Messages**: No sensitive data leakage
- **Structured Logging**: Detailed error context for debugging
- **Graceful Degradation**: Continue processing on non-critical failures

## 📁 Project Structure

```
├── cmd/                           # Application entry points (Standard Go Layout)
│   ├── lfx-indexer/              # Main indexer service
│   │   └── main.go               # Service entry point
│   └── README.md                 # Command documentation
├── internal/                      # Private application code
│   ├── application/              # Application layer (use cases)
│   │   └── usecases/             # Use case implementations
│   ├── domain/                   # Domain layer (business logic)
│   │   ├── constants/            # Shared constants (subject prefixes)
│   │   ├── entities/             # Domain entities
│   │   ├── repositories/         # Repository interfaces
│   │   └── services/             # Domain services
│   ├── infrastructure/           # Infrastructure layer
│   │   ├── config/               # Configuration management
│   │   ├── opensearch/           # OpenSearch client
│   │   ├── nats/                 # NATS messaging
│   │   └── jwt/                  # JWT authentication
│   ├── presentation/             # Presentation layer
│   │   └── handlers/             # HTTP and message handlers
│   ├── container/                # Dependency injection
│   └── mocks/                    # Mock implementations
├── bin/                          # Compiled binaries
├── deployments/                  # Deployment configurations
├── docs/                         # Documentation
├── Dockerfile                    # Container definition
├── Makefile                      # Build automation
├── go.mod                        # Go dependencies
└── README.md                     # This file
```


## 📄 License

This project is licensed under the MIT License - see the LICENSE file for details.