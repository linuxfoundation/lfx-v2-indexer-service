package container

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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

	// Logging
	Logger *slog.Logger

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
	HealthService      *services.HealthService

	// Use Cases
	IndexingUseCase          *usecases.IndexingUseCase
	MessageProcessingUseCase *usecases.MessageProcessingUseCase

	// Handlers
	HealthHandler            *handlers.HealthHandler
	IndexingMessageHandler   *handlers.IndexingMessageHandler
	V1IndexingMessageHandler *handlers.V1IndexingMessageHandler
}

// NewContainer creates a new dependency injection container
func NewContainer(logger *slog.Logger) (*Container, error) {
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
		Logger: logger, // Use provided logger from main.go
	}

	// Initialize dependencies in order (skip logger initialization)
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

	container.Logger.Info("Container initialized successfully")
	return container, nil
}

// initializeInfrastructure initializes infrastructure layer
func (c *Container) initializeInfrastructure() error {
	c.Logger.Info("Infrastructure initialization started")

	// Initialize NATS connection with error handlers
	c.Logger.Info("Connecting to NATS server",
		"url", c.Config.NATS.URL,
		"max_reconnects", c.Config.NATS.MaxReconnects,
		"reconnect_wait", c.Config.NATS.ReconnectWait,
		"connection_timeout", c.Config.NATS.ConnectionTimeout,
		"drain_timeout", c.Config.NATS.DrainTimeout)

	// Create error handler for NATS connection issues
	errorHandler := natsgo.ErrorHandler(func(conn *natsgo.Conn, sub *natsgo.Subscription, err error) {
		if sub != nil {
			pending, _, _ := sub.Pending()
			delivered, _ := sub.Delivered()
			c.Logger.Error("NATS subscription error",
				"error", err.Error(),
				"subject", sub.Subject,
				"queue", sub.Queue,
				"pending_messages", pending,
				"delivered_messages", delivered)
		} else {
			c.Logger.Error("NATS connection error",
				"error", err.Error(),
				"status", conn.Status(),
				"connected_url", conn.ConnectedUrl())
		}
	})

	// Create closed handler for connection recovery
	closedHandler := natsgo.ClosedHandler(func(conn *natsgo.Conn) {
		// Check if this is a graceful shutdown or unexpected closure
		status := conn.Status()
		c.Logger.Error("NATS connection closed",
			"status", status,
			"last_error", conn.LastError())

		if status == natsgo.CLOSED {
			c.Logger.Error("NATS max reconnects exhausted - connection permanently closed")
			// In a production system, you might want to trigger application shutdown
			// or implement additional recovery logic here
		}
	})

	// Create disconnect handler for connection state tracking
	disconnectHandler := natsgo.DisconnectErrHandler(func(conn *natsgo.Conn, err error) {
		c.Logger.Warn("NATS connection disconnected",
			"error", err.Error(),
			"status", conn.Status())
	})

	// Create reconnect handler for connection state tracking
	reconnectHandler := natsgo.ReconnectHandler(func(conn *natsgo.Conn) {
		c.Logger.Info("NATS connection reconnected",
			"connected_url", conn.ConnectedUrl(),
			"status", conn.Status())
	})

	natsConn, err := natsgo.Connect(
		c.Config.NATS.URL,
		natsgo.MaxReconnects(c.Config.NATS.MaxReconnects),
		natsgo.ReconnectWait(c.Config.NATS.ReconnectWait),
		natsgo.Timeout(c.Config.NATS.ConnectionTimeout),
		natsgo.DrainTimeout(c.Config.NATS.DrainTimeout),
		errorHandler,
		closedHandler,
		disconnectHandler,
		reconnectHandler,
	)
	if err != nil {
		c.Logger.Error("Failed to connect to NATS",
			"url", c.Config.NATS.URL,
			"error", err.Error(),
			"max_reconnects", c.Config.NATS.MaxReconnects)
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	c.NATSConnection = natsConn
	c.Logger.Info("NATS connection established successfully",
		"url", c.Config.NATS.URL,
		"status", natsConn.Status(),
		"connected_url", natsConn.ConnectedUrl(),
		"server_info", natsConn.ConnectedServerName())

	// Initialize OpenSearch client
	c.Logger.Info("Creating OpenSearch client",
		"url", c.Config.OpenSearch.URL,
		"index", c.Config.OpenSearch.Index)

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
		c.Logger.Error("Failed to create OpenSearch client",
			"url", c.Config.OpenSearch.URL,
			"error", err.Error())
		return fmt.Errorf("failed to create OpenSearch client: %w", err)
	}
	c.OpenSearchClient = opensearchClient
	c.Logger.Info("OpenSearch client created successfully",
		"url", c.Config.OpenSearch.URL)

	c.Logger.Info("Infrastructure initialization completed successfully")
	return nil
}

