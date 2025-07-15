# Mock Data Support Plan

**Project**: LFX Indexer Service  
**Date**: January 2025  
**Goal**: Comprehensive mock data strategy for robust testing across all layers  
**Complements**: TEST_IMPROVEMENT_PLAN.md

## 📊 Current State Analysis

### ✅ Existing Mock Infrastructure:
- **Manual Mocks**: Basic repository mocks in `internal/mocks/repositories.go`
- **Simple Test Data**: Basic transaction data in existing tests
- **Mock Framework**: Uses `testify` framework

### ❌ Critical Mock Data Gaps:
- **No Mock Data Builders**: Hard-coded test data in tests
- **No Realistic Data Sets**: Limited to simple "test-project" scenarios
- **No Format Variations**: Missing V1 vs V2 message format mock data
- **No Error Scenarios**: No mock data for malformed/invalid inputs
- **No Performance Data**: No large datasets for load testing
- **No Resource Type Variety**: Only basic project mock data
- **No Mock Data Management**: No organized, reusable mock data library

## 🎯 Mock Data Strategy

### Data Categories to Mock:

#### 1. **NATS Message Data**
- V2 transaction messages (`lfx.index.*`)
- V1 legacy messages (`lfx.v1.index.*`)
- Malformed/invalid messages
- Large message payloads

#### 2. **Resource Types**
- **Projects**: Complete project data with metadata
- **Organizations**: Organization structures  
- **Users**: User profiles and permissions
- **Events**: Event data and participants
- **Funding**: Funding rounds and donations
- **Repositories**: Code repository metadata

#### 3. **Authentication Data**
- **JWT Tokens**: Valid, expired, malformed tokens
- **Principals**: Users, service accounts, machine users
- **Headers**: Authorization headers, custom headers

#### 4. **OpenSearch Data**
- **Index Bodies**: Complete transaction bodies for indexing
- **Search Results**: Realistic search response data
- **Error Responses**: OpenSearch error scenarios

#### 5. **Processing Results**
- **Success Scenarios**: Successful processing outcomes
- **Error Scenarios**: Failed processing with various error types
- **Performance Data**: Timing and throughput metrics

## 🏗️ Implementation Plan

### Phase 1: Mock Data Infrastructure (Week 1-2)

#### 1.1 Create Mock Data Foundation
**Files to Create**:
```
testdata/
├── README.md                           # Mock data documentation
├── fixtures/                           # Static mock data files
│   ├── transactions/                   # Transaction mock data
│   │   ├── v2/                        # V2 format messages
│   │   │   ├── projects_create.json   # Project creation messages
│   │   │   ├── projects_update.json   # Project update messages
│   │   │   ├── projects_delete.json   # Project deletion messages
│   │   │   ├── orgs_create.json      # Organization messages
│   │   │   └── invalid_messages.json # Malformed messages
│   │   ├── v1/                        # V1 legacy format messages
│   │   │   ├── projects_legacy.json   # Legacy project data
│   │   │   ├── users_legacy.json     # Legacy user data
│   │   │   └── migration_data.json   # Migration scenarios
│   │   └── performance/               # Large datasets
│   │       ├── bulk_projects.json     # Bulk project data
│   │       ├── stress_test.json      # Stress test scenarios
│   │       └── concurrent_load.json  # Concurrent processing data
│   ├── principals/                     # Authentication mock data
│   │   ├── valid_tokens.json         # Valid JWT tokens
│   │   ├── expired_tokens.json       # Expired tokens
│   │   ├── malformed_tokens.json     # Invalid tokens
│   │   └── service_accounts.json     # Machine user tokens
│   ├── opensearch/                    # OpenSearch mock data
│   │   ├── index_responses.json      # Successful index responses
│   │   ├── search_results.json       # Search query results
│   │   ├── error_responses.json      # Error scenarios
│   │   └── bulk_operations.json      # Bulk operation data
│   └── config/                        # Configuration mock data
│       ├── valid_configs.json        # Valid configurations
│       ├── invalid_configs.json      # Invalid configurations
│       └── environment_vars.json     # Environment variable sets
└── builders/                          # Go mock data builders
    ├── transaction_builder.go         # Transaction data builder
    ├── principal_builder.go           # Principal data builder
    ├── opensearch_builder.go          # OpenSearch response builder
    ├── message_builder.go             # NATS message builder
    └── scenario_builder.go            # Complete scenario builder
```

