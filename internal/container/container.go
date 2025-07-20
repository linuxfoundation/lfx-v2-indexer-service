// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package container

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"

	natsgo "github.com/nats-io/nats.go"
	opensearchgo "github.com/opensearch-project/opensearch-go/v2"

	"github.com/linuxfoundation/lfx-indexer-service/internal/application"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/auth"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/config"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/janitor"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/messaging"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/storage"
	"github.com/linuxfoundation/lfx-indexer-service/internal/presentation/handlers"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/constants"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
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

	// Repositories (consolidated)
	StorageRepository   *storage.StorageRepository
	MessagingRepository *messaging.MessagingRepository
	AuthRepository      *auth.AuthRepository

	// Services (consolidated)
	IndexerService *services.IndexerService
	JanitorService *janitor.JanitorService

	// Application Layer (consolidated)
	MessageProcessor *application.MessageProcessor

	// Handlers (consolidated)
	HealthHandler          *handlers.HealthHandler
	IndexingMessageHandler *handlers.IndexingMessageHandler // Unified handler for both V2 and V1
}

// NewContainer creates a new dependency injection container with CLI overrides
func NewContainer(logger *slog.Logger, cliConfig *config.CLIConfig) (*Container, error) {
	// Validate required parameters
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}

	// Load base configuration
	config, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// Apply CLI overrides to configuration
	if cliConfig != nil {
		if cliConfig.Port != "" {
			port, err := strconv.Atoi(cliConfig.Port)
			if err != nil {
				return nil, fmt.Errorf("invalid port value from CLI: %s - %w", cliConfig.Port, err)
			}
			config.Server.Port = port
			logger.Info("Port overridden by CLI flag", "port", port)
		}

		if cliConfig.NoJanitor {
			config.Janitor.Enabled = false
			logger.Info("Janitor disabled by CLI flag")
		}

		if cliConfig.Debug {
			config.Logging.Level = "debug"
			logger.Info("Debug logging enabled by CLI flag")
		}

		if cliConfig.SimpleHealth {
			config.Health.EnableDetailedResponse = false
			logger.Info("Simple health responses enabled by CLI flag")
		}

		// Note: bind address is handled at HTTP server level, not in config
		if cliConfig.Bind != "*" && cliConfig.Bind != "" {
			logger.Info("Bind address set by CLI flag", "bind", cliConfig.Bind)
		}
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

	if err := container.initializeApplication(); err != nil {
		return nil, fmt.Errorf("failed to initialize application layer: %w", err)
	}

	if err := container.initializeHandlers(); err != nil {
		return nil, fmt.Errorf("failed to initialize handlers: %w", err)
	}

	container.Logger.Info("Container initialized successfully")
	return container, nil
}

