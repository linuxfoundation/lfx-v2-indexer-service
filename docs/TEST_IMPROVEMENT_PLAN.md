# Test Coverage and Unit Testing Improvement Plan

**Project**: LFX Indexer Service  
**Date**: January 2025  
**Goal**: Improve test coverage from 15% to 90%+ with comprehensive testing strategy

## 📊 Current State Analysis

### ✅ What's Working:
- **Domain Services**: `TransactionService` has basic unit tests (69.2% coverage)
- **Mock Infrastructure**: Basic manual mocks exist in `internal/mocks/`
- **Test Structure**: Uses `testify` framework properly
- **CI Setup**: Basic test execution in Makefile

### ❌ Critical Gaps:
- **Coverage**: Only 1 out of 12 packages has tests
- **Test Quality**: Limited to happy path scenarios
- **Mock Quality**: Manual mocks instead of generated ones
- **Integration Testing**: No end-to-end testing
- **Architecture Compliance**: No tests ensuring clean architecture rules

### Coverage Status by Package:
```
✅ internal/domain/services:        69.2%
❌ internal/application/usecases:   0.0%
❌ internal/infrastructure/config:  0.0%
❌ internal/infrastructure/jwt:     0.0%
❌ internal/infrastructure/nats:    0.0%
❌ internal/infrastructure/opensearch: 0.0%
❌ internal/presentation/handlers:  0.0%
❌ internal/container:              0.0%
❌ internal/domain/entities:        0.0%
❌ internal/mocks:                  0.0%
❌ main.go:                         0.0%
```

## 🎯 Comprehensive Improvement Plan

### Phase 1: Test Infrastructure & Tooling
**Timeline**: Week 1-2  
**Goal**: Establish robust testing foundation

#### Actions:
1. **Install Testing Tools**:
   ```bash
   # Add to development dependencies
   go install github.com/golang/mock/mockgen@latest
   go install github.com/onsi/ginkgo/v2/ginkgo@latest
   go install github.com/testcontainers/testcontainers-go@latest
   go install github.com/stretchr/testify@latest
   ```

2. **Create Test Helpers**:
   - `testutil/builders.go` - Test data builders
   - `testutil/helpers.go` - Common test utilities
   - `testutil/fixtures.go` - Test fixtures and data
   - `testutil/assertions.go` - Custom assertions

3. **Generate Proper Mocks**:
   ```bash
   # Generate mocks for all interfaces
   mockgen -source=internal/domain/repositories/transaction_repository.go -destination=internal/mocks/mock_transaction_repository.go
   mockgen -source=internal/domain/repositories/message_repository.go -destination=internal/mocks/mock_message_repository.go
   mockgen -source=internal/domain/repositories/auth_repository.go -destination=internal/mocks/mock_auth_repository.go
   ```

4. **Update Makefile**:
   ```makefile
   .PHONY: generate-mocks
   generate-mocks: ## Generate mocks for testing
   	@echo "Generating mocks..."
   	mockgen -source=internal/domain/repositories/transaction_repository.go -destination=internal/mocks/mock_transaction_repository.go
   	mockgen -source=internal/domain/repositories/message_repository.go -destination=internal/mocks/mock_message_repository.go
   	mockgen -source=internal/domain/repositories/auth_repository.go -destination=internal/mocks/mock_auth_repository.go

   .PHONY: test-coverage-detailed
   test-coverage-detailed: test ## Show detailed coverage report
   	@echo "Generating detailed coverage report..."
   	go tool cover -func=coverage.out | sort -k3 -nr
   ```

### Phase 2: Domain Layer Testing (Priority 1)
**Timeline**: Week 3-4  
**Goal**: Achieve 95%+ coverage for core business logic

#### 2.1 Domain Entities (`internal/domain/entities/`)
**Target Coverage**: 95%

**Test Files to Create**:
- `internal/domain/entities/transaction_test.go`
- `internal/domain/entities/principal_test.go`

**Test Scenarios**:
```go
// Transaction Entity Tests:
- Constructor validation
- Field validation (Action, ObjectType, etc.)
- IsV1Transaction() method
- Timestamp handling
- Data structure validation
- Principal parsing
- Error scenarios with invalid data

// Principal Entity Tests:
- Constructor validation
- Email validation
- Principal ID validation
- String representation
- Equality methods
```