#### 1.2 Mock Data Builders
**Create Fluent Builders** (`testdata/builders/`):

```go
// TransactionBuilder - Fluent API for building transaction test data
type TransactionBuilder struct {
    transaction *entities.LFXTransaction
}

func NewTransactionBuilder() *TransactionBuilder
func (b *TransactionBuilder) WithAction(action string) *TransactionBuilder
func (b *TransactionBuilder) WithObjectType(objectType string) *TransactionBuilder
func (b *TransactionBuilder) WithProjectData(name, uid string, public bool) *TransactionBuilder
func (b *TransactionBuilder) WithPrincipals(principals ...entities.Principal) *TransactionBuilder
func (b *TransactionBuilder) WithV1Data(v1Data map[string]any) *TransactionBuilder
func (b *TransactionBuilder) WithHeaders(headers map[string]string) *TransactionBuilder
func (b *TransactionBuilder) WithTimestamp(timestamp time.Time) *TransactionBuilder
func (b *TransactionBuilder) Build() *entities.LFXTransaction

// Usage Examples:
transaction := NewTransactionBuilder().
    WithAction("create").
    WithObjectType("project").
    WithProjectData("Test Project", "test-project-001", true).
    WithPrincipals(NewPrincipalBuilder().WithUser("test-user").Build()).
    Build()
```

#### 1.3 Mock Data Scenarios
**Create Scenario Builders** (`testdata/builders/scenario_builder.go`):

```go
type ScenarioBuilder struct {
    name string
    transactions []*entities.LFXTransaction
    principals []entities.Principal
    expectedResults []*valueobjects.ProcessingResult
}

// Predefined scenarios:
func NewHappyPathScenario() *ScenarioBuilder
func NewErrorScenario() *ScenarioBuilder
func NewPerformanceScenario() *ScenarioBuilder
func NewV1MigrationScenario() *ScenarioBuilder
func NewConcurrentProcessingScenario() *ScenarioBuilder
```

### Phase 2: Resource-Specific Mock Data (Week 2-3)

#### 2.1 Project Mock Data
```go
// Project-specific builders and fixtures
type ProjectDataBuilder struct {
    data map[string]any
}

func (b *ProjectDataBuilder) WithBasicInfo(name, description string) *ProjectDataBuilder
func (b *ProjectDataBuilder) WithMetadata(category, status string) *ProjectDataBuilder
func (b *ProjectDataBuilder) WithContacts(contacts []entities.ContactBody) *ProjectDataBuilder
func (b *ProjectDataBuilder) WithRepository(repoURL string) *ProjectDataBuilder
func (b *ProjectDataBuilder) WithFunding(budget float64, currency string) *ProjectDataBuilder

// Sample project variations:
- OpenSource projects with repositories
- Commercial projects with funding
- Legacy projects being migrated
- Projects with complex metadata
- Projects with multiple contacts
```

#### 2.2 Multi-Resource Mock Data
```go
// Support for different resource types
type ResourceDataBuilder struct {
    resourceType string
    data map[string]any
}

func NewProjectData() *ResourceDataBuilder
func NewOrganizationData() *ResourceDataBuilder  
func NewUserData() *ResourceDataBuilder
func NewEventData() *ResourceDataBuilder
func NewRepositoryData() *ResourceDataBuilder

// Each with realistic, varied data sets
```

### Phase 3: Message Format Mock Data (Week 3-4)