// initializeInfrastructure initializes infrastructure layer
func (c *Container) initializeInfrastructure() error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.Info("Infrastructure initialization started")

	// Initialize NATS connection with error handlers
	logger.Info("Connecting to NATS server",
		"url", c.Config.NATS.URL,
		"max_reconnects", c.Config.NATS.MaxReconnects,
		"reconnect_wait", c.Config.NATS.ReconnectWait,
		"connection_timeout", c.Config.NATS.ConnectionTimeout,
		"drain_timeout", c.Config.NATS.DrainTimeout)

	// Validate NATS URL format
	if err := validateNATSURL(c.Config.NATS.URL); err != nil {
		logger.Error("Failed to connect to NATS - invalid URL",
			"url", c.Config.NATS.URL,
			"error", err.Error())
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create error handler for NATS connection issues with enhanced context
	errorHandler := func(conn *natsgo.Conn, sub *natsgo.Subscription, err error) {
		// Get connection statistics for context
		stats := conn.Stats()

		if sub != nil {
			pending, _, _ := sub.Pending()
			delivered, _ := sub.Delivered()
			logger.Error("NATS subscription error with full context",
				"error", err.Error(),
				"subject", sub.Subject,
				"queue", sub.Queue,
				"pending_messages", pending,
				"delivered_messages", delivered,
				"connection_status", conn.Status(),
				"connected_url", conn.ConnectedUrl(),
				"reconnects", stats.Reconnects,
				"in_msgs", stats.InMsgs,
				"out_msgs", stats.OutMsgs,
				"in_bytes", stats.InBytes,
				"out_bytes", stats.OutBytes)
		} else {
			logger.Error("NATS connection error with full context",
				"error", err.Error(),
				"connection_status", conn.Status(),
				"connected_url", conn.ConnectedUrl(),
				"connected_server", conn.ConnectedServerName(),
				"reconnects", stats.Reconnects,
				"in_msgs", stats.InMsgs,
				"out_msgs", stats.OutMsgs,
				"in_bytes", stats.InBytes,
				"out_bytes", stats.OutBytes,
				"last_error", conn.LastError())
		}
	}

	// Create closed handler for connection recovery with enhanced monitoring
	closedHandler := func(conn *natsgo.Conn) {
		// Check if this is a graceful shutdown or unexpected closure
		status := conn.Status()
		stats := conn.Stats()
		lastError := conn.LastError()

		logger.Error("NATS connection closed with full context",
			"status", status,
			"last_error", lastError,
			"reconnects", stats.Reconnects,
			"total_in_msgs", stats.InMsgs,
			"total_out_msgs", stats.OutMsgs,
			"total_in_bytes", stats.InBytes,
			"total_out_bytes", stats.OutBytes,
			"connected_server", conn.ConnectedServerName())

		if status == natsgo.CLOSED {
			logger.Error("NATS max reconnects exhausted - connection permanently closed",
				"max_reconnects_reached", c.Config.NATS.MaxReconnects,
				"actual_reconnects", stats.Reconnects,
				"final_error", lastError)
		}
	}

	// Create reconnect handler for recovery monitoring
	reconnectedHandler := func(conn *natsgo.Conn) {
		stats := conn.Stats()
		logger.Info("NATS reconnected successfully",
			"connected_url", conn.ConnectedUrl(),
			"connected_server", conn.ConnectedServerName(),
			"total_reconnects", stats.Reconnects,
			"status", conn.Status())
	}

	natsOptions := []natsgo.Option{
		natsgo.MaxReconnects(c.Config.NATS.MaxReconnects),
		natsgo.ReconnectWait(c.Config.NATS.ReconnectWait),
		natsgo.Timeout(c.Config.NATS.ConnectionTimeout),
		natsgo.DrainTimeout(c.Config.NATS.DrainTimeout),
		natsgo.ErrorHandler(errorHandler),
		natsgo.ClosedHandler(closedHandler),
		natsgo.ReconnectHandler(reconnectedHandler),
		natsgo.RetryOnFailedConnect(true),
	}

	natsConn, err := natsgo.Connect(c.Config.NATS.URL, natsOptions...)
	if err != nil {
		logger.Error("Failed to connect to NATS",
			"url", c.Config.NATS.URL,
			"error", err.Error())
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	c.NATSConnection = natsConn
	logger.Info("NATS connection established successfully",
		"url", c.Config.NATS.URL,
		"connected_server", natsConn.ConnectedServerName(),
		"status", natsConn.Status())

	// Initialize OpenSearch client
	logger.Info("Creating OpenSearch client",
		"url", c.Config.OpenSearch.URL,
		"index", c.Config.OpenSearch.Index)

	// Validate OpenSearch URL format
	if err := validateOpenSearchURL(c.Config.OpenSearch.URL); err != nil {
		logger.Error("Failed to create OpenSearch client - invalid URL",
			"url", c.Config.OpenSearch.URL,
			"error", err.Error())
		return fmt.Errorf("failed to create OpenSearch client: %w", err)
	}

	opensearchConfig := opensearchgo.Config{
		Addresses: []string{c.Config.OpenSearch.URL},
	}
	opensearchClient, err := opensearchgo.NewClient(opensearchConfig)
	if err != nil {
		logger.Error("Failed to create OpenSearch client",
			"url", c.Config.OpenSearch.URL,
			"error", err.Error())
		return fmt.Errorf("failed to create OpenSearch client: %w", err)
	}
	c.OpenSearchClient = opensearchClient
	logger.Info("OpenSearch client created successfully",
		"url", c.Config.OpenSearch.URL)

	logger.Info("Infrastructure initialization completed successfully")
	return nil
}

// initializeRepositories initializes repository layer with consolidated interfaces
func (c *Container) initializeRepositories() error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.Info("Repository initialization started")

	// Initialize auth repository first (needed by messaging repository)
	logger.Info("Creating auth repository",
		"issuer", c.Config.JWT.Issuer,
		"audiences", c.Config.JWT.Audiences,
		"jwks_url", c.Config.JWT.JWKSURL,
		"clock_skew", c.Config.JWT.ClockSkew)

	authRepo, err := auth.NewAuthRepository(
		c.Config.JWT.Issuer,
		c.Config.JWT.Audiences,
		c.Config.JWT.JWKSURL,
		c.Config.JWT.ClockSkew,
		logger,
	)
	if err != nil {
		logger.Error("Failed to create auth repository",
			"issuer", c.Config.JWT.Issuer,
			"jwks_url", c.Config.JWT.JWKSURL,
			"error", err.Error())
		return fmt.Errorf("failed to create auth repository: %w", err)
	}
	c.AuthRepository = authRepo
	logger.Info("Auth repository initialized successfully",
		"issuer", c.Config.JWT.Issuer,
		"audience_count", len(c.Config.JWT.Audiences))

	// Initialize storage repository
	logger.Debug("Creating storage repository",
		"opensearch_url", c.Config.OpenSearch.URL)
	c.StorageRepository = storage.NewStorageRepository(c.OpenSearchClient, c.Logger)
	logger.Info("Storage repository initialized")

	// Initialize messaging repository
	logger.Debug("Creating messaging repository",
		"nats_url", c.Config.NATS.URL,
		"drain_timeout", c.Config.NATS.DrainTimeout)
	c.MessagingRepository = messaging.NewMessagingRepository(
		c.NATSConnection,
		c.AuthRepository,
		c.Logger,
		c.Config.NATS.DrainTimeout,
	)
	logger.Info("Messaging repository initialized with auth delegation",
		"drain_timeout", c.Config.NATS.DrainTimeout)

	logger.Info("Repository initialization completed successfully")
	return nil
}