#### 2.2 Domain Services (`internal/domain/services/`)
**Target Coverage**: 95%

**Expand `transaction_service_test.go`**:
```go
// Additional test scenarios:
- ParseTransaction error scenarios (malformed JSON, missing fields)
- ParseV1Transaction with various data formats
- ProcessTransaction with indexing failures
- Authentication failures and token validation
- Header parsing edge cases
- Subject pattern extraction failures
- Concurrent transaction processing
- Memory leak testing
- Table-driven tests for different message formats
```

#### 2.3 Value Objects (`internal/domain/valueobjects/`)
**Target Coverage**: 100%

**Test Files to Create**:
- `internal/domain/valueobjects/processing_result_test.go`

**Test Scenarios**:
```go
// ProcessingResult Tests:
- Constructor validation
- Success/failure state management
- Error handling and wrapping
- Immutability guarantees
- Serialization/deserialization
- Equality and comparison methods
```

### Phase 3: Application Layer Testing (Priority 1)
**Timeline**: Week 5-6  
**Goal**: Test use case orchestration and business workflows

#### 3.1 Use Cases (`internal/application/usecases/`)
**Target Coverage**: 90%

**Test Files to Create**:
- `internal/application/usecases/indexing_usecase_test.go`
- `internal/application/usecases/janitor_usecase_test.go`

**Test Scenarios**:
```go
// IndexingUseCase Tests:
- HandleIndexingMessage success scenarios
- HandleV1IndexingMessage legacy format processing
- Error handling and recovery
- Transaction parsing integration
- Processing time measurement
- NATS message acknowledgment
- Concurrent message processing
- Memory usage under load

// JanitorUseCase Tests:
- StartJanitorTasks lifecycle
- Background task scheduling
- UpdateLatestFlags operations
- CleanupOldDocuments batch processing
- Error handling and retries
- Graceful shutdown scenarios
- Interval configuration
- Batch size optimization
```

### Phase 4: Infrastructure Layer Testing (Priority 2)
**Timeline**: Week 7-8  
**Goal**: Test external service integrations with proper mocking

#### 4.1 Configuration (`internal/infrastructure/config/`)
**Target Coverage**: 85%

**Test Files to Create**:
- `internal/infrastructure/config/config_test.go`

**Test Scenarios**:
```go
// Configuration Tests:
- LoadConfig with various environment setups
- Environment variable parsing
- Configuration validation
- Default value handling
- Invalid configuration scenarios
- NATS configuration validation
- OpenSearch configuration validation
- JWT configuration validation
- Server configuration validation
```

#### 4.2 Repositories (`internal/infrastructure/`)
**Target Coverage**: 85%

**Test Files to Create**:
- `internal/infrastructure/nats/message_repository_test.go`
- `internal/infrastructure/opensearch/transaction_repository_test.go`
- `internal/infrastructure/jwt/auth_repository_test.go`

**Test Scenarios**:
```go
// NATS Repository Tests:
- Connection management and lifecycle
- Message publishing with queue groups
- Subscription handling
- Error handling and reconnection
- Connection failure scenarios
- Message acknowledgment
- Concurrent operations

// OpenSearch Repository Tests:
- Document indexing operations
- Search and query operations
- Bulk operations and batching
- Connection failures and retries
- Index management
- Document lifecycle (create, update, delete)
- Error response handling

// JWT Repository Tests:
- Token validation with JWKS
- Principal parsing from tokens
- JWKS endpoint interaction
- Authentication failures
- Token expiration handling
- Malformed token scenarios
- Network failure handling
```

### Phase 5: Presentation Layer Testing (Priority 3)
**Timeline**: Week 9  
**Goal**: Test HTTP handlers and API endpoints

#### 5.1 Health Handlers (`internal/presentation/handlers/`)
**Target Coverage**: 90%

**Test Files to Create**:
- `internal/presentation/handlers/health_handler_test.go`

**Test Scenarios**:
```go
// Health Handler Tests:
- GET /livez endpoint
- GET /readyz endpoint
- GET /health endpoint
- HTTP response codes and content
- Content-Type headers
- Error response formatting
- Route registration
- Middleware integration
```

#### 5.2 Container Testing (`internal/container/`)
**Target Coverage**: 80%

**Test Files to Create**:
- `internal/container/container_test.go`