#### 3.1 V2 Message Formats
```json
// testdata/fixtures/transactions/v2/projects_create.json
{
  "realistic_project_create": {
    "action": "create",
    "data": {
      "uid": "awesome-open-source-project",
      "name": "Awesome Open Source Project",
      "description": "A revolutionary open source project for developers",
      "category": "developer-tools",
      "status": "active",
      "public": true,
      "website": "https://awesome-project.org",
      "repository": "https://github.com/awesome/project",
      "license": "Apache-2.0",
      "founded_date": "2023-01-15",
      "maintainers": ["john.doe@example.com", "jane.smith@example.com"],
      "tags": ["golang", "microservices", "cloud-native"],
      "funding": {
        "total_raised": 500000,
        "currency": "USD",
        "funding_rounds": 2
      },
      "contacts": [
        {
          "lfx_principal": "john.doe@linuxfoundation.org",
          "name": "John Doe",
          "emails": ["john.doe@example.com"],
          "bot": false,
          "profile": {
            "role": "maintainer",
            "github": "johndoe"
          }
        }
      ]
    },
    "headers": {
      "Authorization": "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...",
      "Content-Type": "application/json",
      "User-Agent": "lfx-platform/v2.1.0"
    }
  }
}
```

#### 3.2 V1 Legacy Message Formats
```json
// testdata/fixtures/transactions/v1/projects_legacy.json
{
  "legacy_project_migration": {
    "v1_data": {
      "project_id": "12345",
      "project_name": "Legacy Project",
      "project_status": "Active",
      "created_time": "2020-01-01T00:00:00Z",
      "user_id": "legacy-user-123",
      "user_email": "legacy@example.com",
      "project_category": "infrastructure",
      "platform_db_record": {
        "id": 12345,
        "external_id": "legacy-project-001",
        "metadata": {
          "migrated_from": "platform_v1",
          "migration_date": "2024-01-15"
        }
      }
    },
    "headers": {
      "Authorization": "Bearer legacy-token-format...",
      "Migration-Source": "platform-v1"
    }
  }
}
```

### Phase 4: Error Scenario Mock Data (Week 4-5)

#### 4.1 Malformed Message Data
```json
// testdata/fixtures/transactions/v2/invalid_messages.json
{
  "missing_action": {
    "data": {"uid": "test"},
    "headers": {"Authorization": "Bearer token"}
  },
  "invalid_action": {
    "action": "invalid_action",
    "data": {"uid": "test"},
    "headers": {"Authorization": "Bearer token"}
  },
  "malformed_json": "{ invalid json structure",
  "missing_data": {
    "action": "create",
    "headers": {"Authorization": "Bearer token"}
  },
  "invalid_data_type": {
    "action": "create",
    "data": "should_be_object_not_string",
    "headers": {"Authorization": "Bearer token"}
  }
}
```

#### 4.2 Authentication Error Data
```json
// testdata/fixtures/principals/invalid_tokens.json
{
  "expired_token": {
    "token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.expired...",
    "error": "token expired",
    "expires_at": "2023-01-01T00:00:00Z"
  },
  "malformed_token": {
    "token": "not.a.valid.jwt",
    "error": "invalid token format"
  },
  "missing_claims": {
    "token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.missing_claims...",
    "error": "missing required claims"
  }
}
```

### Phase 5: Performance Testing Mock Data (Week 5-6)

#### 5.1 Large Dataset Generation
```go
// testdata/builders/performance_builder.go
type PerformanceDataBuilder struct {
    messageCount int
    resourceTypes []string
    concurrency int
}

func (b *PerformanceDataBuilder) WithMessageCount(count int) *PerformanceDataBuilder
func (b *PerformanceDataBuilder) WithResourceTypes(types ...string) *PerformanceDataBuilder
func (b *PerformanceDataBuilder) WithConcurrency(level int) *PerformanceDataBuilder
func (b *PerformanceDataBuilder) GenerateMessages() []*entities.LFXTransaction

// Generate datasets:
- 1,000 messages for load testing
- 10,000 messages for stress testing  
- 100,000 messages for scalability testing
- Concurrent message scenarios
- Mixed resource type scenarios
```

