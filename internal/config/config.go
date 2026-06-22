// Package config provides application configuration loading and validation.
// Configuration is loaded from YAML files with environment variable overrides.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all application configuration.
type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	ChunkService  ChunkServiceConfig  `mapstructure:"chunk_service"`
	Database      DatabaseConfig      `mapstructure:"database"`
	Redis         RedisConfig         `mapstructure:"redis"`
	MinIO         MinIOConfig         `mapstructure:"minio"`
	Kafka         KafkaConfig         `mapstructure:"kafka"`
	JWT           JWTConfig           `mapstructure:"jwt"`
	Chunking      ChunkingConfig      `mapstructure:"chunking"`
	Replication   ReplicationConfig   `mapstructure:"replication"`
	RateLimit     RateLimitConfig     `mapstructure:"rate_limit"`
	Storage       StorageConfig       `mapstructure:"storage"`
	GC            GCConfig            `mapstructure:"gc"`
	HealthMonitor HealthMonitorConfig `mapstructure:"health_monitor"`
}

// ServerConfig holds HTTP/gRPC server settings.
type ServerConfig struct {
	HTTPPort int    `mapstructure:"http_port"`
	GRPCPort int    `mapstructure:"grpc_port"`
	Mode     string `mapstructure:"mode"` // development | production
}

// ChunkServiceConfig holds the settings the API Gateway uses to reach the
// Chunk Service (the data-plane gRPC service) over the network. It is distinct
// from ServerConfig, which describes a service's own listeners: this is the
// address of a downstream dependency, not a local bind address.
type ChunkServiceConfig struct {
	GRPCAddr string `mapstructure:"grpc_addr"` // host:port of the Chunk Service gRPC endpoint
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	Host           string `mapstructure:"host"`
	Port           int    `mapstructure:"port"`
	User           string `mapstructure:"user"`
	Password       string `mapstructure:"password"`
	Database       string `mapstructure:"database"`
	MaxConnections int32  `mapstructure:"max_connections"`
	MinConnections int32  `mapstructure:"min_connections"`
	SSLMode        string `mapstructure:"ssl_mode"`
}

// DSN returns the PostgreSQL connection string.
func (d *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		d.User, d.Password, d.Host, d.Port, d.Database, d.SSLMode,
	)
}

// RedisConfig holds Redis connection settings.
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

// MinIOConfig holds MinIO storage settings.
type MinIOConfig struct {
	Endpoints   []string `mapstructure:"endpoints"`
	AccessKey   string   `mapstructure:"access_key"`
	SecretKey   string   `mapstructure:"secret_key"`
	UseSSL      bool     `mapstructure:"use_ssl"`
	ChunkBucket string   `mapstructure:"chunk_bucket"`
	TempBucket  string   `mapstructure:"temp_bucket"`
}

// KafkaConfig holds Kafka settings.
type KafkaConfig struct {
	Brokers       []string `mapstructure:"brokers"`
	ConsumerGroup string   `mapstructure:"consumer_group"`
}

// JWTConfig holds JWT authentication settings.
type JWTConfig struct {
	PrivateKeyPath string        `mapstructure:"private_key_path"`
	PublicKeyPath  string        `mapstructure:"public_key_path"`
	AccessTTL      time.Duration `mapstructure:"access_ttl"`
	RefreshTTL     time.Duration `mapstructure:"refresh_ttl"`
	Issuer         string        `mapstructure:"issuer"`
}

// ChunkingConfig holds file chunking parameters.
type ChunkingConfig struct {
	MinSize int `mapstructure:"min_size"`
	AvgSize int `mapstructure:"avg_size"`
	MaxSize int `mapstructure:"max_size"`
}

// ReplicationConfig holds replication settings.
type ReplicationConfig struct {
	Factor       int `mapstructure:"factor"`
	VirtualNodes int `mapstructure:"virtual_nodes"`
}

// RateLimitConfig holds rate limiting thresholds (requests per minute).
type RateLimitConfig struct {
	GlobalRPM   int `mapstructure:"global_rpm"`
	UserRPM     int `mapstructure:"user_rpm"`
	UploadRPM   int `mapstructure:"upload_rpm"`
	DownloadRPM int `mapstructure:"download_rpm"`
	AuthRPM     int `mapstructure:"auth_rpm"`
}

// StorageConfig holds storage quota settings.
type StorageConfig struct {
	DefaultQuota  int64 `mapstructure:"default_quota"`
	MaxUploadSize int64 `mapstructure:"max_upload_size"`
}

// GCConfig holds garbage collector settings.
type GCConfig struct {
	ScanInterval time.Duration `mapstructure:"scan_interval"` // How often to scan for orphans
	GracePeriod  time.Duration `mapstructure:"grace_period"`  // Wait before deleting marked chunks
	BatchSize    int           `mapstructure:"batch_size"`    // Max chunks per GC cycle
}

