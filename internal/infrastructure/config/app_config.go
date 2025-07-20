// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// AppConfig represents the application configuration
type AppConfig struct {
	Server     ServerConfig     `json:"server"`
	NATS       NATSConfig       `json:"nats"`
	OpenSearch OpenSearchConfig `json:"opensearch"`
	JWT        JWTConfig        `json:"jwt"`
	Logging    LoggingConfig    `json:"logging"`
	Janitor    JanitorConfig    `json:"janitor"`
	Health     HealthConfig     `json:"health"`
}

// ServerConfig contains minimal server configuration for health checks
type ServerConfig struct {
	Port            int           `json:"port"`
	ReadTimeout     time.Duration `json:"read_timeout"`
	WriteTimeout    time.Duration `json:"write_timeout"`
	ShutdownTimeout time.Duration `json:"shutdown_timeout"`
}

// NATSConfig contains NATS configuration
type NATSConfig struct {
	URL               string        `json:"url"`
	MaxReconnects     int           `json:"max_reconnects"`
	ReconnectWait     time.Duration `json:"reconnect_wait"`
	ConnectionTimeout time.Duration `json:"connection_timeout"`
	IndexingSubject   string        `json:"indexing_subject"`
	V1IndexingSubject string        `json:"v1_indexing_subject"`
	Queue             string        `json:"queue"`
	DrainTimeout      time.Duration `json:"drain_timeout"`
}

// OpenSearchConfig contains OpenSearch configuration
type OpenSearchConfig struct {
	URL      string        `json:"url"`
	Username string        `json:"username"`
	Password string        `json:"password"`
	Index    string        `json:"index"`
	Timeout  time.Duration `json:"timeout"`
}

