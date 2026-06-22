package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AnirudhSinghRajora/DFMS/internal/config"
)

// minimalValidConfig is the minimal YAML that passes validation.
const minimalValidConfig = `
server:
  http_port: 8080
  grpc_port: 9090
  mode: development

database:
  host: localhost
  port: 5432
  user: test
  password: test
  database: testdb
  max_connections: 10
  ssl_mode: disable

redis:
  addr: localhost:6379

minio:
  endpoints:
    - localhost:9000
  access_key: minioadmin
  secret_key: minioadmin

kafka:
  brokers:
    - localhost:9092

jwt:
  private_key_path: /tmp/test-private.pem
  public_key_path: /tmp/test-public.pem
  access_ttl: 15m
  refresh_ttl: 168h
  issuer: dfms-test

chunking:
  min_size: 262144
  avg_size: 1048576
  max_size: 4194304

replication:
  factor: 3
  virtual_nodes: 150
`

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	path := writeConfigFile(t, minimalValidConfig)

	cfg, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, 8080, cfg.Server.HTTPPort)
	assert.Equal(t, 9090, cfg.Server.GRPCPort)
	assert.Equal(t, "development", cfg.Server.Mode)
	assert.Equal(t, "localhost", cfg.Database.Host)
	assert.Equal(t, "localhost:6379", cfg.Redis.Addr)
	assert.Equal(t, []string{"localhost:9000"}, cfg.MinIO.Endpoints)
	assert.Equal(t, 3, cfg.Replication.Factor)
}

func TestLoad_ChunkServiceAddr_Explicit(t *testing.T) {
	path := writeConfigFile(t, minimalValidConfig+`
chunk_service:
  grpc_addr: chunk-service:9091
`)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "chunk-service:9091", cfg.ChunkService.GRPCAddr)
}

func TestLoad_ChunkServiceAddr_DefaultsToLocalhostGRPCPort(t *testing.T) {
	// minimalValidConfig omits chunk_service entirely and uses grpc_port 9090,
	// so the address should fall back to the local single-host default.
	path := writeConfigFile(t, minimalValidConfig)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "localhost:9090", cfg.ChunkService.GRPCAddr)
}

func TestLoad_ChunkServiceAddr_EnvOverride(t *testing.T) {
	t.Setenv("DFMS_CHUNK_SERVICE_GRPC_ADDR", "chunk-service.internal:9091")
	path := writeConfigFile(t, minimalValidConfig)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "chunk-service.internal:9091", cfg.ChunkService.GRPCAddr)
}

func TestLoad_NonexistentFile(t *testing.T) {
	_, err := config.Load("/nonexistent/config.yaml")
	assert.Error(t, err)
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeConfigFile(t, "{{invalid yaml}}")
	_, err := config.Load(path)
	assert.Error(t, err)
}

func TestDSN(t *testing.T) {
	db := config.DatabaseConfig{
		Host:     "db.example.com",
		Port:     5432,
		User:     "myuser",
		Password: "mypass",
		Database: "mydb",
		SSLMode:  "require",
	}

	dsn := db.DSN()
	assert.Equal(t, "postgres://myuser:mypass@db.example.com:5432/mydb?sslmode=require", dsn)
}

func TestValidate_InvalidPort_Zero(t *testing.T) {
	cfg := validConfig()
	cfg.Server.HTTPPort = 0
	assert.Error(t, cfg.Validate())
}

func TestValidate_InvalidPort_TooHigh(t *testing.T) {
	cfg := validConfig()
	cfg.Server.HTTPPort = 70000
	assert.Error(t, cfg.Validate())
}

func TestValidate_InvalidGRPCPort(t *testing.T) {
	cfg := validConfig()
	cfg.Server.GRPCPort = -1
	assert.Error(t, cfg.Validate())
}

func TestValidate_InvalidMode(t *testing.T) {
	cfg := validConfig()
	cfg.Server.Mode = "staging"
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid server mode")
}

func TestValidate_EmptyDatabaseHost(t *testing.T) {
	cfg := validConfig()
	cfg.Database.Host = ""
	assert.Error(t, cfg.Validate())
}

func TestValidate_ZeroMaxConnections(t *testing.T) {
	cfg := validConfig()
	cfg.Database.MaxConnections = 0
	assert.Error(t, cfg.Validate())
}

func TestValidate_NoMinIOEndpoints(t *testing.T) {
	cfg := validConfig()
	cfg.MinIO.Endpoints = nil
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "MinIO endpoint")
}

func TestValidate_NoKafkaBrokers(t *testing.T) {
	cfg := validConfig()
	cfg.Kafka.Brokers = nil
	assert.Error(t, cfg.Validate())
}

func TestValidate_InvalidJWTAccessTTL(t *testing.T) {
	cfg := validConfig()
	cfg.JWT.AccessTTL = 0
	assert.Error(t, cfg.Validate())
}

func TestValidate_InvalidChunkSizes_MinGteAvg(t *testing.T) {
	cfg := validConfig()
	cfg.Chunking.MinSize = 2000000
	cfg.Chunking.AvgSize = 1000000
	assert.Error(t, cfg.Validate())
}

func TestValidate_InvalidChunkSizes_AvgGteMax(t *testing.T) {
	cfg := validConfig()
	cfg.Chunking.AvgSize = 5000000
	cfg.Chunking.MaxSize = 4000000
	assert.Error(t, cfg.Validate())
}

func TestValidate_InvalidChunkSizes_Zero(t *testing.T) {
	cfg := validConfig()
	cfg.Chunking.MinSize = 0
	assert.Error(t, cfg.Validate())
}

func TestValidate_InvalidReplicationFactor(t *testing.T) {
	cfg := validConfig()
	cfg.Replication.Factor = 0
	assert.Error(t, cfg.Validate())
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := validConfig()
	assert.NoError(t, cfg.Validate())
}

func TestIsProduction(t *testing.T) {
	cfg := validConfig()
	assert.False(t, cfg.IsProduction())

	cfg.Server.Mode = "production"
	assert.True(t, cfg.IsProduction())
}

func validConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{HTTPPort: 8080, GRPCPort: 9090, Mode: "development"},
		Database: config.DatabaseConfig{
			Host: "localhost", Port: 5432, MaxConnections: 10,
		},
		MinIO:       config.MinIOConfig{Endpoints: []string{"localhost:9000"}},
		Kafka:       config.KafkaConfig{Brokers: []string{"localhost:9092"}},
		JWT:         config.JWTConfig{AccessTTL: 900000000000}, // 15m in nanoseconds
		Chunking:    config.ChunkingConfig{MinSize: 256 * 1024, AvgSize: 1024 * 1024, MaxSize: 4 * 1024 * 1024},
		Replication: config.ReplicationConfig{Factor: 3, VirtualNodes: 150},
	}
}
