package config

import (
	"os"
	"testing"
)

// setEnv sets env vars for the duration of a test and clears them on cleanup.
func setEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

func sharedEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://localhost/moebius",
		"NATS_URL":     "nats://localhost:4222",
	}
}

func apiEnv() map[string]string {
	m := sharedEnv()
	m["CA_CERT_PATH"] = "/etc/moebius/ca.crt"
	m["CA_KEY_PATH"] = "/etc/moebius/ca.key"
	m["STORAGE_PATH"] = "/var/lib/moebius/files"
	return m
}

func TestLoadWorker_Defaults(t *testing.T) {
	setEnv(t, sharedEnv())

	cfg, err := Load(ProcessWorker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want %q", cfg.LogFormat, "json")
	}
	if cfg.TenantMode != "multi" {
		t.Errorf("TenantMode = %q, want %q", cfg.TenantMode, "multi")
	}
	if cfg.WorkerConcurrency != 20 {
		t.Errorf("WorkerConcurrency = %d, want 20", cfg.WorkerConcurrency)
	}
}

func TestLoadAPI_Defaults(t *testing.T) {
	setEnv(t, apiEnv())

	cfg, err := Load(ProcessAPI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want 8080", cfg.HTTPPort)
	}
	if cfg.TLSMode != "passthrough" {
		t.Errorf("TLSMode = %q, want %q", cfg.TLSMode, "passthrough")
	}
	if cfg.StorageBackend != "local" {
		t.Errorf("StorageBackend = %q, want %q", cfg.StorageBackend, "local")
	}
}

func TestLoadAPI_DirectTLS(t *testing.T) {
	env := apiEnv()
	env["TLS_MODE"] = "direct"
	env["TLS_CERT_PATH"] = "/etc/moebius/tls.crt"
	env["TLS_KEY_PATH"] = "/etc/moebius/tls.key"
	setEnv(t, env)

	_, err := Load(ProcessAPI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadAPI_DirectTLS_MissingCert(t *testing.T) {
	env := apiEnv()
	env["TLS_MODE"] = "direct"
	setEnv(t, env)

	_, err := Load(ProcessAPI)
	if err == nil {
		t.Fatal("expected error for missing TLS_CERT_PATH and TLS_KEY_PATH")
	}
}

func TestLoadAPI_S3Storage(t *testing.T) {
	env := apiEnv()
	delete(env, "STORAGE_PATH")
	env["STORAGE_BACKEND"] = "s3"
	env["S3_ENDPOINT"] = "https://s3.amazonaws.com"
	env["S3_BUCKET"] = "moebius-files"
	env["S3_REGION"] = "us-east-1"
	env["S3_ACCESS_KEY_ID"] = "AKID"
	env["S3_SECRET_ACCESS_KEY"] = "secret"
	setEnv(t, env)

	cfg, err := Load(ProcessAPI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.S3Bucket != "moebius-files" {
		t.Errorf("S3Bucket = %q, want %q", cfg.S3Bucket, "moebius-files")
	}
}

func TestLoadAPI_S3_MissingBucket(t *testing.T) {
	env := apiEnv()
	delete(env, "STORAGE_PATH")
	env["STORAGE_BACKEND"] = "s3"
	env["S3_ENDPOINT"] = "https://s3.amazonaws.com"
	env["S3_REGION"] = "us-east-1"
	env["S3_ACCESS_KEY_ID"] = "AKID"
	env["S3_SECRET_ACCESS_KEY"] = "secret"
	setEnv(t, env)

	_, err := Load(ProcessAPI)
	if err == nil {
		t.Fatal("expected error for missing S3_BUCKET")
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("NATS_URL", "nats://localhost:4222")

	_, err := Load(ProcessWorker)
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	env := sharedEnv()
	env["LOG_LEVEL"] = "verbose"
	setEnv(t, env)

	_, err := Load(ProcessWorker)
	if err == nil {
		t.Fatal("expected error for invalid LOG_LEVEL")
	}
}

func TestLoad_CustomHTTPPort(t *testing.T) {
	env := apiEnv()
	env["HTTP_PORT"] = "9090"
	setEnv(t, env)

	cfg, err := Load(ProcessAPI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPPort != 9090 {
		t.Errorf("HTTPPort = %d, want 9090", cfg.HTTPPort)
	}
}

func TestLoad_InvalidHTTPPort(t *testing.T) {
	env := apiEnv()
	env["HTTP_PORT"] = "not-a-number"
	setEnv(t, env)

	cfg, err := Load(ProcessAPI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid int falls back to default
	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want 8080 (fallback)", cfg.HTTPPort)
	}
}

func TestLoad_SchedulerMinimal(t *testing.T) {
	setEnv(t, sharedEnv())

	// Scheduler only needs shared vars
	cfg, err := Load(ProcessScheduler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SchedulerTickSeconds != 30 {
		t.Errorf("SchedulerTickSeconds = %d, want 30", cfg.SchedulerTickSeconds)
	}
	if cfg.SMTPPort != 587 {
		t.Errorf("SMTPPort = %d, want 587", cfg.SMTPPort)
	}
	if cfg.SMTPFrom != "moebius@localhost" {
		t.Errorf("SMTPFrom = %q, want %q", cfg.SMTPFrom, "moebius@localhost")
	}
}

func TestLoad_SchedulerCustomSMTP(t *testing.T) {
	env := sharedEnv()
	env["SCHEDULER_TICK_SECONDS"] = "10"
	env["SMTP_HOST"] = "smtp.example.com"
	env["SMTP_PORT"] = "465"
	env["SMTP_USERNAME"] = "user"
	env["SMTP_PASSWORD"] = "pass"
	env["SMTP_FROM"] = "alerts@example.com"
	setEnv(t, env)

	cfg, err := Load(ProcessScheduler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SchedulerTickSeconds != 10 {
		t.Errorf("SchedulerTickSeconds = %d, want 10", cfg.SchedulerTickSeconds)
	}
	if cfg.SMTPHost != "smtp.example.com" {
		t.Errorf("SMTPHost = %q, want %q", cfg.SMTPHost, "smtp.example.com")
	}
	if cfg.SMTPPort != 465 {
		t.Errorf("SMTPPort = %d, want 465", cfg.SMTPPort)
	}
	if cfg.SMTPFrom != "alerts@example.com" {
		t.Errorf("SMTPFrom = %q, want %q", cfg.SMTPFrom, "alerts@example.com")
	}
}

// Ensure we don't leak env between tests — sanity check that t.Setenv restores.
func TestLoad_CleanEnv(t *testing.T) {
	if os.Getenv("DATABASE_URL") != "" {
		t.Error("DATABASE_URL should not be set outside of test helpers")
	}
}
