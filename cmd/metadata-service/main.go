// DFMS Metadata Service — manages file/folder metadata, manifests, and versioning.
// Implementation: Phase 4 (Core Storage Sprint)
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/AnirudhSinghRajora/DFMS/internal/config"
)

func main() {
	configPath := os.Getenv("DFMS_CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.dev.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Metadata Service starting on gRPC port %d (not yet implemented)\n", cfg.Server.GRPCPort)
}