// JWTConfig contains JWT configuration
type JWTConfig struct {
	Issuer    string        `json:"issuer"`
	Audiences []string      `json:"audiences"`  // Multiple audiences
	JWKSURL   string        `json:"jwks_url"`   // Configurable JWKS URL
	ClockSkew time.Duration `json:"clock_skew"` // Configurable clock skew
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// JanitorConfig contains janitor configuration
type JanitorConfig struct {
	Enabled bool `json:"enabled"`
}

// HealthConfig contains health check configuration
type HealthConfig struct {
	CheckTimeout           time.Duration `json:"check_timeout"`
	CacheDuration          time.Duration `json:"cache_duration"`
	EnableDetailedResponse bool          `json:"enable_detailed_response"`
}

// LoadConfig loads configuration from environment variables with comprehensive logging
func LoadConfig() (*AppConfig, error) {
	// Create a basic logger for configuration loading
	logger := slog.Default()
	logger.Info("Configuration loading started")

	// Track which values come from environment vs defaults
	envVarsUsed := make(map[string]bool)
	defaultsUsed := make(map[string]bool)

	config := &AppConfig{
		Server: ServerConfig{
			Port:            getEnvIntWithLogging("PORT", 8080, envVarsUsed, defaultsUsed, logger),
			ReadTimeout:     getEnvDurationWithLogging("READ_TIMEOUT", 5*time.Second, envVarsUsed, defaultsUsed, logger),
			WriteTimeout:    getEnvDurationWithLogging("WRITE_TIMEOUT", 5*time.Second, envVarsUsed, defaultsUsed, logger),
			ShutdownTimeout: getEnvDurationWithLogging("SHUTDOWN_TIMEOUT", 10*time.Second, envVarsUsed, defaultsUsed, logger),
		},
		NATS: NATSConfig{
			URL:               getEnvStringWithLogging("NATS_URL", "nats://nats:4222", envVarsUsed, defaultsUsed, logger),
			MaxReconnects:     getEnvIntWithLogging("NATS_MAX_RECONNECTS", 10, envVarsUsed, defaultsUsed, logger),
			ReconnectWait:     getEnvDurationWithLogging("NATS_RECONNECT_WAIT", 2*time.Second, envVarsUsed, defaultsUsed, logger),
			ConnectionTimeout: getEnvDurationWithLogging("NATS_CONNECTION_TIMEOUT", 10*time.Second, envVarsUsed, defaultsUsed, logger),
			IndexingSubject:   getEnvStringWithLogging("NATS_INDEXING_SUBJECT", "lfx.index.>", envVarsUsed, defaultsUsed, logger),
			V1IndexingSubject: getEnvStringWithLogging("NATS_V1_INDEXING_SUBJECT", "lfx.v1.index.>", envVarsUsed, defaultsUsed, logger),
			Queue:             getEnvStringWithLogging("NATS_QUEUE", "lfx.indexer.queue", envVarsUsed, defaultsUsed, logger),
			DrainTimeout:      getEnvDurationWithLogging("NATS_DRAIN_TIMEOUT", 55*time.Second, envVarsUsed, defaultsUsed, logger),
		},
		OpenSearch: OpenSearchConfig{
			URL:   getEnvStringWithLogging("OPENSEARCH_URL", "http://localhost:9200", envVarsUsed, defaultsUsed, logger),
			Index: getEnvStringWithLogging("OPENSEARCH_INDEX", "resources", envVarsUsed, defaultsUsed, logger),
			/*Username: getEnvString("OPENSEARCH_USERNAME", "admin"),
			Password: getEnvString("OPENSEARCH_PASSWORD", "admin"),
			Timeout:  getEnvDuration("OPENSEARCH_TIMEOUT", 30*time.Second),*/
		},
		JWT: JWTConfig{
			Issuer: getEnvStringWithLogging("JWT_ISSUER", "heimdall", envVarsUsed, defaultsUsed, logger),
			Audiences: func() []string {
				envValue := getEnvStringWithLogging("JWT_AUDIENCES", "", envVarsUsed, defaultsUsed, logger)
				if envValue != "" {
					audiences := strings.Split(envValue, ",")
					logger.Info("JWT audiences parsed from environment",
						"raw_value", envValue,
						"parsed_count", len(audiences),
						"audiences", audiences)
					return audiences
				}
				// Default to projects-api
				defaultAudiences := []string{"projects-api"}
				defaultsUsed["JWT_AUDIENCES"] = true
				logger.Info("Using default JWT audiences",
					"audiences", defaultAudiences)
				return defaultAudiences
			}(),
			JWKSURL:   getEnvStringWithLogging("JWKS_URL", "http://heimdall:4457/.well-known/jwks", envVarsUsed, defaultsUsed, logger),
			ClockSkew: getEnvDurationWithLogging("JWT_CLOCK_SKEW", 6*time.Hour, envVarsUsed, defaultsUsed, logger),
		},
		Logging: LoggingConfig{
			Level:  getEnvStringWithLogging("LOG_LEVEL", "info", envVarsUsed, defaultsUsed, logger),
			Format: getEnvStringWithLogging("LOG_FORMAT", "json", envVarsUsed, defaultsUsed, logger),
		},
		Janitor: JanitorConfig{
			Enabled: getEnvBoolWithLogging("JANITOR_ENABLED", true, envVarsUsed, defaultsUsed, logger),
		},
		Health: HealthConfig{
			CheckTimeout:           getEnvDurationWithLogging("HEALTH_CHECK_TIMEOUT", 5*time.Second, envVarsUsed, defaultsUsed, logger),
			CacheDuration:          getEnvDurationWithLogging("HEALTH_CACHE_DURATION", 5*time.Second, envVarsUsed, defaultsUsed, logger),
			EnableDetailedResponse: getEnvBoolWithLogging("HEALTH_DETAILED_RESPONSE", true, envVarsUsed, defaultsUsed, logger),
		},
	}

	// Log configuration summary
	logger.Info("Configuration loading completed",
		"env_vars_used", len(envVarsUsed),
		"defaults_used", len(defaultsUsed),
		"total_config_items", len(envVarsUsed)+len(defaultsUsed))

	// Log environment vs defaults breakdown
	if len(envVarsUsed) > 0 {
		envKeys := make([]string, 0, len(envVarsUsed))
		for key := range envVarsUsed {
			envKeys = append(envKeys, key)
		}
		logger.Debug("Environment variables used", "keys", envKeys)
	}

	if len(defaultsUsed) > 0 {
		defaultKeys := make([]string, 0, len(defaultsUsed))
		for key := range defaultsUsed {
			defaultKeys = append(defaultKeys, key)
		}
		logger.Debug("Default values used", "keys", defaultKeys)
	}

	return config, nil
}

// Validate validates the configuration with detailed logging
func (c *AppConfig) Validate() error {
	logger := slog.Default()
	logger.Info("Configuration validation started")

	// Validate each configuration domain separately for better separation of concerns
	validationSteps := []struct {
		name string
		fn   func() error
	}{
		{"server", c.validateServer},
		{"nats", c.validateNATS},
		{"opensearch", c.validateOpenSearch},
		{"jwt", c.validateJWT},
		{"logging", c.validateLogging},
		{"janitor", c.validateJanitor},
		{"health", c.validateHealth},
	}

	for _, step := range validationSteps {
		logger.Debug("Validating configuration section", "section", step.name)
		if err := step.fn(); err != nil {
			logger.Error("Configuration validation failed",
				"section", step.name,
				"error", err.Error())
			return fmt.Errorf("%s configuration: %w", step.name, err)
		}
		logger.Debug("Configuration section validation passed", "section", step.name)
	}

	logger.Info("Configuration validation completed successfully")
	return nil
}

// validateServer validates server configuration
func (c *AppConfig) validateServer() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d, must be between 1 and 65535", c.Server.Port)
	}

	if c.Server.ReadTimeout <= 0 {
		return fmt.Errorf("read timeout must be positive, got: %v", c.Server.ReadTimeout)
	}

	if c.Server.WriteTimeout <= 0 {
		return fmt.Errorf("write timeout must be positive, got: %v", c.Server.WriteTimeout)
	}

	if c.Server.ShutdownTimeout <= 0 {
		return fmt.Errorf("shutdown timeout must be positive, got: %v", c.Server.ShutdownTimeout)
	}

	return nil
}