**Test Scenarios**:
```go
// Container Tests:
- NewContainer initialization
- Dependency injection setup
- Configuration validation
- Service initialization order
- Health check integration
- Graceful shutdown
- Error handling during startup
- Resource cleanup
```

### Phase 6: Integration Testing (Priority 2)
**Timeline**: Week 10  
**Goal**: End-to-end testing with real dependencies

#### 6.1 Integration Test Suite
**Test Files to Create**:
- `test/integration/message_processing_test.go`
- `test/integration/health_endpoints_test.go`
- `test/integration/janitor_tasks_test.go`

**Test Scenarios**:
```go
// End-to-End Tests:
- Complete NATS message processing pipeline
- OpenSearch indexing with real cluster
- JWT authentication flow
- Error handling and recovery
- Performance under load
- Multi-instance queue group behavior
- Data consistency validation
```

#### 6.2 Test Infrastructure
**Files to Create**:
- `test/testcontainers/nats.go`
- `test/testcontainers/opensearch.go`
- `test/testcontainers/jwt_mock.go`
- `docker-compose.test.yml`

**Setup**:
```go
// Use testcontainers for:
- NATS server with clustering
- OpenSearch cluster (single node for testing)
- Mock JWT service with JWKS endpoint
- Docker compose test environment
- Test data fixtures and cleanup
```

### Phase 7: Advanced Testing Features (Priority 3)
**Timeline**: Week 11-12  
**Goal**: Performance, compliance, and quality assurance

#### 7.1 Architecture Compliance Tests
**Test Files to Create**:
- `test/architecture/dependency_rules_test.go`
- `test/architecture/layer_isolation_test.go`

**Test Scenarios**:
```go
// Architecture Compliance:
- Domain layer doesn't import infrastructure
- Application layer doesn't import presentation
- Infrastructure implements domain interfaces
- Clean architecture dependency direction
- Import restrictions enforcement
- Interface compliance validation
```

#### 7.2 Benchmark Tests
**Test Files to Create**:
- `internal/domain/services/transaction_service_bench_test.go`
- `internal/application/usecases/indexing_usecase_bench_test.go`

**Benchmark Scenarios**:
```go
// Performance Critical Paths:
- BenchmarkTransactionParsing
- BenchmarkMessageProcessing
- BenchmarkOpenSearchIndexing
- BenchmarkConcurrentProcessing
- BenchmarkMemoryUsage
- BenchmarkThroughput
```

#### 7.3 Property-Based Testing
**Test Files to Create**:
- `test/property/transaction_properties_test.go`

**Property Tests**:
```go
// Use rapid or quickcheck for:
- Input validation fuzzing
- Transaction data generation
- Error scenario exploration
- Invariant verification
```

## 🚀 Implementation Timeline

### Week 1-2: Foundation
- [ ] Set up test infrastructure and tooling
- [ ] Install required testing dependencies
- [ ] Create test helpers and builders
- [ ] Generate proper mocks for all interfaces
- [ ] Update Makefile with testing targets

### Week 3-4: Domain Layer
- [ ] Comprehensive entity testing
- [ ] Expand service layer tests with error scenarios
- [ ] Value object validation and immutability tests
- [ ] Achieve 95% domain layer coverage

### Week 5-6: Application Layer  
- [ ] Use case testing with full scenario coverage
- [ ] Error handling and edge cases
- [ ] Integration between layers
- [ ] Achieve 90% application layer coverage

### Week 7-8: Infrastructure Layer
- [ ] Repository testing with mocked dependencies
- [ ] Configuration and setup testing
- [ ] External service integration tests
- [ ] Achieve 85% infrastructure layer coverage

### Week 9: Presentation & Container
- [ ] Health handler testing
- [ ] Container initialization testing
- [ ] HTTP endpoint validation
- [ ] Achieve 85% presentation layer coverage

### Week 10: Integration Testing
- [ ] End-to-end integration tests
- [ ] Testcontainers setup
- [ ] Performance validation
- [ ] Multi-service integration

### Week 11-12: Advanced Features
- [ ] Architecture compliance testing
- [ ] Benchmark and performance tests
- [ ] Property-based testing
- [ ] Quality assurance automation

## 📈 Success Metrics

### Coverage Targets:
- **Domain Layer**: 95%+ coverage
- **Application Layer**: 90%+ coverage  
- **Infrastructure Layer**: 85%+ coverage
- **Presentation Layer**: 85%+ coverage
- **Overall Project**: 90%+ coverage

