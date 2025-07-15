package container

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	natsgo "github.com/nats-io/nats.go"
	opensearchgo "github.com/opensearch-project/opensearch-go/v2"

	"github.com/linuxfoundation/lfx-indexer-service/internal/application/usecases"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/config"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/jwt"
	natspkg "github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/nats"
	opensearchpkg "github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/opensearch"
	"github.com/linuxfoundation/lfx-indexer-service/internal/presentation/handlers"
)

// Container holds all dependencies
type Container struct {
	// Configuration
	Config *config.AppConfig

	// Infrastructure
	NATSConnection   *natsgo.Conn
	OpenSearchClient *opensearchgo.Client

	// Repositories
	TransactionRepository *opensearchpkg.TransactionRepository
	MessageRepository     *natspkg.MessageRepository
	AuthRepository        *jwt.AuthRepository

	// Services
	TransactionService *services.TransactionService

	// Use Cases
	IndexingUseCase *usecases.IndexingUseCase
	JanitorUseCase  *usecases.JanitorUseCase

	// Handlers
	HealthHandler *handlers.HealthHandler
}

// NewContainer creates a new dependency injection container
func NewContainer() (*Container, error) {
	// Load configuration
	config, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	container := &Container{
		Config: config,
	}

	// Initialize infrastructure dependencies
	if err := container.initializeInfrastructure(); err != nil {
		return nil, fmt.Errorf("failed to initialize infrastructure: %w", err)
	}

	// Initialize repositories
	if err := container.initializeRepositories(); err != nil {
		return nil, fmt.Errorf("failed to initialize repositories: %w", err)
	}

	// Initialize services
	if err := container.initializeServices(); err != nil {
		return nil, fmt.Errorf("failed to initialize services: %w", err)
	}

	// Initialize use cases
	if err := container.initializeUseCases(); err != nil {
		return nil, fmt.Errorf("failed to initialize use cases: %w", err)
	}

	// Initialize handlers
	if err := container.initializeHandlers(); err != nil {
		return nil, fmt.Errorf("failed to initialize handlers: %w", err)
	}

	return container, nil
}

