package config

import (
	"time"
)

// Config holds all application configuration.
type Config struct {
	Server   ServerConfig   `json:"server"`
	Database DatabaseConfig `json:"database"`
	Podman   PodmanConfig   `json:"podman"`
	Agent    AgentConfig    `json:"agent"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// DatabaseConfig holds database settings.
type DatabaseConfig struct {
	Path string `json:"path"`
}

// PodmanConfig holds Podman connection settings.
type PodmanConfig struct {
	SocketPath       string        `json:"socket_path"`
	ImageName        string        `json:"image_name"`
	APIVersion       string        `json:"api_version"`
	RequestTimeout   time.Duration `json:"request_timeout"`
	ContainerTimeout time.Duration `json:"container_timeout"`
}

// AgentConfig holds default agent settings.
type AgentConfig struct {
	DefaultTimeout time.Duration `json:"default_timeout"`
	DefaultImage   string        `json:"default_image"`
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
		Database: DatabaseConfig{
			Path: "./data/horizon.db",
		},
		Podman: PodmanConfig{
			SocketPath:       "",
			ImageName:        "localhost/horizon-sandbox:latest",
			APIVersion:       "v4.0.0",
			RequestTimeout:   30 * time.Second,
			ContainerTimeout: 10 * time.Minute,
		},
		Agent: AgentConfig{
			DefaultTimeout: 10 * time.Minute,
			DefaultImage:   "localhost/horizon-sandbox:latest",
		},
	}
}