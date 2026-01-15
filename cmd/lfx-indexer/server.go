// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package main provides the entry point for the LFX indexer service application.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/linuxfoundation/lfx-v2-indexer-service/internal/container"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// createHTTPServer creates and configures the HTTP server with health check routes
func createHTTPServer(container *container.Container, bind string) *http.Server {
	// Setup minimal HTTP server for Kubernetes health checks only
	mux := http.NewServeMux()
	container.HealthHandler.RegisterRoutes(mux)

	// Wrap the handler with OpenTelemetry instrumentation
	var handler http.Handler = mux
	handler = otelhttp.NewHandler(handler, "indexer-service")

	// Create HTTP server with CLI overrides
	var addr string
	if bind == "*" {
		addr = fmt.Sprintf(":%d", container.Config.Server.Port)
	} else {
		addr = fmt.Sprintf("%s:%d", bind, container.Config.Server.Port)
	}

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       container.Config.Server.ReadTimeout,
		WriteTimeout:      container.Config.Server.WriteTimeout,
		ReadHeaderTimeout: 3 * time.Second, // Security: prevent slowloris attacks
	}
}

// startHTTPServer starts the HTTP server in a goroutine with logging
func startHTTPServer(server *http.Server, container *container.Container, bind string, logger *slog.Logger) {
	go func() {
		logger.Info("Starting health check HTTP server",
			"port", container.Config.Server.Port,
			"bind", bind,
			"addr", server.Addr)
		logger.Info("Health endpoints available",
			"health", fmt.Sprintf("http://%s:%d/health", bind, container.Config.Server.Port),
			"livez", fmt.Sprintf("http://%s:%d/livez", bind, container.Config.Server.Port),
			"readyz", fmt.Sprintf("http://%s:%d/readyz", bind, container.Config.Server.Port))

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Health check server error", "error", err.Error())
			os.Exit(1)
		}
	}()
}