// validateNATS validates NATS configuration
func (c *AppConfig) validateNATS() error {
	if c.NATS.URL == "" {
		return fmt.Errorf("NATS URL is required")
	}

	if c.NATS.Queue == "" {
		return fmt.Errorf("NATS queue is required")
	}

	if c.NATS.IndexingSubject == "" {
		return fmt.Errorf("NATS indexing subject is required")
	}

	if c.NATS.V1IndexingSubject == "" {
		return fmt.Errorf("NATS V1 indexing subject is required")
	}

	if c.NATS.MaxReconnects < 0 {
		return fmt.Errorf("NATS max reconnects cannot be negative, got: %d", c.NATS.MaxReconnects)
	}

	if c.NATS.ReconnectWait <= 0 {
		return fmt.Errorf("NATS reconnect wait must be positive, got: %v", c.NATS.ReconnectWait)
	}

	if c.NATS.ConnectionTimeout <= 0 {
		return fmt.Errorf("NATS connection timeout must be positive, got: %v", c.NATS.ConnectionTimeout)
	}

	return nil
}

// validateOpenSearch validates OpenSearch configuration
func (c *AppConfig) validateOpenSearch() error {
	if c.OpenSearch.URL == "" {
		return fmt.Errorf("OpenSearch URL is required")
	}

	if c.OpenSearch.Index == "" {
		return fmt.Errorf("OpenSearch index is required")
	}

	return nil
}

// validateJWT validates JWT configuration
func (c *AppConfig) validateJWT() error {
	if strings.TrimSpace(c.JWT.Issuer) == "" {
		return fmt.Errorf("JWT issuer is required")
	}

	// Validate that we have at least one audience
	if len(c.JWT.Audiences) == 0 {
		return fmt.Errorf("JWT audiences array is required")
	}

	// Validate JWKS URL if provided
	if strings.TrimSpace(c.JWT.JWKSURL) == "" {
		return fmt.Errorf("JWKS URL is required")
	}

	// Validate clock skew is reasonable (not negative)
	if c.JWT.ClockSkew < 0 {
		return fmt.Errorf("JWT clock skew cannot be negative, got: %v", c.JWT.ClockSkew)
	}

	return nil
}

