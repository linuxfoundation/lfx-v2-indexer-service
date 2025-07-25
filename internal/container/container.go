// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package container provides dependency injection and service container functionality.
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
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/contracts"
	"github.com/linuxfoundation/lfx-indexer-service/internal/domain/services"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/auth"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/cleanup"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/config"
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

	// Repositories
	StorageRepository   contracts.StorageRepository
	MessagingRepository contracts.MessagingRepository
	AuthRepository      contracts.AuthRepository
	CleanupRepository   contracts.CleanupRepository

	// Services (consolidated)
	IndexerService *services.IndexerService

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
			logger.Info("Cleanup disabled by CLI flag")
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

	// Initialize NATS connection
	if err := validateNATSURL(c.Config.NATS.URL); err != nil {
		logger.Error("Invalid NATS URL", "url", c.Config.NATS.URL, "error", err.Error())
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create error handler for NATS connection issues
	errorHandler := func(conn *natsgo.Conn, sub *natsgo.Subscription, err error) {
		if sub != nil {
			logger.Error("NATS subscription error",
				"error", err.Error(),
				"subject", sub.Subject,
				"queue", sub.Queue)
		} else {
			logger.Error("NATS connection error",
				"error", err.Error(),
				"status", conn.Status())
		}
	}

	// Create closed handler for connection recovery
	closedHandler := func(conn *natsgo.Conn) {
		status := conn.Status()
		if status == natsgo.CLOSED {
			logger.Error("NATS connection permanently closed - max reconnects exhausted")
		}
	}

	// Create reconnect handler
	reconnectedHandler := func(conn *natsgo.Conn) {
		logger.Info("NATS reconnected successfully", "url", conn.ConnectedUrl())
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
		logger.Error("Failed to connect to NATS", "url", c.Config.NATS.URL, "error", err.Error())
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	c.NATSConnection = natsConn
	logger.Info("NATS connection established", "url", c.Config.NATS.URL)

	// Initialize OpenSearch client
	if err := validateOpenSearchURL(c.Config.OpenSearch.URL); err != nil {
		logger.Error("Invalid OpenSearch URL", "url", c.Config.OpenSearch.URL, "error", err.Error())
		return fmt.Errorf("failed to create OpenSearch client: %w", err)
	}

	opensearchConfig := opensearchgo.Config{
		Addresses: []string{c.Config.OpenSearch.URL},
	}
	opensearchClient, err := opensearchgo.NewClient(opensearchConfig)
	if err != nil {
		logger.Error("Failed to create OpenSearch client", "url", c.Config.OpenSearch.URL, "error", err.Error())
		return fmt.Errorf("failed to create OpenSearch client: %w", err)
	}
	c.OpenSearchClient = opensearchClient
	logger.Info("OpenSearch client created", "url", c.Config.OpenSearch.URL)

	return nil
}

// initializeRepositories initializes repository layer with consolidated interfaces
func (c *Container) initializeRepositories() error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)

	// Initialize auth repository first (needed by messaging repository)
	authRepo, err := auth.NewAuthRepository(
		c.Config.JWT.Issuer,
		c.Config.JWT.Audiences,
		c.Config.JWT.JWKSURL,
		c.Config.JWT.ClockSkew,
		logger,
	)
	if err != nil {
		logger.Error("Failed to create auth repository", "error", err.Error())
		return fmt.Errorf("failed to create auth repository: %w", err)
	}
	c.AuthRepository = authRepo

	// Initialize storage repository
	c.StorageRepository = storage.NewStorageRepository(c.OpenSearchClient, c.Logger)

	// Initialize messaging repository
	c.MessagingRepository = messaging.NewMessagingRepository(
		c.NATSConnection,
		c.AuthRepository,
		c.Logger,
		c.Config.NATS.DrainTimeout,
	)

	// Initialize cleanup repository (background operations)
	c.CleanupRepository = cleanup.NewCleanupRepository(
		c.StorageRepository,
		c.Logger,
		c.Config.OpenSearch.Index,
	)

	return nil
}

// initializeServices initializes service layer
func (c *Container) initializeServices() error {
	// Initialize indexer service
	c.IndexerService = services.NewIndexerService(
		c.StorageRepository,
		c.MessagingRepository,
		c.Logger,
	)
	return nil
}

