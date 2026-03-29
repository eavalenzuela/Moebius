package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config is the agent's TOML configuration.
type Config struct {
	Server  ServerConfig  `toml:"server"`
	Storage StorageConfig `toml:"storage"`
	LocalUI LocalUIConfig `toml:"local_ui"`
	Logging LoggingConfig `toml:"logging"`
	CDM     CDMConfig     `toml:"cdm"`
}

type ServerConfig struct {
	URL                 string `toml:"url"`
	PollIntervalSeconds int    `toml:"poll_interval_seconds"`
}

type StorageConfig struct {
	DropDirectory       string  `toml:"drop_directory"`
	SpaceCheckEnabled   bool    `toml:"space_check_enabled"`
	SpaceCheckThreshold float64 `toml:"space_check_threshold"`
}

type LocalUIConfig struct {
	Enabled bool `toml:"enabled"`
	Port    int  `toml:"port"`
}

type LoggingConfig struct {
	Level string `toml:"level"`
	File  string `toml:"file"`
}

type CDMConfig struct {
	Enabled bool `toml:"enabled"`
}

// Defaults returns a Config with default values.
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			PollIntervalSeconds: 30,
		},
		Storage: StorageConfig{
			SpaceCheckEnabled:   true,
			SpaceCheckThreshold: 0.50,
		},
		LocalUI: LocalUIConfig{
			Enabled: true,
			Port:    57000,
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

// Load reads and parses a TOML config file, applying defaults for unset fields.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path) //nolint:gosec // operator-controlled config path
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.Server.URL == "" {
		return fmt.Errorf("server.url is required")
	}
	if c.Server.PollIntervalSeconds < 5 {
		return fmt.Errorf("server.poll_interval_seconds must be >= 5")
	}
	return nil
}
