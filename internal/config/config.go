package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

// Config defines the supported application configuration.
type Config struct {
	App      AppConfig      `yaml:"app"`
	Exports  ExportsConfig  `yaml:"exports"`
	TicketID TicketIDConfig `yaml:"ticket_id"`
	Webhook  WebhookConfig  `yaml:"webhook"`
}

type AppConfig struct {
	Timezone string `yaml:"timezone"`
}

type ExportsConfig struct {
	DefaultDir string `yaml:"default_dir"`
}

type TicketIDConfig struct {
	TimestampFormat string `yaml:"timestamp_format"`
}

type WebhookConfig struct {
	EndpointURL         string `yaml:"endpoint_url"`
	TimeoutSeconds      int    `yaml:"timeout_seconds"`
	MaxRetries          int    `yaml:"max_retries"`
	RetryBackoffSeconds int    `yaml:"retry_backoff_seconds"`
}

// Load reads config from path if present and merges it over defaults.
func Load(path string, defaultExportsDir string) (Config, error) {
	cfg := Defaults(defaultExportsDir)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}

	if cfg.App.Timezone == "" {
		cfg.App.Timezone = "Local"
	}
	if cfg.Exports.DefaultDir == "" {
		cfg.Exports.DefaultDir = defaultExportsDir
	}
	if cfg.TicketID.TimestampFormat == "" {
		cfg.TicketID.TimestampFormat = "YYMMDDHHmm"
	}
	if cfg.Webhook.TimeoutSeconds == 0 {
		cfg.Webhook.TimeoutSeconds = 10
	}
	if cfg.Webhook.MaxRetries == 0 {
		cfg.Webhook.MaxRetries = 3
	}
	if cfg.Webhook.RetryBackoffSeconds == 0 {
		cfg.Webhook.RetryBackoffSeconds = 5
	}

	return cfg, nil
}

// Defaults returns the in-memory baseline config.
func Defaults(defaultExportsDir string) Config {
	return Config{
		App: AppConfig{
			Timezone: "Local",
		},
		Exports: ExportsConfig{
			DefaultDir: defaultExportsDir,
		},
		TicketID: TicketIDConfig{
			TimestampFormat: "YYMMDDHHmm",
		},
		Webhook: WebhookConfig{
			TimeoutSeconds:      10,
			MaxRetries:          3,
			RetryBackoffSeconds: 5,
		},
	}
}
