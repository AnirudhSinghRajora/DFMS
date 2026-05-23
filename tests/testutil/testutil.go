// Package testutil provides shared test helpers for DFMS unit and integration tests.
package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AnirudhSinghRajora/DFMS/internal/auth"
	"github.com/AnirudhSinghRajora/DFMS/internal/config"
)

// GenerateTestKeyPair creates an ephemeral ECDSA P-256 key pair for testing.
// Does not touch the filesystem.
func GenerateTestKeyPair(t *testing.T) (*ecdsa.PrivateKey, *ecdsa.PublicKey) {
	t.Helper()
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate test key pair: %v", err)
	}
	return privKey, &privKey.PublicKey
}

// WriteTestKeys writes a test ECDSA key pair to temporary PEM files
// and returns their paths. The files are automatically cleaned up when
// the test completes.
func WriteTestKeys(t *testing.T) (privPath, pubPath string) {
	t.Helper()
	privKey, _ := GenerateTestKeyPair(t)

	dir := t.TempDir()

	// Write private key
	privPath = filepath.Join(dir, "test-private.pem")
	privBytes, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		t.Fatalf("Failed to marshal private key: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})
	if err := os.WriteFile(privPath, privPEM, 0600); err != nil {
		t.Fatalf("Failed to write private key: %v", err)
	}

	// Write public key
	pubPath = filepath.Join(dir, "test-public.pem")
	pubBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatalf("Failed to marshal public key: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})
	if err := os.WriteFile(pubPath, pubPEM, 0600); err != nil {
		t.Fatalf("Failed to write public key: %v", err)
	}

	return privPath, pubPath
}

// NewTestJWTService creates a JWTService with ephemeral test keys.
// Uses NewJWTServiceFromKeys to avoid filesystem I/O.
func NewTestJWTService(t *testing.T) *auth.JWTService {
	t.Helper()
	privKey, pubKey := GenerateTestKeyPair(t)
	return auth.NewJWTServiceFromKeys(privKey, pubKey, config.JWTConfig{
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 168 * time.Hour,
		Issuer:     "dfms-test",
	})
}

// NewTestConfig returns a valid Config struct for testing.
func NewTestConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			HTTPPort: 8080,
			GRPCPort: 9090,
			Mode:     "development",
		},
		Database: config.DatabaseConfig{
			Host:           "localhost",
			Port:           5432,
			User:           "test",
			Password:       "test",
			Database:       "test",
			MaxConnections: 10,
			MinConnections: 2,
			SSLMode:        "disable",
		},
		Redis: config.RedisConfig{
			Addr:     "localhost:6379",
			PoolSize: 10,
		},
		MinIO: config.MinIOConfig{
			Endpoints:   []string{"localhost:9000"},
			AccessKey:   "test",
			SecretKey:   "test",
			ChunkBucket: "chunks",
			TempBucket:  "temp-uploads",
		},
		Kafka: config.KafkaConfig{
			Brokers:       []string{"localhost:9092"},
			ConsumerGroup: "test-group",
		},
		JWT: config.JWTConfig{
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 168 * time.Hour,
			Issuer:     "dfms-test",
		},
		Chunking: config.ChunkingConfig{
			MinSize: 256 * 1024,
			AvgSize: 1024 * 1024,
			MaxSize: 4 * 1024 * 1024,
		},
		Replication: config.ReplicationConfig{
			Factor:       3,
			VirtualNodes: 150,
		},
		RateLimit: config.RateLimitConfig{
			GlobalRPM: 10000,
			UserRPM:   200,
			AuthRPM:   5,
		},
		Storage: config.StorageConfig{
			DefaultQuota:  5 * 1024 * 1024 * 1024,
			MaxUploadSize: 10 * 1024 * 1024 * 1024,
		},
		GC: config.GCConfig{
			ScanInterval: 6 * time.Hour,
			GracePeriod:  24 * time.Hour,
			BatchSize:    500,
		},
		HealthMonitor: config.HealthMonitorConfig{
			CheckInterval: 30 * time.Second,
			Timeout:       5 * time.Second,
		},
	}
}

// RandomBytes returns n random bytes using crypto/rand.
func RandomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("Failed to generate random bytes: %v", err)
	}
	return b
}