// initializeApplication initializes application layer
func (c *Container) initializeApplication() error {
	// Initialize message processor
	c.MessageProcessor = application.NewMessageProcessor(
		c.IndexerService,
		c.MessagingRepository,
		c.CleanupRepository,
		c.Logger,
	)

	return nil
}

// initializeHandlers initializes presentation layer
func (c *Container) initializeHandlers() error {
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

	// Initialize unified message handler (handles both V2 and V1)
	c.IndexingMessageHandler = handlers.NewIndexingMessageHandler(c.MessageProcessor)

	return nil
}

// StartServices starts all background services
func (c *Container) StartServices(ctx context.Context) error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)

	// Check for required components
	if c.Config == nil {
		return fmt.Errorf("container config is not initialized")
	}

	// Setup NATS subscriptions
	if err := c.SetupNATSSubscriptions(ctx); err != nil {
		return fmt.Errorf("failed to setup NATS subscriptions: %w", err)
	}

	// Start production cleanup service
	if c.Config.Janitor.Enabled && c.CleanupRepository != nil {
		c.CleanupRepository.StartItemLoop(ctx)
		logger.Info("Cleanup started")
	}

	logger.Info("NATS message processing services started")
	return nil
}

// StartServicesWithWaitGroup starts all background services with WaitGroup coordination
func (c *Container) StartServicesWithWaitGroup(ctx context.Context, wg *sync.WaitGroup) error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)

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
		<-ctx.Done() // Wait for shutdown signal

		// Drain NATS connections
		if c.MessagingRepository != nil {
			if err := c.MessagingRepository.DrainWithTimeout(); err != nil {
				logger.Error("Failed to drain NATS connections", "error", err.Error())
			}
		}
	}()

	// Start production cleanup service with WaitGroup coordination
	if c.Config.Janitor.Enabled && c.CleanupRepository != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ctx.Done() // Wait for shutdown signal
			c.CleanupRepository.Shutdown()
		}()

		c.CleanupRepository.StartItemLoop(ctx)
		logger.Info("Cleanup started")
	}

	logger.Info("NATS message processing services started")
	return nil
}

// SetupNATSSubscriptions sets up all NATS message subscriptions
func (c *Container) SetupNATSSubscriptions(ctx context.Context) error {
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

	// Subscribe to indexing messages with queue group for load balancing
	indexingSubject := c.Config.NATS.IndexingSubject
	if err := c.MessagingRepository.QueueSubscribe(ctx, indexingSubject, c.Config.NATS.Queue, c.IndexingMessageHandler); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", indexingSubject, err)
	}

	// Subscribe to v1 indexing messages with the same unified handler
	v1IndexingSubject := c.Config.NATS.V1IndexingSubject
	if err := c.MessagingRepository.QueueSubscribe(ctx, v1IndexingSubject, c.Config.NATS.Queue, c.IndexingMessageHandler); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", v1IndexingSubject, err)
	}

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

	var errors []error

	// First, drain NATS connections gracefully
	if c.MessagingRepository != nil {
		if err := c.MessagingRepository.DrainWithTimeout(); err != nil {
			logger.Error("Failed to drain NATS connections", "error", err.Error())
			errors = append(errors, fmt.Errorf("failed to drain NATS: %w", err))
		}
	}

	// Stop cleanup service
	if c.CleanupRepository != nil {
		c.CleanupRepository.Shutdown()
	}

	// Finally, close all remaining resources
	if err := c.Shutdown(context.Background()); err != nil {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return fmt.Errorf("graceful shutdown completed with errors: %v", errors)
	}

	return nil
}

// Shutdown gracefully shuts down all services and connections
func (c *Container) Shutdown(ctx context.Context) error {
	logger := logging.WithComponent(c.Logger, constants.ComponentContainer)
	logger.InfoContext(ctx, "Initiating container shutdown sequence")

	// Stop cleanup service first
	if c.CleanupRepository != nil {
		logger.DebugContext(ctx, "Shutting down cleanup repository")
		c.CleanupRepository.Shutdown()
	}

	// Drain and close NATS connections
	if c.MessagingRepository != nil {
		if err := c.MessagingRepository.DrainWithTimeout(); err != nil {
			logger.Error("Failed to drain NATS connections during shutdown", "error", err.Error())
		}

		if err := c.MessagingRepository.Close(); err != nil {
			logger.Error("Failed to close NATS connections during shutdown", "error", err.Error())
		}
	}

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
