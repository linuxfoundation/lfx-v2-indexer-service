package config

import (
	"fmt"
	"os"
	"strconv"
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
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// JanitorConfig contains janitor configuration
type JanitorConfig struct {
	Enabled   bool          `json:"enabled"`
	Interval  time.Duration `json:"interval"`
	BatchSize int           `json:"batch_size"`
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
			Issuer:   getEnvString("JWT_ISSUER", "heimdall"),
			Audience: getEnvString("JWT_AUDIENCE", "projects-api"),
		},
		Logging: LoggingConfig{
			Level:  getEnvString("LOG_LEVEL", "info"),
			Format: getEnvString("LOG_FORMAT", "json"),
		},
		Janitor: JanitorConfig{
			Enabled:   getEnvBool("JANITOR_ENABLED", true),
			Interval:  getEnvDuration("JANITOR_INTERVAL", 5*time.Minute),
			BatchSize: getEnvInt("JANITOR_BATCH_SIZE", 100),
		},
	}

	return config, nil
}

// Validate validates the configuration
func (c *AppConfig) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.NATS.URL == "" {
		return fmt.Errorf("NATS URL is required")
	}

	if c.OpenSearch.URL == "" {
		return fmt.Errorf("OpenSearch URL is required")
	}

	if c.OpenSearch.Index == "" {
		return fmt.Errorf("OpenSearch index is required")
	}

	if c.JWT.Issuer == "" {
		return fmt.Errorf("JWT issuer is required")
	}

	if c.JWT.Audience == "" {
		return fmt.Errorf("JWT audience is required")
	}

	validLogLevels := []string{"debug", "info", "warn", "error"}
	if !contains(validLogLevels, c.Logging.Level) {
		return fmt.Errorf("invalid log level: %s", c.Logging.Level)
	}

	validLogFormats := []string{"json", "text"}
	if !contains(validLogFormats, c.Logging.Format) {
		return fmt.Errorf("invalid log format: %s", c.Logging.Format)
	}

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
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