// validateLogging validates logging configuration (PREVIOUSLY MISSING!)
func (c *AppConfig) validateLogging() error {
	validLevels := []string{"debug", "info", "warn", "error", "fatal", "panic"}
	if !contains(validLevels, c.Logging.Level) {
		return fmt.Errorf("invalid log level: %s, must be one of: %v", c.Logging.Level, validLevels)
	}

	validFormats := []string{"json", "text", "console"}
	if !contains(validFormats, c.Logging.Format) {
		return fmt.Errorf("invalid log format: %s, must be one of: %v", c.Logging.Format, validFormats)
	}

	return nil
}

// validateJanitor validates janitor configuration
func (c *AppConfig) validateJanitor() error {
	// No validation needed for boolean Enabled field
	return nil
}

// Enhanced environment variable helper functions with logging

func getEnvStringWithLogging(key, defaultValue string, envVarsUsed, defaultsUsed map[string]bool, logger *slog.Logger) string {
	if value := os.Getenv(key); value != "" {
		envVarsUsed[key] = true
		logger.Debug("Environment variable loaded",
			"key", key,
			"value_length", len(value),
			"has_value", true)
		return value
	}
	defaultsUsed[key] = true
	logger.Debug("Using default value",
		"key", key,
		"default_value", defaultValue)
	return defaultValue
}

func getEnvIntWithLogging(key string, defaultValue int, envVarsUsed, defaultsUsed map[string]bool, logger *slog.Logger) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			envVarsUsed[key] = true
			logger.Debug("Environment variable loaded as integer",
				"key", key,
				"value", intValue)
			return intValue
		} else {
			logger.Warn("Invalid integer in environment variable, using default",
				"key", key,
				"invalid_value", value,
				"error", err.Error(),
				"default_value", defaultValue)
		}
	}
	defaultsUsed[key] = true
	logger.Debug("Using default integer value",
		"key", key,
		"default_value", defaultValue)
	return defaultValue
}

func getEnvBoolWithLogging(key string, defaultValue bool, envVarsUsed, defaultsUsed map[string]bool, logger *slog.Logger) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			envVarsUsed[key] = true
			logger.Debug("Environment variable loaded as boolean",
				"key", key,
				"value", boolValue)
			return boolValue
		} else {
			logger.Warn("Invalid boolean in environment variable, using default",
				"key", key,
				"invalid_value", value,
				"error", err.Error(),
				"default_value", defaultValue)
		}
	}
	defaultsUsed[key] = true
	logger.Debug("Using default boolean value",
		"key", key,
		"default_value", defaultValue)
	return defaultValue
}

func getEnvDurationWithLogging(key string, defaultValue time.Duration, envVarsUsed, defaultsUsed map[string]bool, logger *slog.Logger) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			envVarsUsed[key] = true
			logger.Debug("Environment variable loaded as duration",
				"key", key,
				"value", duration,
				"raw_value", value)
			return duration
		} else {
			logger.Warn("Invalid duration in environment variable, using default",
				"key", key,
				"invalid_value", value,
				"error", err.Error(),
				"default_value", defaultValue)
		}
	}
	defaultsUsed[key] = true
	logger.Debug("Using default duration value",
		"key", key,
		"default_value", defaultValue)
	return defaultValue
}

// validateHealth validates health configuration
func (c *AppConfig) validateHealth() error {
	if c.Health.CheckTimeout <= 0 {
		return fmt.Errorf("health check timeout must be positive, got: %v", c.Health.CheckTimeout)
	}

	if c.Health.CacheDuration < 0 {
		return fmt.Errorf("health cache duration must be non-negative, got: %v", c.Health.CacheDuration)
	}

	// Cache duration should typically be shorter than check timeout to be effective
	if c.Health.CacheDuration > c.Health.CheckTimeout {
		return fmt.Errorf("health cache duration (%v) should not exceed check timeout (%v)",
			c.Health.CacheDuration, c.Health.CheckTimeout)
	}

	return nil
}

// Helper function for slice contains check
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
