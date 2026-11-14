package config

import (
	"fmt"
	"os"
	"strconv"
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

// LoadFromEnv loads configuration from environment variables.
// Environment variables take precedence over defaults.
func (c *Config) LoadFromEnv() error {
	if v := os.Getenv("HORIZON_DB_PATH"); v != "" {
		c.Database.Path = v
	}
	if v := os.Getenv("HORIZON_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("parse HORIZON_PORT: %w", err)
		}
		if port < 1 || port > 65535 {
			return fmt.Errorf("HORIZON_PORT must be between 1 and 65535")
		}
		c.Server.Port = port
	}
	if v := os.Getenv("HORIZON_SOCKET"); v != "" {
		c.Podman.SocketPath = v
	}
	if v := os.Getenv("HORIZON_IMAGE"); v != "" {
		c.Podman.ImageName = v
		c.Agent.DefaultImage = v
	}
	if err := loadDurationFromEnv("HORIZON_REQUEST_TIMEOUT", &c.Podman.RequestTimeout); err != nil {
		return err
	}
	if err := loadDurationFromEnv("HORIZON_CONTAINER_TIMEOUT", &c.Podman.ContainerTimeout); err != nil {
		return err
	}
	if err := loadDurationFromEnv("HORIZON_AGENT_TIMEOUT", &c.Agent.DefaultTimeout); err != nil {
		return err
	}
	return nil
}

func loadDurationFromEnv(name string, target *time.Duration) error {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}

	duration, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	if duration <= 0 {
		return fmt.Errorf("%s must be greater than zero", name)
	}
	*target = duration
	return nil
}
