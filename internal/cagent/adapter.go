package cagent

import (
	"context"
	"time"
)

// AgentConfig represents the configuration passed to cagent.
type AgentConfig struct {
	Name        string            `yaml:"name"`
	Goal        string            `yaml:"goal"`
	Instructions string           `yaml:"instructions"`
	Model       ModelConfig       `yaml:"model"`
	Tools       []ToolConfig      `yaml:"tools,omitempty"`
	Permissions PermissionsConfig `yaml:"permissions"`
	Timeout     time.Duration     `yaml:"timeout,omitempty"`
}

// ModelConfig holds model settings for cagent.
type ModelConfig struct {
	Provider string         `yaml:"provider"`
	Name     string         `yaml:"name"`
	Options  map[string]any `yaml:"options,omitempty"`
}

// ToolConfig defines a tool available to the agent.
type ToolConfig struct {
	Name     string         `yaml:"name"`
	Config   map[string]any `yaml:"config,omitempty"`
	Enabled  bool           `yaml:"enabled"`
}

// PermissionsConfig defines permissions for cagent.
type PermissionsConfig struct {
	FilesystemAccess []string `yaml:"filesystem_access,omitempty"`
	NetworkAccess    bool     `yaml:"network_access"`
	ShellAccess      bool     `yaml:"shell_access"`
}

// Adapter translates Horizon agent definitions to cagent config and executes them.
type Adapter interface {
	BuildConfig(ctx context.Context, agentDef interface{}) (*AgentConfig, error)
	Execute(ctx context.Context, config *AgentConfig, workspaceDir string, prompt string, outputWriter interface{}) (string, error)
	ValidateConfig(config *AgentConfig) error
}

// DefaultAdapter is the standard implementation.
type DefaultAdapter struct {
	imageName    string
	containerTimeout time.Duration
}

// NewDefaultAdapter creates a new cagent adapter.
func NewDefaultAdapter(imageName string, containerTimeout time.Duration) *DefaultAdapter {
	return &DefaultAdapter{
		imageName:        imageName,
		containerTimeout: containerTimeout,
	}
}

// BuildConfig converts a Horizon agent definition to cagent config.
func (a *DefaultAdapter) BuildConfig(ctx context.Context, agentDef interface{}) (*AgentConfig, error) {
	// TODO: Implement conversion from agent.AgentDefinition to AgentConfig
	return &AgentConfig{
		Name:        "horizon-agent",
		Goal:        "Execute the given task",
		Instructions: "Follow the prompt",
		Model: ModelConfig{
			Provider: "google",
			Name:     "gemini-1.5-pro",
		},
		Permissions: PermissionsConfig{
			FilesystemAccess: []string{"/workspace:ro"},
			NetworkAccess:    true,
			ShellAccess:      false,
		},
		Timeout: a.containerTimeout,
	}, nil
}

// ValidateConfig checks if the cagent config is valid.
func (a *DefaultAdapter) ValidateConfig(config *AgentConfig) error {
	if config.Name == "" {
		return ErrInvalidConfig("name is required")
	}
	if config.Model.Provider == "" || config.Model.Name == "" {
		return ErrInvalidConfig("model provider and name are required")
	}
	return nil
}

// Execute runs the agent via Podman (delegates to sandbox).
func (a *DefaultAdapter) Execute(ctx context.Context, config *AgentConfig, workspaceDir string, prompt string, outputWriter interface{}) (string, error) {
	// This will be implemented by delegating to the sandbox package
	return "", ErrNotImplemented("Execute not implemented - delegates to sandbox")
}

// ErrInvalidConfig is returned when config validation fails.
type ErrInvalidConfig string

func (e ErrInvalidConfig) Error() string {
	return "invalid cagent config: " + string(e)
}

// ErrNotImplemented is returned for unimplemented methods.
type ErrNotImplemented string

func (e ErrNotImplemented) Error() string {
	return "not implemented: " + string(e)
}