package config

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host = %q, want %q", cfg.Server.Host, "127.0.0.1")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 8080)
	}
	if cfg.Database.Path != "./data/horizon.db" {
		t.Errorf("Database.Path = %q, want %q", cfg.Database.Path, "./data/horizon.db")
	}
	if cfg.Podman.ImageName != "localhost/horizon-sandbox:latest" {
		t.Errorf("Podman.ImageName = %q, want %q", cfg.Podman.ImageName, "localhost/horizon-sandbox:latest")
	}
	if cfg.Podman.RequestTimeout != 30*time.Second {
		t.Errorf("Podman.RequestTimeout = %s, want %s", cfg.Podman.RequestTimeout, 30*time.Second)
	}
	if cfg.Podman.ContainerTimeout != 10*time.Minute {
		t.Errorf("Podman.ContainerTimeout = %s, want %s", cfg.Podman.ContainerTimeout, 10*time.Minute)
	}
	if cfg.Agent.DefaultTimeout != 10*time.Minute {
		t.Errorf("Agent.DefaultTimeout = %s, want %s", cfg.Agent.DefaultTimeout, 10*time.Minute)
	}
	if cfg.Agent.DefaultImage != "localhost/horizon-sandbox:latest" {
		t.Errorf("Agent.DefaultImage = %q, want %q", cfg.Agent.DefaultImage, "localhost/horizon-sandbox:latest")
	}
}

func TestLoadFromEnvOverridesDefaults(t *testing.T) {
	t.Setenv("HORIZON_DB_PATH", "./testdata/horizon.db")
	t.Setenv("HORIZON_PORT", "9090")
	t.Setenv("HORIZON_SOCKET", "/tmp/podman.sock")
	t.Setenv("HORIZON_IMAGE", "registry.example/horizon:test")
	t.Setenv("HORIZON_REQUEST_TIMEOUT", "15s")
	t.Setenv("HORIZON_CONTAINER_TIMEOUT", "4m")
	t.Setenv("HORIZON_AGENT_TIMEOUT", "5m")

	cfg := DefaultConfig()
	if err := cfg.LoadFromEnv(); err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.Database.Path != "./testdata/horizon.db" {
		t.Errorf("Database.Path = %q, want %q", cfg.Database.Path, "./testdata/horizon.db")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 9090)
	}
	if cfg.Podman.SocketPath != "/tmp/podman.sock" {
		t.Errorf("Podman.SocketPath = %q, want %q", cfg.Podman.SocketPath, "/tmp/podman.sock")
	}
	if cfg.Podman.ImageName != "registry.example/horizon:test" {
		t.Errorf("Podman.ImageName = %q, want %q", cfg.Podman.ImageName, "registry.example/horizon:test")
	}
	if cfg.Agent.DefaultImage != "registry.example/horizon:test" {
		t.Errorf("Agent.DefaultImage = %q, want %q", cfg.Agent.DefaultImage, "registry.example/horizon:test")
	}
	if cfg.Podman.RequestTimeout != 15*time.Second {
		t.Errorf("Podman.RequestTimeout = %s, want %s", cfg.Podman.RequestTimeout, 15*time.Second)
	}
	if cfg.Podman.ContainerTimeout != 4*time.Minute {
		t.Errorf("Podman.ContainerTimeout = %s, want %s", cfg.Podman.ContainerTimeout, 4*time.Minute)
	}
	if cfg.Agent.DefaultTimeout != 5*time.Minute {
		t.Errorf("Agent.DefaultTimeout = %s, want %s", cfg.Agent.DefaultTimeout, 5*time.Minute)
	}
}

func TestLoadFromEnvRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "non-numeric port", key: "HORIZON_PORT", value: "invalid"},
		{name: "zero port", key: "HORIZON_PORT", value: "0"},
		{name: "port above range", key: "HORIZON_PORT", value: "65536"},
		{name: "invalid request timeout", key: "HORIZON_REQUEST_TIMEOUT", value: "not-a-duration"},
		{name: "zero container timeout", key: "HORIZON_CONTAINER_TIMEOUT", value: "0s"},
		{name: "negative agent timeout", key: "HORIZON_AGENT_TIMEOUT", value: "-1m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)

			if err := DefaultConfig().LoadFromEnv(); err == nil {
				t.Fatalf("LoadFromEnv() error = nil, want an error for %s=%q", tt.key, tt.value)
			}
		})
	}
}
