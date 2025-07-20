// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/linuxfoundation/lfx-indexer-service/internal/container"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
)

func main() {
	// Parse CLI flags
	flags := parseCLIFlags()

	// Initialize logger using existing infrastructure
	logger := logging.NewLogger(flags.Debug)

	// Handle early exits (help, config-check)
	handleEarlyExits(flags, logger)

	// Log configuration with sources for transparency
	logger.Info("Configuration loaded",
		"port", flags.Port,
		"debug", flags.Debug,
		"bind", flags.Bind,
		"no_janitor", flags.NoJanitor,
		"simple_health", flags.SimpleHealth)

	startupTime := time.Now()
	logger.Info("LFX Indexer Service startup initiated")

	// Initialize dependency injection container
	logger.Info("Initializing dependency injection container...")
	containerStartTime := time.Now()

	// CLI config is already the right type - no conversion needed
	container, err := container.NewContainer(logger, flags)
	if err != nil {
		logger.Error("Failed to initialize container",
			"duration", time.Since(containerStartTime),
			"error", err.Error())
		os.Exit(1)
	}

	logger.Info("Container initialized successfully", "duration", time.Since(containerStartTime))

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create WaitGroup for coordinated shutdown
	gracefulCloseWG := &sync.WaitGroup{}

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

	// Start background services with WaitGroup coordination
	logger.Info("Starting background services...")
	servicesStartTime := time.Now()

	if err := container.StartServicesWithWaitGroup(ctx, gracefulCloseWG); err != nil {
		logger.Error("Failed to start background services",
			"duration", time.Since(servicesStartTime),
			"error", err.Error())
		os.Exit(1)
	}

	logger.Info("Background services started successfully", "duration", time.Since(servicesStartTime))

	// Setup and start HTTP server
	logger.Info("Setting up health check HTTP server...")
	server := createHTTPServer(container, flags.Bind)
	startHTTPServer(server, container, flags.Bind, logger)

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

	// Cancel context to signal shutdown
	logger.Info("Signaling background services to stop...")
	cancel()

	// Wait for background services to complete gracefully with timeout
	logger.Info("Waiting for background services to complete...")
	waitDone := make(chan struct{})
	go func() {
		gracefulCloseWG.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		logger.Info("All background services completed gracefully")
	case <-time.After(30 * time.Second):
		logger.Warn("Background services shutdown timeout reached")
	}

	// Now shutdown HTTP server after all other services are stopped
	logger.Info("Shutting down health check HTTP server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
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