### Quality Targets:
- **Test Execution Time**: < 30 seconds for unit tests
- **Integration Tests**: < 2 minutes
- **Code Complexity**: Cyclomatic complexity < 10
- **Architecture Compliance**: 100% (no violations)
- **Test Reliability**: > 99% (flaky test rate < 1%)

### Performance Targets:
- **Message Processing**: > 1000 messages/second
- **Test Coverage Check**: < 5 seconds
- **Memory Usage**: < 100MB during testing
- **Docker Test Startup**: < 30 seconds

## 🛠️ Tools and Technologies

### Testing Frameworks:
- **Unit Testing**: `testify/assert`, `testify/suite`, `testify/mock`
- **Mocking**: `golang/mock/mockgen`
- **Integration**: `testcontainers-go`
- **BDD Testing**: `ginkgo/gomega` (optional)
- **Property Testing**: `pgregory.net/rapid`

### Coverage and Quality:
- **Coverage**: `go test -cover`, `gocov`, `gocov-html`
- **Benchmarking**: `go test -bench`, `benchstat`
- **Linting**: `golangci-lint` (already configured)
- **Architecture**: Custom compliance tests
- **Security**: `gosec` (already in golangci-lint)

### CI/CD Integration:
- **GitHub Actions**: Automated testing pipeline
- **Coverage Reports**: Built-in Go coverage tools
- **Quality Gates**: Makefile enforcement
- **Performance Monitoring**: Continuous benchmarking

## 🔄 Maintenance Strategy

### Daily Practices:
1. **Test-Driven Development**: Write tests before implementation
2. **Coverage Monitoring**: Check coverage on every commit
3. **Mock Maintenance**: Regenerate mocks when interfaces change

### Weekly Practices:
1. **Test Review**: Include tests in code review process
2. **Performance Monitoring**: Review benchmark results
3. **Flaky Test Analysis**: Identify and fix unreliable tests

### Monthly Practices:
1. **Architecture Review**: Validate compliance tests
2. **Test Refactoring**: Improve test maintainability
3. **Documentation Update**: Keep test documentation current

### Quarterly Practices:
1. **Testing Strategy Review**: Assess and improve testing approach
2. **Tool Evaluation**: Consider new testing tools and practices
3. **Performance Baseline**: Update performance expectations

## 📋 Checklist for Implementation

### Phase 1 - Foundation
- [ ] Install mockgen and testing tools
- [ ] Create testutil package with helpers
- [ ] Generate mocks for all repository interfaces
- [ ] Update Makefile with test targets
- [ ] Set up test data fixtures

### Phase 2 - Domain Layer
- [ ] Create entity tests with comprehensive validation
- [ ] Expand service tests with error scenarios
- [ ] Add value object tests
- [ ] Achieve target coverage metrics

### Phase 3 - Application Layer
- [ ] Create use case tests with mocked dependencies
- [ ] Test error handling and edge cases
- [ ] Validate business logic workflows
- [ ] Performance test critical paths

### Phase 4 - Infrastructure Layer
- [ ] Test configuration loading and validation
- [ ] Mock external service dependencies
- [ ] Test connection handling and retries
- [ ] Validate error scenarios

### Phase 5 - Presentation & Container
- [ ] Test HTTP handlers and responses
- [ ] Validate dependency injection
- [ ] Test service lifecycle management
- [ ] Error handling validation

### Phase 6 - Integration Testing
- [ ] Set up testcontainers infrastructure
- [ ] Create end-to-end test scenarios
- [ ] Performance and load testing
- [ ] Multi-service integration validation

### Phase 7 - Advanced Features
- [ ] Architecture compliance automation
- [ ] Benchmark test implementation
- [ ] Property-based testing setup
- [ ] CI/CD integration completion

## 🎯 Next Steps

1. **Start with Phase 1** - Set up the testing infrastructure
2. **Focus on Domain Layer** - Get core business logic to 95% coverage
3. **Iterate Quickly** - Implement one phase at a time
4. **Measure Progress** - Track coverage and quality metrics
5. **Get Team Buy-in** - Ensure all developers follow TDD practices

This plan will transform your testing strategy from the current 15% coverage to a comprehensive 90%+ coverage with high-quality, maintainable tests that ensure code reliability and facilitate future development. 