#### 5.2 Realistic Data Patterns
```go
// Generate realistic data distributions
func GenerateRealisticProjectDistribution() []*entities.LFXTransaction {
    // 70% create, 25% update, 5% delete
    // Realistic project names, descriptions
    // Varied data sizes (small to large projects)
    // Realistic timestamps and user patterns
}
```

### Phase 6: Integration Testing Mock Data (Week 6-7)

#### 6.1 End-to-End Scenario Data
```yaml
# testdata/scenarios/e2e_scenarios.yaml
scenarios:
  - name: "complete_project_lifecycle"
    description: "Create, update, and delete project with realistic data flow"
    steps:
      - action: "create"
        subject: "lfx.index.project"
        data_file: "fixtures/transactions/v2/projects_create.json"
        expected_outcome: "success"
      - action: "update"
        subject: "lfx.index.project"  
        data_file: "fixtures/transactions/v2/projects_update.json"
        expected_outcome: "success"
      - action: "delete"
        subject: "lfx.index.project"
        data_file: "fixtures/transactions/v2/projects_delete.json"
        expected_outcome: "success"
```

#### 6.2 Multi-Service Integration Data
```go
// Mock data for testing with real external services
type IntegrationMockData struct {
    natsMessages []NATSMessage
    opensearchResponses []OpenSearchResponse
    jwtTokens []JWTToken
}

func NewIntegrationScenario() *IntegrationMockData
func (d *IntegrationMockData) WithRealisticNATSLoad() *IntegrationMockData  
func (d *IntegrationMockData) WithOpenSearchCluster() *IntegrationMockData
func (d *IntegrationMockData) WithJWTAuthService() *IntegrationMockData
```

## 🛠️ Mock Data Utilities

### Utility Functions
```go
// testdata/utils/mock_utils.go

// Data loading utilities
func LoadTransactionFixture(name string) (*entities.LFXTransaction, error)
func LoadPrincipalFixture(name string) (*entities.Principal, error)
func LoadScenarioFixture(name string) (*Scenario, error)

// Data generation utilities  
func GenerateRandomTransaction() *entities.LFXTransaction
func GenerateRandomPrincipal() entities.Principal
func GenerateRandomProjectData() map[string]any

// Data validation utilities
func ValidateTransactionData(t *entities.LFXTransaction) error
func ValidateMessageFormat(data []byte) error
func ValidateOpenSearchBody(body io.Reader) error

// Test assertion utilities
func AssertTransactionEqual(t *testing.T, expected, actual *entities.LFXTransaction)
func AssertProcessingResultSuccess(t *testing.T, result *valueobjects.ProcessingResult)
func AssertOpenSearchIndexed(t *testing.T, docID string, body map[string]any)
```

### Mock Data Management
```go
// testdata/manager/mock_manager.go
type MockDataManager struct {
    fixtures map[string]interface{}
    builders map[string]interface{}
}

func NewMockDataManager() *MockDataManager
func (m *MockDataManager) RegisterFixture(name string, data interface{})
func (m *MockDataManager) GetFixture(name string) (interface{}, error)
func (m *MockDataManager) LoadAllFixtures() error
func (m *MockDataManager) ValidateFixtures() error
```

## 🧪 Integration with Testing Framework

### Enhanced Test Helpers
```go
// testutil/mock_helpers.go

// Test suite with mock data support
type MockDataTestSuite struct {
    suite.Suite
    mockManager *MockDataManager
    fixtures map[string]interface{}
}

func (s *MockDataTestSuite) SetupSuite()
func (s *MockDataTestSuite) SetupTest()
func (s *MockDataTestSuite) TearDownTest()
func (s *MockDataTestSuite) LoadFixture(name string) interface{}
func (s *MockDataTestSuite) CreateTransaction(opts ...TransactionOption) *entities.LFXTransaction
```

