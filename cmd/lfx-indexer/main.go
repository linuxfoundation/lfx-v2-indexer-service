package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/container"
	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/logging"
)

func main() {
	// Initialize structured logger immediately (JSON by default)
	logger := logging.NewLogger()
	slog.SetDefault(logger) // Set as default for any slog.Default() calls

	startupTime := time.Now()
	logger.Info("LFX Indexer Service startup initiated", "version", "v2.0.0")

	// Parse command line flags
	var (
		configCheck = flag.Bool("check-config", false, "Check configuration and exit")
		version     = flag.Bool("version", false, "Print version and exit")
		help        = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	logger.Info("Command line flags parsed",
		"config_check", *configCheck,
		"version", *version,
		"help", *help)

	// Handle help flag
	if *help {
		fmt.Println("LFX Indexer Service")
		fmt.Println("Usage:")
		flag.PrintDefaults()
		os.Exit(0)
	}

	// Handle version flag
	if *version {
		fmt.Println("LFX Indexer Service v2.0.0")
		os.Exit(0)
	}

	// Initialize dependency injection container
	logger.Info("Initializing dependency injection container...")
	containerStartTime := time.Now()

	container, err := container.NewContainer(logger)
	if err != nil {
		logger.Error("Failed to initialize container",
			"duration", time.Since(containerStartTime),
			"error", err.Error())
		os.Exit(1)
	}
	defer func() {
		logger.Info("Initiating graceful shutdown...")
		if err := container.GracefulShutdown(); err != nil {
			logger.Error("Graceful shutdown completed with errors", "error", err.Error())
		} else {
			logger.Info("Graceful shutdown completed successfully")
		}
	}()

	logger.Info("Container initialized successfully", "duration", time.Since(containerStartTime))

	// Handle config check flag
	if *configCheck {
		logger.Info("Configuration check requested")
		fmt.Println("Configuration is valid")
		logger.Info("Configuration check completed successfully")
		os.Exit(0)
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Perform health check
	logger.Info("Performing initial health check...")
	healthCheckStart := time.Now()

	if err := container.HealthCheck(ctx); err != nil {
		logger.Warn("Initial health check failed",
			"duration", time.Since(healthCheckStart),
			"error", err.Error())
		logger.Warn("Service will start in degraded mode - some dependencies may be unavailable")
		logger.Warn("Health status will be continuously monitored via health endpoints")
	} else {
		logger.Info("Initial health check passed",
			"duration", time.Since(healthCheckStart))
	}

	// Start background services
	logger.Info("Starting background services...")
	servicesStartTime := time.Now()

	if err := container.StartServices(ctx); err != nil {
		logger.Error("Failed to start background services",
			"duration", time.Since(servicesStartTime),
			"error", err.Error())
		os.Exit(1)
	}

	logger.Info("Background services started successfully", "duration", time.Since(servicesStartTime))

	// Setup minimal HTTP server for Kubernetes health checks only
	logger.Info("Setting up health check HTTP server...")
	mux := http.NewServeMux()
	container.HealthHandler.RegisterRoutes(mux)

	// Create HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", container.Config.Server.Port),
		Handler:      mux,
		ReadTimeout:  container.Config.Server.ReadTimeout,
		WriteTimeout: container.Config.Server.WriteTimeout,
	}

	// Start HTTP server in a goroutine
	go func() {
		logger.Info("Starting health check HTTP server", "port", container.Config.Server.Port)
		logger.Info("Health endpoints available",
			"health", fmt.Sprintf("http://localhost:%d/health", container.Config.Server.Port),
			"livez", fmt.Sprintf("http://localhost:%d/livez", container.Config.Server.Port),
			"readyz", fmt.Sprintf("http://localhost:%d/readyz", container.Config.Server.Port))

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Health check server error", "error", err.Error())
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	totalStartupTime := time.Since(startupTime)
	logger.Info("LFX Indexer Service started successfully", "startup_duration", totalStartupTime)
	logger.Info("Service Status",
		"nats_processing", "ACTIVE",
		"opensearch_indexing", "READY",
		"health_monitoring", "ENABLED",
		"queue_group", container.Config.NATS.Queue,
		"index_target", container.Config.OpenSearch.Index)
	logger.Info("Service is ready to process messages...")
	logger.Info("Press Ctrl+C to initiate graceful shutdown")

	// Wait for shutdown signal
	receivedSignal := <-sigChan
	shutdownStartTime := time.Now()

	logger.Info("Shutdown signal received", "signal", receivedSignal)
	logger.Info("Initiating graceful shutdown sequence...")

	// Cancel context to stop background services
	logger.Info("Stopping background services...")
	cancel()

	// Shutdown HTTP server gracefully
	logger.Info("Shutting down health check HTTP server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), container.Config.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Health check server shutdown error", "error", err.Error())
	} else {
		logger.Info("Health check server shutdown completed")
	}

	shutdownDuration := time.Since(shutdownStartTime)
	totalUptime := time.Since(startupTime)

	logger.Info("Graceful shutdown completed", "shutdown_duration", shutdownDuration)
	logger.Info("Service uptime", "uptime", totalUptime)
	logger.Info("LFX Indexer Service stopped")
}
