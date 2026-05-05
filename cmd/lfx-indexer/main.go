// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package main provides the entry point for the LFX indexer service application.
package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/container"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/logging"
	"github.com/linuxfoundation/lfx-v2-indexer-service/pkg/utils"
)

// Build-time variables set via ldflags
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// gracefulShutdownSeconds is the maximum time to wait for OpenTelemetry SDK to flush
const gracefulShutdownSeconds = 30

func main() {
	// Parse CLI flags
	flags := parseCLIFlags()

	// Initialize logger using existing infrastructure
	logger := logging.NewLogger(flags.Debug)

	// Handle early exits (help, config-check)
	handleEarlyExits(flags, logger)

	// Set up OpenTelemetry SDK.
	// Command-line/environment OTEL_SERVICE_VERSION takes precedence over
	// the build-time Version variable.
	otelConfig := utils.OTelConfigFromEnv()
	if otelConfig.ServiceVersion == "" {
		otelConfig.ServiceVersion = Version
	}
	otelShutdown, err := utils.SetupOTelSDKWithConfig(context.Background(), otelConfig)
	if err != nil {
		logger.Error("error setting up OpenTelemetry SDK", "error", err)
		os.Exit(1)
	}
	// Handle shutdown properly so nothing leaks.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), gracefulShutdownSeconds*time.Second)
		defer cancel()
		if shutdownErr := otelShutdown(ctx); shutdownErr != nil {
			logger.Error("error shutting down OpenTelemetry SDK", "error", shutdownErr)
		}
	}()

	// Log configuration with sources for transparency
	logger.Info("Configuration loaded",
		"port", flags.Port,
		"debug", flags.Debug,
		"bind", flags.Bind,
		"no_janitor", flags.NoJanitor,
		"simple_health", flags.SimpleHealth)

	logger.Info("LFX Indexer Service startup initiated")

	// Initialize dependency injection container
	logger.Info("Initializing dependency injection container...")

	// CLI config is already the right type - no conversion needed
	container, err := container.NewContainer(logger, flags)
	if err != nil {
		logger.Error("Failed to initialize container", "error", err.Error())
		os.Exit(1)
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create WaitGroup for coordinated shutdown
	gracefulCloseWG := &sync.WaitGroup{}

	// Perform health check
	logger.Info("Performing initial health check...")

	if err := container.HealthCheck(ctx); err != nil {
		logger.Warn("Initial health check failed", "error", err.Error())
		logger.Warn("Service will start in degraded mode - some dependencies may be unavailable")
		logger.Warn("Health status will be continuously monitored via health endpoints")
	} else {
		logger.Info("Initial health check passed")
	}

	// Start background services with WaitGroup coordination
	logger.Info("Starting background services...")

	if err := container.StartServicesWithWaitGroup(ctx, gracefulCloseWG); err != nil {
		logger.Error("Failed to start background services", "error", err.Error())
		return // Let deferred cancel() run naturally
	}

	logger.Info("Background services started successfully")

	// Setup and start HTTP server
	logger.Info("Setting up health check HTTP server...")
	server := createHTTPServer(container, flags.Bind)
	startHTTPServer(server, container, flags.Bind, logger)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("LFX Indexer Service started successfully")
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

	logger.Info("Graceful shutdown completed")
	logger.Info("LFX Indexer Service stopped")
}