// initializeRepositories initializes repository layer
func (c *Container) initializeRepositories() error {
	c.Logger.Info("Repository initialization started")

	// Initialize transaction repository
	c.Logger.Debug("Creating transaction repository",
		"opensearch_url", c.Config.OpenSearch.URL)
	c.TransactionRepository = opensearchpkg.NewTransactionRepository(c.OpenSearchClient, c.Logger)
	c.Logger.Info("Transaction repository initialized")

	// Initialize message repository
	c.Logger.Debug("Creating message repository",
		"nats_url", c.Config.NATS.URL,
		"drain_timeout", c.Config.NATS.DrainTimeout)
	c.MessageRepository = natspkg.NewMessageRepository(c.NATSConnection, c.Logger, c.Config.NATS.DrainTimeout)
	c.Logger.Info("Message repository initialized", "drain_timeout", c.Config.NATS.DrainTimeout)

	// Initialize auth repository
	c.Logger.Info("Creating auth repository",
		"issuer", c.Config.JWT.Issuer,
		"audiences", c.Config.JWT.Audiences,
		"jwks_url", c.Config.JWT.JWKSURL,
		"clock_skew", c.Config.JWT.ClockSkew)

	authRepo, err := jwt.NewAuthRepository(
		c.Config.JWT.Issuer,
		c.Config.JWT.Audiences, // Use audiences array from config
		c.Config.JWT.JWKSURL,   // JWKS URL for key provider
		c.Config.JWT.ClockSkew, // Clock skew tolerance
	)
	if err != nil {
		c.Logger.Error("Failed to create auth repository",
			"issuer", c.Config.JWT.Issuer,
			"jwks_url", c.Config.JWT.JWKSURL,
			"error", err.Error())
		return fmt.Errorf("failed to create auth repository: %w", err)
	}
	c.AuthRepository = authRepo
	c.Logger.Info("Auth repository initialized successfully",
		"issuer", c.Config.JWT.Issuer,
		"audience_count", len(c.Config.JWT.Audiences))

	c.Logger.Info("Repository initialization completed successfully")
	return nil
}

// initializeServices initializes service layer
func (c *Container) initializeServices() error {
	// Initialize transaction service
	c.TransactionService = services.NewTransactionService(
		c.TransactionRepository,
		c.AuthRepository,
		c.Logger,
	)

	// Initialize janitor service
	c.JanitorService = janitor.NewJanitorService(
		c.TransactionRepository,
		c.Logger,
		c.Config.OpenSearch.Index,
	)

	// Initialize health service with repository dependencies
	c.HealthService = services.NewHealthService(
		c.TransactionRepository,
		c.MessageRepository,
		c.AuthRepository,
		c.Logger,
		c.Config.Health.CheckTimeout,
		c.Config.Health.CacheDuration,
	)

	return nil
}

