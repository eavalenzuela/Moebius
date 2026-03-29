package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Server.PollIntervalSeconds != 30 {
		t.Errorf("poll_interval = %d, want 30", cfg.Server.PollIntervalSeconds)
	}
	if cfg.LocalUI.Port != 57000 {
		t.Errorf("local_ui.port = %d, want 57000", cfg.LocalUI.Port)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("logging.level = %q, want %q", cfg.Logging.Level, "info")
	}
	if !cfg.Storage.SpaceCheckEnabled {
		t.Error("storage.space_check_enabled should default to true")
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	content := `
[server]
url = "https://manage.example.com"
poll_interval_seconds = 60

[logging]
level = "debug"

[cdm]
enabled = true
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.URL != "https://manage.example.com" {
		t.Errorf("server.url = %q", cfg.Server.URL)
	}
	if cfg.Server.PollIntervalSeconds != 60 {
		t.Errorf("poll_interval = %d, want 60", cfg.Server.PollIntervalSeconds)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("logging.level = %q, want %q", cfg.Logging.Level, "debug")
	}
	if !cfg.CDM.Enabled {
		t.Error("cdm.enabled should be true")
	}
	// Defaults preserved for unset fields
	if cfg.LocalUI.Port != 57000 {
		t.Errorf("local_ui.port = %d, want 57000 (default)", cfg.LocalUI.Port)
	}
}

func TestLoad_MissingURL(t *testing.T) {
	content := `
[server]
poll_interval_seconds = 30
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing server.url")
	}
}

func TestLoad_PollIntervalTooLow(t *testing.T) {
	content := `
[server]
url = "https://example.com"
poll_interval_seconds = 2
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for poll_interval < 5")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	path := writeTemp(t, `[[[broken`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