// initializeServices initializes service layer
func (c *Container) initializeServices() error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.Info("Service initialization started")

	// Initialize indexer service
	c.IndexerService = services.NewIndexerService(
		c.StorageRepository,
		c.MessagingRepository,
		c.Logger,
	)
	logger.Info("Indexer service initialized")

	// Initialize janitor service (kept separate as domain service)
	c.JanitorService = janitor.NewJanitorService(
		c.StorageRepository,
		c.Logger,
		c.Config.OpenSearch.Index,
	)
	logger.Info("Janitor service initialized")

	logger.Info("Service initialization completed successfully")
	return nil
}

// initializeApplication initializes application layer
func (c *Container) initializeApplication() error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.Info("Application layer initialization started")

	// Initialize message processor
	c.MessageProcessor = application.NewMessageProcessor(
		c.IndexerService,
		c.MessagingRepository,
		c.JanitorService,
		c.Logger,
	)
	logger.Info("Message processor initialized")

	logger.Info("Application layer initialization completed successfully")
	return nil
}

// initializeHandlers initializes presentation layer
func (c *Container) initializeHandlers() error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.Info("Handler initialization started")

	// Check for required dependencies
	if c.IndexerService == nil {
		return fmt.Errorf("indexer service is not initialized")
	}
	if c.MessageProcessor == nil {
		return fmt.Errorf("message processor is not initialized")
	}

	// Initialize health handler with indexer service
	simpleResponse := !c.Config.Health.EnableDetailedResponse
	c.HealthHandler = handlers.NewHealthHandler(c.IndexerService, simpleResponse)
	logger.Info("Health handler initialized", "simple_response", simpleResponse)

	// Initialize unified message handler (handles both V2 and V1)
	c.IndexingMessageHandler = handlers.NewIndexingMessageHandler(c.MessageProcessor)
	logger.Info("Unified message handler initialized (handles both V2 and V1)")

	logger.Info("Handler initialization completed successfully")
	return nil
}