### Table-Driven Test Support
```go
// Enhanced table-driven tests with mock data
func TestTransactionProcessing(t *testing.T) {
    tests := []struct {
        name string
        fixture string
        expected ProcessingResult
    }{
        {
            name: "successful_project_creation",
            fixture: "transactions/v2/projects_create.json",
            expected: ProcessingResult{Success: true},
        },
        {
            name: "legacy_project_migration", 
            fixture: "transactions/v1/projects_legacy.json",
            expected: ProcessingResult{Success: true},
        },
        {
            name: "invalid_action_error",
            fixture: "transactions/v2/invalid_messages.json",
            expected: ProcessingResult{Success: false, Error: "unknown action"},
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            transaction := LoadTransactionFixture(tt.fixture)
            result := ProcessTransaction(transaction)
            assert.Equal(t, tt.expected.Success, result.Success)
        })
    }
}
```

## 📈 Success Metrics

### Mock Data Quality Targets:
- **Coverage**: Mock data for 100% of message formats
- **Realism**: 95% of mock data reflects production patterns
- **Variety**: 50+ different test scenarios
- **Performance**: Generate 10,000 mock messages in <1 second
- **Maintainability**: Single source of truth for mock data

### Mock Data Organization:
- **Discoverability**: All mock data documented and cataloged
- **Reusability**: Mock data reused across multiple test suites
- **Versioning**: Mock data versioned with API changes
- **Validation**: All mock data validates against schemas

## 🔄 Maintenance Strategy

### Daily Practices:
1. **Mock Data Updates**: Update mock data when adding new features
2. **Validation**: Ensure mock data stays valid with schema changes
3. **Documentation**: Document new mock data scenarios

### Weekly Practices:
1. **Mock Data Review**: Review and clean up unused mock data
2. **Performance Testing**: Validate mock data generation performance
3. **Scenario Updates**: Add new realistic test scenarios

### Monthly Practices:
1. **Mock Data Audit**: Comprehensive review of all mock data
2. **Production Alignment**: Ensure mock data reflects production patterns
3. **Tool Updates**: Update mock data generation tools

## 🎯 Implementation Priority

### High Priority (Immediate):
1. **Transaction Builders**: Fluent APIs for creating test transactions
2. **Basic Fixtures**: Essential project, principal, and message fixtures
3. **Error Scenarios**: Mock data for common error cases

### Medium Priority (Next Sprint):
1. **Performance Data**: Large datasets for load testing
2. **V1 Migration Data**: Legacy format mock data
3. **Integration Scenarios**: End-to-end test data

### Low Priority (Future):
1. **Advanced Scenarios**: Complex multi-resource test scenarios
2. **Property-Based Data**: Generated property-based test data
3. **Production Mirroring**: Mock data that mirrors production patterns

## 🔗 Integration with Existing Plan

This Mock Data Support Plan **supplements** the TEST_IMPROVEMENT_PLAN.md:

- **Phase 1**: Enhances test infrastructure with comprehensive mock data
- **Phase 2-7**: Provides realistic test data for all testing phases
- **Integration**: Supports integration testing with realistic scenarios
- **Performance**: Enables performance testing with large datasets

Together, these plans provide a complete testing strategy with both test infrastructure and comprehensive mock data support. 

### 🔗 Next Steps

1. Review Both Plans: TEST_IMPROVEMENT_PLAN.md + MOCK_DATA_SUPPORT_PLAN.md
2. Start with Mock Infrastructure: Implement Phase 1 of both plans together
3. Build Mock Data Builders: Create fluent APIs for test data generation
4. Add Realistic Fixtures: JSON files with production-like data
5. Integrate with Tests: Use mock data in your test implementation phases

The two plans work together to give you:

1. 90% test coverage (from TEST_IMPROVEMENT_PLAN.md)
2. Realistic, maintainable test data (from MOCK_DATA_SUPPORT_PLAN.md)
3. Comprehensive testing strategy spanning unit → integration → performance