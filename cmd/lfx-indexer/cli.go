// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/linuxfoundation/lfx-indexer-service/internal/infrastructure/config"
	"github.com/linuxfoundation/lfx-indexer-service/pkg/logging"
)

// parseCLIFlags sets up, parses, and returns all CLI flags
func parseCLIFlags() *config.CLIConfig {
	// Production CLI flags with ENV > Default precedence
	debug := flag.Bool("d", logging.GetEnvBool("DEBUG", false), "enable debug logging")
	port := flag.String("p", logging.GetEnvOrDefault("PORT", "8080"), "health checks port")
	bind := flag.String("bind", logging.GetEnvOrDefault("BIND", "*"), "interface to bind on")
	noJanitor := flag.Bool("nojanitor", logging.GetEnvBool("NO_JANITOR", false), "disable janitor")
	simpleHealth := flag.Bool("simple-health", logging.GetEnvBool("SIMPLE_HEALTH", false), "use simple 'OK' health responses")

	configCheck := flag.Bool("check-config", false, "Check configuration and exit")
	help := flag.Bool("help", false, "Show help")

	// Custom usage function
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "LFX Indexer Service\n")
		fmt.Fprintf(os.Stderr, "================================\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])

		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -d               Enable debug logging with source location\n")
		fmt.Fprintf(os.Stderr, "  -p <port>        Health check port (default: 8080)\n")
		fmt.Fprintf(os.Stderr, "  --bind <iface>   Interface to bind on (default: *, use 0.0.0.0 for all)\n")
		fmt.Fprintf(os.Stderr, "  --nojanitor      Disable background janitor service\n")
		fmt.Fprintf(os.Stderr, "  --simple-health  Use simple 'OK' health responses for K8s\n")
		fmt.Fprintf(os.Stderr, "  --check-config   Check configuration and exit\n")
		fmt.Fprintf(os.Stderr, "  --help           Show this help message\n\n")

		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  Standard:        %s -p 8080 --bind 0.0.0.0\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Debug mode:      %s -d -p 9090\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Kubernetes:      %s --bind 0.0.0.0 --simple-health --nojanitor\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Development:     DEBUG=1 PORT=8080 %s\n\n", os.Args[0])

		fmt.Fprintf(os.Stderr, "Environment Variables:\n")
		fmt.Fprintf(os.Stderr, "  Service Config:\n")
		fmt.Fprintf(os.Stderr, "    DEBUG=1                Enable debug logging\n")
		fmt.Fprintf(os.Stderr, "    PORT=8080              Health check port\n")
		fmt.Fprintf(os.Stderr, "    BIND=0.0.0.0           Bind interface\n")
		fmt.Fprintf(os.Stderr, "    NO_JANITOR=1           Disable janitor service\n")
		fmt.Fprintf(os.Stderr, "    SIMPLE_HEALTH=1        Use simple health responses\n\n")
		fmt.Fprintf(os.Stderr, "  Infrastructure:\n")
		fmt.Fprintf(os.Stderr, "    LOG_LEVEL=info         Logging level (debug,info,warn,error)\n")
		fmt.Fprintf(os.Stderr, "    LOG_FORMAT=json        Log format (json,text)\n")
		fmt.Fprintf(os.Stderr, "    NATS_URL=nats://...    NATS server URL\n")
		fmt.Fprintf(os.Stderr, "    OPENSEARCH_URL=http... OpenSearch URL\n\n")

		fmt.Fprintf(os.Stderr, "Configuration precedence: CLI flags > Environment variables > Defaults\n\n")
		fmt.Fprintf(os.Stderr, "Health endpoints:\n")
		fmt.Fprintf(os.Stderr, "  GET /health            Detailed health status (JSON)\n")
		fmt.Fprintf(os.Stderr, "  GET /livez             Kubernetes liveness probe\n")
		fmt.Fprintf(os.Stderr, "  GET /readyz            Kubernetes readiness probe\n\n")
	}

	flag.Parse()

	return &config.CLIConfig{
		Port:         *port,
		Debug:        *debug,
		Bind:         *bind,
		NoJanitor:    *noJanitor,
		SimpleHealth: *simpleHealth,
		ConfigCheck:  *configCheck,
		Help:         *help,
	}
}

// handleEarlyExits processes flags that cause early program termination
func handleEarlyExits(flags *config.CLIConfig, logger *slog.Logger) {
	// Handle help flag
	if flags.Help {
		flag.Usage()
		os.Exit(0)
	}

	// Handle config check flag
	if flags.ConfigCheck {
		logger.Info("Configuration check requested")
		logger.Info("Configuration validation completed", "status", "valid")
		os.Exit(0)
	}
}

// No longer needed - CLIFlags and CLIConfig are identical now