// StartServices starts all background services
func (c *Container) StartServices(ctx context.Context) error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.Info("Starting NATS message processing services...")

	// Check for required components
	if c.Config == nil {
		return fmt.Errorf("container config is not initialized")
	}

	// Setup NATS subscriptions
	if err := c.SetupNATSSubscriptions(ctx); err != nil {
		return fmt.Errorf("failed to setup NATS subscriptions: %w", err)
	}

	// Start production janitor service
	if c.Config.Janitor.Enabled && c.JanitorService != nil {
		c.JanitorService.StartItemLoop(ctx)
		logger.Info("Janitor service started")
	}

	logger.Info("NATS message processing services started successfully")
	return nil
}

// StartServicesWithWaitGroup starts all background services with WaitGroup coordination
func (c *Container) StartServicesWithWaitGroup(ctx context.Context, wg *sync.WaitGroup) error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.Info("Starting NATS message processing services with WaitGroup coordination...")

	// Check for required components
	if c.Config == nil {
		return fmt.Errorf("container config is not initialized")
	}

	// Setup NATS subscriptions
	if err := c.SetupNATSSubscriptions(ctx); err != nil {
		return fmt.Errorf("failed to setup NATS subscriptions: %w", err)
	}

	// Add NATS to WaitGroup for shutdown coordination
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Debug("NATS shutdown coordinator started")
		<-ctx.Done() // Wait for shutdown signal
		logger.Info("NATS shutdown initiated...")

		// Drain NATS connections
		if c.MessagingRepository != nil {
			if err := c.MessagingRepository.DrainWithTimeout(); err != nil {
				logger.Error("Failed to drain NATS connections", "error", err.Error())
			} else {
				logger.Info("NATS connections drained successfully")
			}
		}
		logger.Info("NATS shutdown completed")
	}()

	// Start production janitor service with WaitGroup coordination
	if c.Config.Janitor.Enabled && c.JanitorService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Debug("Janitor shutdown coordinator started")
			<-ctx.Done() // Wait for shutdown signal
			logger.Info("Janitor service shutdown initiated...")
			c.JanitorService.Shutdown()
			logger.Info("Janitor service shutdown completed")
		}()

		c.JanitorService.StartItemLoop(ctx)
		logger.Info("Janitor service started with WaitGroup coordination")
	}

	logger.Info("NATS message processing services started successfully with WaitGroup coordination")
	return nil
}

// SetupNATSSubscriptions sets up all NATS message subscriptions
func (c *Container) SetupNATSSubscriptions(ctx context.Context) error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)

	// Check for required components
	if c.Config == nil {
		return fmt.Errorf("container config is not initialized")
	}

	if c.MessagingRepository == nil {
		return fmt.Errorf("failed to subscribe - messaging repository is not initialized")
	}

	if c.IndexingMessageHandler == nil {
		return fmt.Errorf("indexing message handler is not initialized")
	}

	logger.Info("Setting up NATS subscriptions...",
		"queue", c.Config.NATS.Queue)

	// Subscribe to indexing messages with queue group for load balancing
	indexingSubject := constants.SubjectIndexing
	if err := c.MessagingRepository.QueueSubscribe(ctx, indexingSubject, c.Config.NATS.Queue, c.IndexingMessageHandler); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", indexingSubject, err)
	}
	logger.Info("NATS subscription established", "subject", indexingSubject, "queue", c.Config.NATS.Queue)

	// Subscribe to v1 indexing messages with the same unified handler
	v1IndexingSubject := constants.SubjectV1Indexing
	if err := c.MessagingRepository.QueueSubscribe(ctx, v1IndexingSubject, c.Config.NATS.Queue, c.IndexingMessageHandler); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", v1IndexingSubject, err)
	}
	logger.Info("NATS subscription established", "subject", v1IndexingSubject, "queue", c.Config.NATS.Queue)

	logger.Info("All NATS subscriptions established successfully")
	return nil
}