// initializeUseCases initializes use case layer
func (c *Container) initializeUseCases() error {
	// Initialize indexing use case
	c.IndexingUseCase = usecases.NewIndexingUseCase(
		c.TransactionService,
		c.MessageRepository,
		c.Logger,
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
	// Initialize health handler with health service
	c.HealthHandler = handlers.NewHealthHandler(c.HealthService)

	// Initialize message handlers (presentation layer)
	c.IndexingMessageHandler = handlers.NewIndexingMessageHandler(c.MessageProcessingUseCase)
	c.V1IndexingMessageHandler = handlers.NewV1IndexingMessageHandler(c.MessageProcessingUseCase)

	return nil
}

// StartServices starts all background services
func (c *Container) StartServices(ctx context.Context) error {
	c.Logger.Info("Starting NATS message processing services...")

	// Setup NATS subscriptions
	if err := c.SetupNATSSubscriptions(ctx); err != nil {
		return fmt.Errorf("failed to setup NATS subscriptions: %w", err)
	}

	// Start production janitor service
	if c.Config.Janitor.Enabled && c.JanitorService != nil {
		c.JanitorService.StartItemLoop(ctx)
		c.Logger.Info("janitor service started")
	}

	c.Logger.Info("NATS message processing services started successfully")
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

	c.Logger.Info("Subscribed to NATS subjects",
		"indexing_subject", c.Config.NATS.IndexingSubject,
		"v1_indexing_subject", c.Config.NATS.V1IndexingSubject,
		"queue", c.Config.NATS.Queue,
	)

	return nil
}

// HealthCheck performs a comprehensive health check on all services using the health service
func (c *Container) HealthCheck(ctx context.Context) error {
	if c.HealthService == nil {
		return fmt.Errorf("health service is not initialized")
	}

	status := c.HealthService.CheckReadiness(ctx)

	if status.Status == "unhealthy" {
		var errors []string
		for name, check := range status.Checks {
			if check.Status == "unhealthy" {
				errors = append(errors, fmt.Sprintf("%s: %s", name, check.Error))
			}
		}
		return fmt.Errorf("health check failed: %s", strings.Join(errors, "; "))
	}

	return nil
}

// GracefulShutdown performs graceful shutdown with NATS drain
func (c *Container) GracefulShutdown() error {
	c.Logger.Info("Starting graceful shutdown sequence")

	var errors []error

	// First, drain NATS connections gracefully
	if c.MessageRepository != nil {
		c.Logger.Info("Draining NATS connections")
		if err := c.MessageRepository.DrainWithTimeout(); err != nil {
			c.Logger.Error("Failed to drain NATS connections", "error", err.Error())
			errors = append(errors, fmt.Errorf("failed to drain NATS: %w", err))
		} else {
			c.Logger.Info("NATS connections drained successfully")
		}
	}

	// Stop janitor service
	if c.JanitorService != nil {
		c.Logger.Info("Stopping janitor service")
		c.JanitorService.Shutdown()
		c.Logger.Info("Janitor service stopped")
	}

	// Finally, close all remaining resources
	if err := c.Close(); err != nil {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return fmt.Errorf("graceful shutdown completed with errors: %v", errors)
	}

	c.Logger.Info("Graceful shutdown completed successfully")
	return nil
}

// Close gracefully shuts down the container
func (c *Container) Close() error {
	if c.Logger != nil {
		c.Logger.Info("Container shutdown initiated")
	}

	var errors []error

	// Close message repository (includes NATS connection)
	if c.MessageRepository != nil {
		if c.Logger != nil {
			c.Logger.Info("Closing message repository")
		}
		if err := c.MessageRepository.Close(); err != nil {
			if c.Logger != nil {
				c.Logger.Error("Failed to close message repository", "error", err.Error())
			}
			errors = append(errors, fmt.Errorf("failed to close message repository: %w", err))
		} else {
			if c.Logger != nil {
				c.Logger.Info("Message repository closed successfully")
			}
		}
	}

	// Close janitor service
	if c.JanitorService != nil {
		if c.Logger != nil {
			c.Logger.Info("Stopping janitor service")
		}
		c.JanitorService.Shutdown()
		if c.Logger != nil {
			c.Logger.Info("Janitor service stopped")
		}
	}

	// Log completion
	if c.Logger != nil {
		if len(errors) == 0 {
			c.Logger.Info("Container shutdown completed successfully")
		} else {
			c.Logger.Error("Container shutdown completed with errors",
				"error_count", len(errors))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("shutdown errors: %v", errors)
	}

	return nil
}
