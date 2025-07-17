package config

import (
	"fmt"
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

// LoadConfig loads configuration from environment variables
func LoadConfig() (*AppConfig, error) {
	config := &AppConfig{
		Server: ServerConfig{
			Port:            getEnvInt("PORT", 8080),
			ReadTimeout:     getEnvDuration("READ_TIMEOUT", 5*time.Second),
			WriteTimeout:    getEnvDuration("WRITE_TIMEOUT", 5*time.Second),
			ShutdownTimeout: getEnvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		},
		NATS: NATSConfig{
			URL:               getEnvString("NATS_URL", "nats://nats:4222"),
			MaxReconnects:     getEnvInt("NATS_MAX_RECONNECTS", 10),
			ReconnectWait:     getEnvDuration("NATS_RECONNECT_WAIT", 2*time.Second),
			ConnectionTimeout: getEnvDuration("NATS_CONNECTION_TIMEOUT", 10*time.Second),
			IndexingSubject:   getEnvString("NATS_INDEXING_SUBJECT", "lfx.index.>"),
			V1IndexingSubject: getEnvString("NATS_V1_INDEXING_SUBJECT", "lfx.v1.index.>"),
			Queue:             getEnvString("NATS_QUEUE", "lfx.indexer.queue"),
		},
		OpenSearch: OpenSearchConfig{
			URL:   getEnvString("OPENSEARCH_URL", "http://localhost:9200"),
			Index: getEnvString("OPENSEARCH_INDEX", "resources"),
			/*Username: getEnvString("OPENSEARCH_USERNAME", "admin"),
			Password: getEnvString("OPENSEARCH_PASSWORD", "admin"),
			Timeout:  getEnvDuration("OPENSEARCH_TIMEOUT", 30*time.Second),*/
		},
		JWT: JWTConfig{
			Issuer: getEnvString("JWT_ISSUER", "heimdall"),
			Audiences: func() []string {
				if envAudiences := getEnvString("JWT_AUDIENCES", ""); envAudiences != "" {
					return strings.Split(envAudiences, ",")
				}
				// Default to projects-api
				return []string{"projects-api"}
			}(),
			JWKSURL:   getEnvString("JWKS_URL", "http://heimdall:4457/.well-known/jwks"),
			ClockSkew: getEnvDuration("JWT_CLOCK_SKEW", 6*time.Hour),
		},
		Logging: LoggingConfig{
			Level:  getEnvString("LOG_LEVEL", "info"),
			Format: getEnvString("LOG_FORMAT", "json"),
		},
		Janitor: JanitorConfig{
			Enabled: getEnvBool("JANITOR_ENABLED", true),
		},
		Health: HealthConfig{
			CheckTimeout:           getEnvDuration("HEALTH_CHECK_TIMEOUT", 5*time.Second),
			CacheDuration:          getEnvDuration("HEALTH_CACHE_DURATION", 5*time.Second),
			EnableDetailedResponse: getEnvBool("HEALTH_DETAILED_RESPONSE", true),
		},
	}

	return config, nil
}

// Validate validates the configuration by delegating to domain-specific validators
func (c *AppConfig) Validate() error {
	// Validate each configuration domain separately for better separation of concerns
	if err := c.validateServer(); err != nil {
		return fmt.Errorf("server configuration: %w", err)
	}

	if err := c.validateNATS(); err != nil {
		return fmt.Errorf("NATS configuration: %w", err)
	}

	if err := c.validateOpenSearch(); err != nil {
		return fmt.Errorf("OpenSearch configuration: %w", err)
	}

	if err := c.validateJWT(); err != nil {
		return fmt.Errorf("JWT configuration: %w", err)
	}

	if err := c.validateLogging(); err != nil {
		return fmt.Errorf("logging configuration: %w", err)
	}

	if err := c.validateJanitor(); err != nil {
		return fmt.Errorf("janitor configuration: %w", err)
	}

	if err := c.validateHealth(); err != nil {
		return fmt.Errorf("health configuration: %w", err)
	}

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
	if c.JWT.Issuer == "" {
		return fmt.Errorf("JWT issuer is required")
	}

	// Validate that we have at least one audience
	if len(c.JWT.Audiences) == 0 {
		return fmt.Errorf("JWT audiences array is required")
	}

	// Validate JWKS URL if provided
	if c.JWT.JWKSURL == "" {
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

// Helper functions for environment variable parsing
func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
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
