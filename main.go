package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/linuxfoundation/lfx-indexer-service/internal/container"
)

func main() {
	// Parse command line flags
	var (
		configCheck = flag.Bool("check-config", false, "Check configuration and exit")
		version     = flag.Bool("version", false, "Print version and exit")
		help        = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

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
	container, err := container.NewContainer()
	if err != nil {
		log.Fatalf("Failed to initialize container: %v", err)
	}
	defer container.Close()

	// Handle config check flag
	if *configCheck {
		fmt.Println("Configuration is valid")
		os.Exit(0)
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Perform health check
	if err := container.HealthCheck(ctx); err != nil {
		log.Printf("Health check failed: %v", err)
		log.Println("Continuing startup anyway...")
	}

	// Start background services
	if err := container.StartServices(ctx); err != nil {
		log.Fatalf("Failed to start services: %v", err)
	}

	// Setup minimal HTTP server for Kubernetes health checks only
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
		log.Printf("Starting health check server on port %d", container.Config.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Health check server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("LFX Indexer Service started successfully")
	log.Println("NATS message processing active, health checks available")
	log.Println("Press Ctrl+C to shutdown...")

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutdown signal received, initiating graceful shutdown...")

	// Cancel context to stop background services
	cancel()

	// Shutdown HTTP server gracefully
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), container.Config.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Health check server shutdown error: %v", err)
	}

	log.Println("Service stopped gracefully")
}