// HealthMonitorConfig holds health monitor settings.
type HealthMonitorConfig struct {
	CheckInterval time.Duration `mapstructure:"check_interval"` // How often to probe each node
	Timeout       time.Duration `mapstructure:"timeout"`        // Probe timeout per node
}

// Load reads configuration from the specified YAML file path and merges
// environment variable overrides. Environment variables use the prefix DFMS_
// and replace dots/nested keys with underscores (e.g., DFMS_DATABASE_PASSWORD).
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Read config file
	v.SetConfigFile(configPath)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	// Environment variable overrides
	v.SetEnvPrefix("DFMS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// The API Gateway dials the Chunk Service over gRPC. When the address is not
	// set explicitly, fall back to the gRPC port on localhost — this matches the
	// single-host development layout where every service runs on the same machine.
	// Containerized deployments must set chunk_service.grpc_addr to the service's
	// network name (e.g. "chunk-service:9091").
	if cfg.ChunkService.GRPCAddr == "" {
		cfg.ChunkService.GRPCAddr = fmt.Sprintf("localhost:%d", cfg.Server.GRPCPort)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// Validate checks that the configuration values are sane.
func (c *Config) Validate() error {
	if c.Server.HTTPPort <= 0 || c.Server.HTTPPort > 65535 {
		return fmt.Errorf("invalid HTTP port: %d", c.Server.HTTPPort)
	}
	if c.Server.GRPCPort <= 0 || c.Server.GRPCPort > 65535 {
		return fmt.Errorf("invalid gRPC port: %d", c.Server.GRPCPort)
	}
	if c.Server.Mode != "development" && c.Server.Mode != "production" {
		return fmt.Errorf("invalid server mode: %s (must be development or production)", c.Server.Mode)
	}
	if c.Database.Host == "" {
		return fmt.Errorf("database host is required")
	}
	if c.Database.MaxConnections <= 0 {
		return fmt.Errorf("database max_connections must be positive")
	}
	if len(c.MinIO.Endpoints) == 0 {
		return fmt.Errorf("at least one MinIO endpoint is required")
	}
	if len(c.Kafka.Brokers) == 0 {
		return fmt.Errorf("at least one Kafka broker is required")
	}
	if c.JWT.AccessTTL <= 0 {
		return fmt.Errorf("JWT access_ttl must be positive")
	}
	if c.Chunking.MinSize <= 0 || c.Chunking.AvgSize <= 0 || c.Chunking.MaxSize <= 0 {
		return fmt.Errorf("chunking sizes must be positive")
	}
	if c.Chunking.MinSize >= c.Chunking.AvgSize || c.Chunking.AvgSize >= c.Chunking.MaxSize {
		return fmt.Errorf("chunking sizes must satisfy: min < avg < max")
	}
	if c.Replication.Factor < 1 {
		return fmt.Errorf("replication factor must be at least 1")
	}
	return nil
}

// IsProduction returns true if the server is running in production mode.
func (c *Config) IsProduction() bool {
	return c.Server.Mode == "production"
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.http_port", 8080)
	v.SetDefault("server.grpc_port", 9090)
	v.SetDefault("server.mode", "development")
	// Registered (empty) so AutomaticEnv can bind DFMS_CHUNK_SERVICE_GRPC_ADDR
	// even when the key is absent from the config file. When left empty it is
	// derived from server.grpc_port in Load (see the fallback there).
	v.SetDefault("chunk_service.grpc_addr", "")
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.max_connections", 25)
	v.SetDefault("database.min_connections", 5)
	v.SetDefault("database.ssl_mode", "disable")
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.pool_size", 20)
	v.SetDefault("minio.use_ssl", false)
	v.SetDefault("minio.chunk_bucket", "chunks")
	v.SetDefault("minio.temp_bucket", "temp-uploads")
	v.SetDefault("kafka.consumer_group", "dfms-consumers")
	v.SetDefault("jwt.access_ttl", "15m")
	v.SetDefault("jwt.refresh_ttl", "168h")
	v.SetDefault("jwt.issuer", "dfms-auth")
	v.SetDefault("chunking.min_size", 262144)
	v.SetDefault("chunking.avg_size", 1048576)
	v.SetDefault("chunking.max_size", 4194304)
	v.SetDefault("replication.factor", 3)
	v.SetDefault("replication.virtual_nodes", 150)
	v.SetDefault("rate_limit.global_rpm", 10000)
	v.SetDefault("rate_limit.user_rpm", 200)
	v.SetDefault("rate_limit.upload_rpm", 20)
	v.SetDefault("rate_limit.download_rpm", 100)
	v.SetDefault("rate_limit.auth_rpm", 5)
	v.SetDefault("storage.default_quota", 5368709120)
	v.SetDefault("storage.max_upload_size", 10737418240)
	v.SetDefault("gc.scan_interval", "6h")
	v.SetDefault("gc.grace_period", "24h")
	v.SetDefault("gc.batch_size", 500)
	v.SetDefault("health_monitor.check_interval", "30s")
	v.SetDefault("health_monitor.timeout", "5s")
}