// HealthCheck performs a comprehensive health check on all services using the health service
func (c *Container) HealthCheck(ctx context.Context) error {
	if c.IndexerService == nil {
		return fmt.Errorf("indexer service is not initialized")
	}

	status := c.IndexerService.CheckReadiness(ctx)

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
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.Info("Starting graceful shutdown sequence")

	var errors []error

	// First, drain NATS connections gracefully
	if c.MessagingRepository != nil {
		logger.Info("Draining NATS connections")
		if err := c.MessagingRepository.DrainWithTimeout(); err != nil {
			logger.Error("Failed to drain NATS connections", "error", err.Error())
			errors = append(errors, fmt.Errorf("failed to drain NATS: %w", err))
		} else {
			logger.Info("NATS connections drained successfully")
		}
	}

	// Stop janitor service
	if c.JanitorService != nil {
		logger.Info("Stopping janitor service")
		c.JanitorService.Shutdown()
		logger.Info("Janitor service stopped")
	}

	// Finally, close all remaining resources
	if err := c.Shutdown(context.Background()); err != nil {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return fmt.Errorf("graceful shutdown completed with errors: %v", errors)
	}

	logger.Info("Graceful shutdown completed successfully")
	return nil
}

// Shutdown gracefully shuts down all services and connections
func (c *Container) Shutdown(ctx context.Context) error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.Info("Container shutdown initiated...")

	// Stop janitor service first
	if c.JanitorService != nil {
		c.JanitorService.Shutdown()
		logger.Info("Janitor service stopped")
	}

	// Drain and close NATS connections
	if c.MessagingRepository != nil {
		if err := c.MessagingRepository.DrainWithTimeout(); err != nil {
			logger.Error("Failed to drain NATS connections during shutdown", "error", err.Error())
		} else {
			logger.Info("NATS connections drained successfully")
		}

		if err := c.MessagingRepository.Close(); err != nil {
			logger.Error("Failed to close NATS connections during shutdown", "error", err.Error())
		} else {
			logger.Info("NATS connections closed successfully")
		}
	}

	logger.Info("Container shutdown completed successfully")
	return nil
}

// validateNATSURL validates the NATS URL format and scheme
func validateNATSURL(natsURL string) error {
	if natsURL == "" {
		return fmt.Errorf("NATS URL cannot be empty")
	}

	u, err := url.Parse(natsURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if u.Scheme == "" {
		return fmt.Errorf("URL missing scheme (expected: nats://)")
	}

	// Validate NATS-specific schemes
	validSchemes := []string{"nats", "nats+tls", "ws", "wss"}
	schemeValid := false
	for _, validScheme := range validSchemes {
		if u.Scheme == validScheme {
			schemeValid = true
			break
		}
	}

	if !schemeValid {
		return fmt.Errorf("invalid URL scheme '%s' (expected: nats://, nats+tls://, ws://, or wss://)", u.Scheme)
	}

	if u.Host == "" {
		return fmt.Errorf("URL missing host")
	}

	return nil
}

// validateOpenSearchURL validates the OpenSearch URL format
func validateOpenSearchURL(opensearchURL string) error {
	if opensearchURL == "" {
		return fmt.Errorf("OpenSearch URL cannot be empty")
	}

	u, err := url.Parse(opensearchURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if u.Scheme == "" {
		return fmt.Errorf("URL missing scheme (expected: http:// or https://)")
	}

	// Validate OpenSearch-specific schemes
	validSchemes := []string{"http", "https"}
	schemeValid := false
	for _, validScheme := range validSchemes {
		if u.Scheme == validScheme {
			schemeValid = true
			break
		}
	}

	if !schemeValid {
		return fmt.Errorf("invalid URL scheme '%s' (expected: http:// or https://)", u.Scheme)
	}

	if u.Host == "" {
		return fmt.Errorf("URL missing host")
	}

	return nil
}