// initializeInfrastructure initializes infrastructure dependencies
func (c *Container) initializeInfrastructure() error {
	// Initialize NATS connection
	natsConn, err := natsgo.Connect(
		c.Config.NATS.URL,
		natsgo.MaxReconnects(c.Config.NATS.MaxReconnects),
		natsgo.ReconnectWait(c.Config.NATS.ReconnectWait),
		natsgo.Timeout(c.Config.NATS.ConnectionTimeout),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	c.NATSConnection = natsConn

	// Initialize OpenSearch client
	opensearchConfig := opensearchgo.Config{
		Addresses: []string{c.Config.OpenSearch.URL},
		/*Username:  c.Config.OpenSearch.Username,
		Password:  c.Config.OpenSearch.Password,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},*/
	}
	opensearchClient, err := opensearchgo.NewClient(opensearchConfig)
	if err != nil {
		return fmt.Errorf("failed to create OpenSearch client: %w", err)
	}
	c.OpenSearchClient = opensearchClient

	return nil
}

// initializeRepositories initializes repository layer
func (c *Container) initializeRepositories() error {
	// Initialize transaction repository
	c.TransactionRepository = opensearchpkg.NewTransactionRepository(c.OpenSearchClient)

	// Initialize message repository
	c.MessageRepository = natspkg.NewMessageRepository(c.NATSConnection)

	// Initialize auth repository
	authRepo, err := jwt.NewAuthRepository(c.Config.JWT.Issuer, c.Config.JWT.Audience)
	if err != nil {
		return fmt.Errorf("failed to create auth repository: %w", err)
	}
	c.AuthRepository = authRepo

	return nil
}

// initializeServices initializes service layer
func (c *Container) initializeServices() error {
	// Initialize transaction service
	c.TransactionService = services.NewTransactionService(
		c.TransactionRepository,
		c.AuthRepository,
	)

	return nil
}

// initializeUseCases initializes use case layer
func (c *Container) initializeUseCases() error {
	// Initialize indexing use case
	c.IndexingUseCase = usecases.NewIndexingUseCase(
		c.TransactionService,
		c.MessageRepository,
		c.Config.OpenSearch.Index,
		c.Config.NATS.Queue,
	)

	// Initialize janitor use case
	janitorInterval := c.Config.Janitor.Interval
	if janitorInterval == 0 {
		janitorInterval = 5 * time.Minute // Default interval
	}
	if intervalStr := os.Getenv("JANITOR_INTERVAL"); intervalStr != "" {
		if interval, err := time.ParseDuration(intervalStr); err == nil {
			janitorInterval = interval
		}
	}

	janitorBatchSize := c.Config.Janitor.BatchSize
	if janitorBatchSize == 0 {
		janitorBatchSize = 100 // Default batch size
	}
	if batchSizeStr := os.Getenv("JANITOR_BATCH_SIZE"); batchSizeStr != "" {
		if batchSize, err := strconv.Atoi(batchSizeStr); err == nil {
			janitorBatchSize = batchSize
		}
	}

	c.JanitorUseCase = usecases.NewJanitorUseCase(
		c.TransactionRepository,
		c.Config.OpenSearch.Index,
		janitorInterval,
		janitorBatchSize,
	)

	return nil
}

// initializeHandlers initializes presentation layer
func (c *Container) initializeHandlers() error {
	// Initialize health handler
	c.HealthHandler = handlers.NewHealthHandler()

	return nil
}

// StartServices starts all background services
func (c *Container) StartServices(ctx context.Context) error {
	log.Println("Starting NATS message processing services...")

	// Setup NATS subscriptions
	if err := c.SetupNATSSubscriptions(ctx); err != nil {
		return fmt.Errorf("failed to setup NATS subscriptions: %w", err)
	}

	// Start janitor if enabled
	if c.Config.Janitor.Enabled {
		go func() {
			if err := c.JanitorUseCase.StartJanitorTasks(ctx); err != nil {
				log.Printf("Janitor stopped: %v", err)
			}
		}()
	}

	log.Println("NATS message processing services started successfully")
	return nil
}

// SetupNATSSubscriptions sets up all NATS message subscriptions
func (c *Container) SetupNATSSubscriptions(ctx context.Context) error {
	log.Println("Setting up NATS subscriptions with queue groups...")

	// Start indexing subscription for regular messages
	log.Printf("Setting up indexing subscription for subject: %s (queue: %s)", c.Config.NATS.IndexingSubject, c.Config.NATS.Queue)
	if err := c.IndexingUseCase.StartIndexingSubscription(ctx, c.Config.NATS.IndexingSubject); err != nil {
		return fmt.Errorf("failed to start indexing subscription: %w", err)
	}

	// Start V1 indexing subscription for legacy messages
	log.Printf("Setting up V1 indexing subscription for subject: %s (queue: %s)", c.Config.NATS.V1IndexingSubject, c.Config.NATS.Queue)
	if err := c.IndexingUseCase.StartV1IndexingSubscription(ctx, c.Config.NATS.V1IndexingSubject); err != nil {
		return fmt.Errorf("failed to start V1 indexing subscription: %w", err)
	}

	log.Printf("NATS queue subscriptions setup complete:")
	log.Printf("  - Indexing subject: %s", c.Config.NATS.IndexingSubject)
	log.Printf("  - V1 indexing subject: %s", c.Config.NATS.V1IndexingSubject)
	log.Printf("  - Queue group: %s", c.Config.NATS.Queue)
	log.Printf("  - Active subscriptions: %d", c.MessageRepository.GetSubscriptionCount())

	return nil
}

// HealthCheck performs health checks on all dependencies
func (c *Container) HealthCheck(ctx context.Context) error {
	// Check NATS connection
	if err := c.MessageRepository.HealthCheck(ctx); err != nil {
		return fmt.Errorf("NATS health check failed: %w", err)
	}

	// Check OpenSearch connection
	if err := c.TransactionRepository.HealthCheck(ctx); err != nil {
		return fmt.Errorf("OpenSearch health check failed: %w", err)
	}

	// Check Auth service
	if err := c.AuthRepository.HealthCheck(ctx); err != nil {
		return fmt.Errorf("Auth health check failed: %w", err)
	}

	return nil
}

// Close closes all resources
func (c *Container) Close() error {
	log.Println("Closing container resources...")

	// Close repositories
	if c.MessageRepository != nil {
		if err := c.MessageRepository.Close(); err != nil {
			log.Printf("Error closing message repository: %v", err)
		}
	}

	// Close NATS connection directly
	if c.NATSConnection != nil {
		c.NATSConnection.Close()
	}

	log.Println("Container resources closed")
	return nil
}
