package container

import (
	"context"
	"fmt"
	"log"

	natsgo "github.com/nats-io/nats.go"
	opensearchgo "github.com/opensearch-project/opensearch-go/v2"

	"github.com/linuxfoundation/lfx-indexer-service/internal/application/usecases"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/config"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/janitor"
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
	JanitorService     *janitor.JanitorService

	// Use Cases
	IndexingUseCase          *usecases.IndexingUseCase
	MessageProcessingUseCase *usecases.MessageProcessingUseCase

	// Handlers
	HealthHandler            *handlers.HealthHandler
	IndexingMessageHandler   *handlers.IndexingMessageHandler
	V1IndexingMessageHandler *handlers.V1IndexingMessageHandler
}

// NewContainer creates a new dependency injection container
func NewContainer() (*Container, error) {
	// Load configuration
	config, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	container := &Container{
		Config: config,
	}

	// Initialize dependencies in order
	if err := container.initializeInfrastructure(); err != nil {
		return nil, fmt.Errorf("failed to initialize infrastructure: %w", err)
	}

	if err := container.initializeRepositories(); err != nil {
		return nil, fmt.Errorf("failed to initialize repositories: %w", err)
	}

	if err := container.initializeServices(); err != nil {
		return nil, fmt.Errorf("failed to initialize services: %w", err)
	}

	if err := container.initializeUseCases(); err != nil {
		return nil, fmt.Errorf("failed to initialize use cases: %w", err)
	}

	if err := container.initializeHandlers(); err != nil {
		return nil, fmt.Errorf("failed to initialize handlers: %w", err)
	}

	log.Println("Container initialized successfully")
	return container, nil
}

// initializeInfrastructure initializes infrastructure layer
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
	authRepo, err := jwt.NewAuthRepository(
		c.Config.JWT.Issuer,
		c.Config.JWT.Audiences, // Use audiences array from config
		c.Config.JWT.JWKSURL,   // JWKS URL for key provider
		c.Config.JWT.ClockSkew, // Clock skew tolerance
	)
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

	// Initialize janitor service
	c.JanitorService = janitor.NewJanitorService(
		c.TransactionRepository,
		c.Config.OpenSearch.Index,
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

	// Initialize message processing use case (application layer coordination)
	c.MessageProcessingUseCase = usecases.NewMessageProcessingUseCase(
		c.IndexingUseCase,
		c.JanitorService, // JanitorService implements JanitorServiceInterface
	)

	return nil
}

// initializeHandlers initializes presentation layer
func (c *Container) initializeHandlers() error {
	// Initialize health handler
	c.HealthHandler = handlers.NewHealthHandler()

	// Initialize message handlers (presentation layer)
	c.IndexingMessageHandler = handlers.NewIndexingMessageHandler(c.MessageProcessingUseCase)
	c.V1IndexingMessageHandler = handlers.NewV1IndexingMessageHandler(c.MessageProcessingUseCase)

	return nil
}

// StartServices starts all background services
func (c *Container) StartServices(ctx context.Context) error {
	log.Println("Starting NATS message processing services...")

	// Setup NATS subscriptions
	if err := c.SetupNATSSubscriptions(ctx); err != nil {
		return fmt.Errorf("failed to setup NATS subscriptions: %w", err)
	}

	// Start production janitor service
	if c.Config.Janitor.Enabled && c.JanitorService != nil {
		c.JanitorService.StartItemLoop(ctx)
		log.Println("janitor service started")
	}

	log.Println("NATS message processing services started successfully")
	return nil
}

// SetupNATSSubscriptions sets up all NATS message subscriptions
// Following clean architecture: pure dependency injection, no business logic
// Uses pre-initialized handlers from container (Single Responsibility Principle)
func (c *Container) SetupNATSSubscriptions(ctx context.Context) error {
	// Subscribe to V2 indexing messages with reply support
	if err := c.MessageRepository.QueueSubscribeWithReply(
		ctx,
		c.Config.NATS.IndexingSubject,
		c.Config.NATS.Queue,
		c.IndexingMessageHandler, // Use stored handler
	); err != nil {
		return fmt.Errorf("failed to subscribe to indexing subject: %w", err)
	}

	// Subscribe to V1 indexing messages with reply support
	if err := c.MessageRepository.QueueSubscribeWithReply(
		ctx,
		c.Config.NATS.V1IndexingSubject,
		c.Config.NATS.Queue,
		c.V1IndexingMessageHandler, // Use stored handler
	); err != nil {
		return fmt.Errorf("failed to subscribe to V1 indexing subject: %w", err)
	}

	log.Printf("Subscribed to NATS subjects: %s, %s with queue: %s (with reply support)",
		c.Config.NATS.IndexingSubject,
		c.Config.NATS.V1IndexingSubject,
		c.Config.NATS.Queue,
	)

	return nil
}

// HealthCheck performs a health check on all services
func (c *Container) HealthCheck(ctx context.Context) error {
	// Check NATS connection
	if c.NATSConnection == nil || !c.NATSConnection.IsConnected() {
		return fmt.Errorf("NATS connection is not healthy")
	}

	// Check OpenSearch connection
	if err := c.TransactionRepository.HealthCheck(ctx); err != nil {
		return fmt.Errorf("OpenSearch health check failed: %w", err)
	}

	// Check Message repository (NATS subscriptions)
	if err := c.MessageRepository.HealthCheck(ctx); err != nil {
		return fmt.Errorf("message repository health check failed: %w", err)
	}

	return nil
}

// Close gracefully shuts down the container
func (c *Container) Close() error {
	log.Println("Shutting down container...")

	// Stop production janitor
	if c.JanitorService != nil {
		c.JanitorService.Shutdown()
	}

	// Close message repository (unsubscribe from NATS subscriptions)
	if c.MessageRepository != nil {
		if err := c.MessageRepository.Close(); err != nil {
			log.Printf("Error closing message repository: %v", err)
		}
	}

	// Close NATS connection (after unsubscribing)
	if c.NATSConnection != nil {
		c.NATSConnection.Close()
	}

	log.Println("Container shutdown complete")
	return nil
}
