package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration for the server processes.
// Shared fields are used by both (api, scheduler);
// process-specific fields are only relevant to their respective binary.
type Config struct {
	// Shared
	DatabaseURL string
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
	TrustedProxyCIDRs string // comma-separated CIDRs trusted to forward X-Client-Cert
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

	// Rate limiting (API server only)
	RateLimitEnabled           bool
	RateLimitPerIPRPM          int // requests per minute per IP
	RateLimitPerIPBurst        int
	RateLimitPerTenantRPM      int // requests per minute per tenant
	RateLimitPerTenantBurst    int
	RateLimitAgentCheckinRPM   int // check-ins per minute per agent
	RateLimitAgentCheckinBurst int

	// Per-tenant resource quotas (API server only). -1 = unlimited.
	QuotaMaxDevices       int64
	QuotaMaxQueuedJobs    int64
	QuotaMaxAPIKeys       int64
	QuotaMaxFileSizeBytes int64

	// Scheduler only
	SchedulerTickSeconds       int    // tick interval for cron evaluation
	ReaperDispatchedTimeoutSec int    // dispatched jobs older than this are requeued
	ReaperInflightTimeoutSec   int    // acknowledged/running jobs older than this fail with timed_out
	SMTPHost                   string // SMTP server host for email alerts
	SMTPPort                   int
	SMTPUsername               string
	SMTPPassword               string
	SMTPFrom                   string // sender address for alert emails
}

// Process identifies which server binary is loading the config.
type Process int

const (
	ProcessAPI Process = iota
	ProcessScheduler
)

// Load reads configuration from environment variables and validates
// required fields based on which process is loading it.
func Load(proc Process) (*Config, error) {
	c := &Config{
		DatabaseURL:                os.Getenv("DATABASE_URL"),
		LogLevel:                   envOrDefault("LOG_LEVEL", "info"),
		LogFormat:                  envOrDefault("LOG_FORMAT", "json"),
		TenantMode:                 envOrDefault("TENANT_MODE", "multi"),
		HTTPPort:                   envIntOrDefault("HTTP_PORT", 8080),
		TLSMode:                    envOrDefault("TLS_MODE", "passthrough"),
		TrustedProxyCIDRs:          envOrDefault("TRUSTED_PROXY_CIDRS", "127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1/128,fd00::/8"),
		TLSCertPath:                os.Getenv("TLS_CERT_PATH"),
		TLSKeyPath:                 os.Getenv("TLS_KEY_PATH"),
		CACertPath:                 os.Getenv("CA_CERT_PATH"),
		CAKeyPath:                  os.Getenv("CA_KEY_PATH"),
		StorageBackend:             envOrDefault("STORAGE_BACKEND", "local"),
		StoragePath:                envOrDefault("STORAGE_PATH", "/tmp/moebius-storage"),
		S3Endpoint:                 os.Getenv("S3_ENDPOINT"),
		S3Bucket:                   os.Getenv("S3_BUCKET"),
		S3Region:                   os.Getenv("S3_REGION"),
		S3AccessKeyID:              os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretAccessKey:          os.Getenv("S3_SECRET_ACCESS_KEY"),
		OIDCIssuerURL:              os.Getenv("OIDC_ISSUER_URL"),
		OIDCClientID:               os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:           os.Getenv("OIDC_CLIENT_SECRET"),
		SchedulerTickSeconds:       envIntOrDefault("SCHEDULER_TICK_SECONDS", 30),
		ReaperDispatchedTimeoutSec: envIntOrDefault("REAPER_DISPATCHED_TIMEOUT_SECONDS", 300),
		ReaperInflightTimeoutSec:   envIntOrDefault("REAPER_INFLIGHT_TIMEOUT_SECONDS", 3600),
		SMTPHost:                   os.Getenv("SMTP_HOST"),
		SMTPPort:                   envIntOrDefault("SMTP_PORT", 587),
		SMTPUsername:               os.Getenv("SMTP_USERNAME"),
		SMTPPassword:               os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:                   envOrDefault("SMTP_FROM", "moebius@localhost"),
		RateLimitEnabled:           envOrDefault("RATE_LIMIT_ENABLED", "true") == "true",
		RateLimitPerIPRPM:          envIntOrDefault("RATE_LIMIT_PER_IP_RPM", 60),
		RateLimitPerIPBurst:        envIntOrDefault("RATE_LIMIT_PER_IP_BURST", 10),
		RateLimitPerTenantRPM:      envIntOrDefault("RATE_LIMIT_PER_TENANT_RPM", 600),
		RateLimitPerTenantBurst:    envIntOrDefault("RATE_LIMIT_PER_TENANT_BURST", 50),
		RateLimitAgentCheckinRPM:   envIntOrDefault("RATE_LIMIT_AGENT_CHECKIN_RPM", 6),
		RateLimitAgentCheckinBurst: envIntOrDefault("RATE_LIMIT_AGENT_CHECKIN_BURST", 3),
		QuotaMaxDevices:            envInt64OrDefault("QUOTA_MAX_DEVICES_PER_TENANT", 10000),
		QuotaMaxQueuedJobs:         envInt64OrDefault("QUOTA_MAX_QUEUED_JOBS_PER_TENANT", 10000),
		QuotaMaxAPIKeys:            envInt64OrDefault("QUOTA_MAX_API_KEYS_PER_TENANT", 100),
		QuotaMaxFileSizeBytes:      envInt64OrDefault("QUOTA_MAX_FILE_SIZE_BYTES", 1024*1024*1024),
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

func envInt64OrDefault(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}
