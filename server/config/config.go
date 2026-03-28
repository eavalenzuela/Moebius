package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration for the server processes.
// Shared fields are used by all three (api, worker, scheduler);
// process-specific fields are only relevant to their respective binary.
type Config struct {
	// Shared
	DatabaseURL string
	NATSURL     string
	LogLevel    string // debug, info, warn, error
	LogFormat   string // json, text
	TenantMode  string // single, multi

	// API server only
	HTTPPort          int
	TLSMode           string // passthrough, direct
	TLSCertPath       string
	TLSKeyPath        string
	CACertPath        string
	CAKeyPath         string
	StorageBackend    string // local, s3
	StoragePath       string
	S3Endpoint        string
	S3Bucket          string
	S3Region          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	OIDCIssuerURL     string
	OIDCClientID      string
	OIDCClientSecret  string

	// Worker only
	WorkerConcurrency int
}

// Process identifies which server binary is loading the config.
type Process int

const (
	ProcessAPI Process = iota
	ProcessWorker
	ProcessScheduler
)

// Load reads configuration from environment variables and validates
// required fields based on which process is loading it.
func Load(proc Process) (*Config, error) {
	c := &Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		NATSURL:           os.Getenv("NATS_URL"),
		LogLevel:          envOrDefault("LOG_LEVEL", "info"),
		LogFormat:         envOrDefault("LOG_FORMAT", "json"),
		TenantMode:        envOrDefault("TENANT_MODE", "multi"),
		HTTPPort:          envIntOrDefault("HTTP_PORT", 8080),
		TLSMode:           envOrDefault("TLS_MODE", "passthrough"),
		TLSCertPath:       os.Getenv("TLS_CERT_PATH"),
		TLSKeyPath:        os.Getenv("TLS_KEY_PATH"),
		CACertPath:        os.Getenv("CA_CERT_PATH"),
		CAKeyPath:         os.Getenv("CA_KEY_PATH"),
		StorageBackend:    envOrDefault("STORAGE_BACKEND", "local"),
		StoragePath:       os.Getenv("STORAGE_PATH"),
		S3Endpoint:        os.Getenv("S3_ENDPOINT"),
		S3Bucket:          os.Getenv("S3_BUCKET"),
		S3Region:          os.Getenv("S3_REGION"),
		S3AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		OIDCIssuerURL:     os.Getenv("OIDC_ISSUER_URL"),
		OIDCClientID:      os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:  os.Getenv("OIDC_CLIENT_SECRET"),
		WorkerConcurrency: envIntOrDefault("WORKER_CONCURRENCY", 20),
	}

	if err := c.validate(proc); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate(proc Process) error {
	var missing []string

	// Shared required vars
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.NATSURL == "" {
		missing = append(missing, "NATS_URL")
	}

	// Validate enum values
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid LOG_LEVEL %q: must be debug, info, warn, or error", c.LogLevel)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("invalid LOG_FORMAT %q: must be json or text", c.LogFormat)
	}
	switch c.TenantMode {
	case "single", "multi":
	default:
		return fmt.Errorf("invalid TENANT_MODE %q: must be single or multi", c.TenantMode)
	}

	// API-specific validation
	if proc == ProcessAPI {
		if c.CACertPath == "" {
			missing = append(missing, "CA_CERT_PATH")
		}
		if c.CAKeyPath == "" {
			missing = append(missing, "CA_KEY_PATH")
		}

		switch c.TLSMode {
		case "passthrough", "direct":
		default:
			return fmt.Errorf("invalid TLS_MODE %q: must be passthrough or direct", c.TLSMode)
		}
		if c.TLSMode == "direct" {
			if c.TLSCertPath == "" {
				missing = append(missing, "TLS_CERT_PATH")
			}
			if c.TLSKeyPath == "" {
				missing = append(missing, "TLS_KEY_PATH")
			}
		}

		switch c.StorageBackend {
		case "local":
			if c.StoragePath == "" {
				missing = append(missing, "STORAGE_PATH")
			}
		case "s3":
			if c.S3Endpoint == "" {
				missing = append(missing, "S3_ENDPOINT")
			}
			if c.S3Bucket == "" {
				missing = append(missing, "S3_BUCKET")
			}
			if c.S3Region == "" {
				missing = append(missing, "S3_REGION")
			}
			if c.S3AccessKeyID == "" {
				missing = append(missing, "S3_ACCESS_KEY_ID")
			}
			if c.S3SecretAccessKey == "" {
				missing = append(missing, "S3_SECRET_ACCESS_KEY")
			}
		default:
			return fmt.Errorf("invalid STORAGE_BACKEND %q: must be local or s3", c.StorageBackend)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
