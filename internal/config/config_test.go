package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	t.Parallel()

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), "/tmp/exports")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Exports.DefaultDir != "/tmp/exports" {
		t.Fatalf("expected default export dir, got %q", cfg.Exports.DefaultDir)
	}
	if cfg.TicketID.TimestampFormat != "YYMMDDHHmm" {
		t.Fatalf("expected timestamp format default, got %q", cfg.TicketID.TimestampFormat)
	}
}

func TestLoadOverridesFromYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("app:\n  timezone: America/New_York\nexports:\n  default_dir: /custom/exports\nwebhook:\n  timeout_seconds: 15\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	cfg, err := Load(path, "/tmp/exports")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.App.Timezone != "America/New_York" {
		t.Fatalf("expected timezone override, got %q", cfg.App.Timezone)
	}
	if cfg.Exports.DefaultDir != "/custom/exports" {
		t.Fatalf("expected export dir override, got %q", cfg.Exports.DefaultDir)
	}
	if cfg.Webhook.TimeoutSeconds != 15 {
		t.Fatalf("expected timeout override, got %d", cfg.Webhook.TimeoutSeconds)
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("app: ["), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, err := Load(path, "/tmp/exports"); err == nil {
		t.Fatal("expected invalid YAML error")
	}
}
