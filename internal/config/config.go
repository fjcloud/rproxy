package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the application configuration.
type Config struct {
	UpdateInterval    time.Duration
	// CertsDir          string // Removed - Hardcoded to /certs in certs/manager.go
	CertCheckInterval time.Duration
	RenewBefore       time.Duration

	SSHUser string
	SSHHost string // Set via Makefile
	SSHPort string // Set via Makefile
	// SSHIdentityFile string // Removed field

	GandiAPIKey string
	ACMEEmail   string
	GandiZone   string
	ACMEStaging bool
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		// Defaults
		UpdateInterval:    10 * time.Second,
		// CertsDir:          "/certs", // Removed
		CertCheckInterval: 12 * time.Hour,
		RenewBefore:       30 * 24 * time.Hour,
		SSHUser:           "core", // Default SSH user
		ACMEStaging:       false,
	}

	// Load from environment variables
	cfg.SSHUser = getEnv("PODMAN_SSH_USER", cfg.SSHUser)
	cfg.SSHHost = getEnv("PODMAN_SSH_HOST", "") // Expect host set by Makefile
	cfg.SSHPort = getEnv("PODMAN_SSH_PORT", "") // Expect port set by Makefile
	// cfg.SSHIdentityFile = getEnv("PODMAN_SSH_KEY", "") // Removed line
	cfg.GandiAPIKey = getEnv("GANDI_API_KEY", "")
	cfg.ACMEEmail = getEnv("ACME_EMAIL", "")
	cfg.GandiZone = getEnv("GANDI_ZONE", "")
	cfg.ACMEStaging = getEnvAsBool("LEGO_STAGING", cfg.ACMEStaging)
	// cfg.CertsDir = getEnv("CERTS_DIR", cfg.CertsDir) // Removed

	// Validate required fields
	if cfg.SSHHost == "" {
		return nil, fmt.Errorf("PODMAN_SSH_HOST environment variable must be set (expected from Makefile)")
	}
	if cfg.SSHPort == "" {
		return nil, fmt.Errorf("PODMAN_SSH_PORT environment variable must be set (expected from Makefile)")
	}
	/* // Removed validation block for SSHIdentityFile
	if cfg.SSHIdentityFile == "" {
		return nil, fmt.Errorf("PODMAN_SSH_KEY environment variable must be set")
	}
	if _, err := os.Stat(cfg.SSHIdentityFile); os.IsNotExist(err) {
         return nil, fmt.Errorf("SSH identity file not found at %s", cfg.SSHIdentityFile)
     } else if err != nil {
         return nil, fmt.Errorf("error checking SSH identity file %s: %w", cfg.SSHIdentityFile, err)
     }
	*/

	if cfg.GandiAPIKey == "" {
		return nil, fmt.Errorf("GANDI_API_KEY environment variable must be set (in .env)")
	}
	if cfg.ACMEEmail == "" {
		return nil, fmt.Errorf("ACME_EMAIL environment variable must be set (in .env)")
	}
	if cfg.GandiZone == "" {
		return nil, fmt.Errorf("GANDI_ZONE environment variable must be set (in .env)")
	}

	/* // Removed certs dir check
	// Ensure certs directory exists
	if err := os.MkdirAll(cfg.CertsDir, 0700); err != nil {
		// Log warning but allow continuation, maybe permissions are fixed later
		slog.Warn("Could not create certs directory", "path", cfg.CertsDir, "error", err)
	}
	*/

	slog.Info("Configuration loaded.")
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getEnvAsBool(key string, fallback bool) bool {
	if valueStr, exists := os.LookupEnv(key); exists {
		value, err := strconv.ParseBool(strings.ToLower(valueStr))
		if err == nil {
			return value
		}
		slog.Warn("Invalid boolean value for environment variable", "key", key, "value", valueStr, "error", err, "default", fallback)
	}
	return fallback
} 