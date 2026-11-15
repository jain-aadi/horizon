package cagent

import (
	"fmt"
	"strings"

	"github.com/jain-aadi/horizon/internal/agent"
	"gopkg.in/yaml.v3"
)

const (
	// DockerAgentVersion is the release whose configuration contract Horizon emits.
	DockerAgentVersion = "v1.158.0"
	// DockerAgentCommit is the immutable commit for DockerAgentVersion.
	DockerAgentCommit = "7f94f376a5d10320591d959b351ca1eeb07c959a"

	// ConfigVersion is the Docker Agent configuration schema version supported by the pinned release.
	ConfigVersion = "16"
	// RootAgentName is the fixed entry-point agent for a Horizon run.
	RootAgentName = "root"
	// HorizonModelName identifies the generated model definition used by the root agent.
	HorizonModelName = "horizon-model"
)

// Config is the supported subset of the Docker Agent v1.158.0 YAML contract.
// Its YAML shape is intentionally separate from Horizon's durable agent model.
type Config struct {
	Version string                 `yaml:"version"`
	Models  map[string]ModelConfig `yaml:"models"`
	Agents  map[string]AgentConfig `yaml:"agents"`
}

// AgentConfig is the generated Docker Agent entry-point configuration.
type AgentConfig struct {
	Model       string `yaml:"model"`
	Description string `yaml:"description,omitempty"`
	Instruction string `yaml:"instruction"`
}

// ModelConfig is the generated Docker Agent model configuration.
type ModelConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// Adapter materializes a validated Horizon definition into pinned Docker Agent YAML.
type Adapter interface {
	BuildConfig(agentDef *agent.AgentDefinition) (*Config, error)
	MarshalConfig(config *Config) ([]byte, error)
	ValidateConfig(config *Config) error
}

// DefaultAdapter is the standard Docker Agent configuration adapter.
type DefaultAdapter struct{}

// NewDefaultAdapter creates a configuration adapter for the pinned Docker Agent contract.
func NewDefaultAdapter() *DefaultAdapter {
	return &DefaultAdapter{}
}

// BuildConfig validates and converts a Horizon agent definition to Docker Agent YAML data.
func (a *DefaultAdapter) BuildConfig(agentDef *agent.AgentDefinition) (*Config, error) {
	if agentDef == nil {
		return nil, ErrInvalidConfig("agent definition is required")
	}
	if err := agentDef.Validate(); err != nil {
		return nil, fmt.Errorf("validate agent definition: %w", err)
	}
	if err := validateSupportedDefinition(agentDef); err != nil {
		return nil, err
	}

	description := agentDef.Description
	if description == "" {
		description = agentDef.Name
	}
	config := &Config{
		Version: ConfigVersion,
		Models: map[string]ModelConfig{
			HorizonModelName: {
				Provider: agentDef.ModelConfig.Provider,
				Model:    agentDef.ModelConfig.Model,
			},
		},
		Agents: map[string]AgentConfig{
			RootAgentName: {
				Model:       HorizonModelName,
				Description: description,
				Instruction: formatInstruction(agentDef.Goal, agentDef.Instructions),
			},
		},
	}
	if err := a.ValidateConfig(config); err != nil {
		return nil, err
	}
	return config, nil
}

func validateSupportedDefinition(agentDef *agent.AgentDefinition) error {
	if len(agentDef.Capabilities) > 0 {
		return &UnsupportedFieldError{Field: "capabilities", Reason: "capability-to-toolset mapping is not implemented"}
	}
	if len(agentDef.Permissions.FilesystemAccess) > 0 {
		return &UnsupportedFieldError{Field: "permissions.filesystem_access", Reason: "filesystem permissions require execution-boundary enforcement"}
	}
	if agentDef.Permissions.NetworkAccess {
		return &UnsupportedFieldError{Field: "permissions.network_access", Reason: "network permissions require execution-boundary enforcement"}
	}
	if agentDef.Permissions.ShellAccess {
		return &UnsupportedFieldError{Field: "permissions.shell_access", Reason: "shell permissions require execution-boundary enforcement"}
	}
	if len(agentDef.Permissions.SecretAccess) > 0 {
		return &UnsupportedFieldError{Field: "permissions.secret_access", Reason: "secret injection is not supported by generated configuration"}
	}
	if agentDef.ModelConfig.APIKeyRef != "" {
		return &UnsupportedFieldError{Field: "model_config.api_key_ref", Reason: "credentials are supplied only by the execution environment"}
	}
	if len(agentDef.ModelConfig.Parameters) > 0 {
		return &UnsupportedFieldError{Field: "model_config.parameters", Reason: "model parameter mapping is not implemented"}
	}
	return nil
}

func formatInstruction(goal, instructions string) string {
	return "Goal:\n" + goal + "\n\nInstructions:\n" + instructions
}

// MarshalConfig serializes a valid generated configuration with yaml.v3.
func (a *DefaultAdapter) MarshalConfig(config *Config) ([]byte, error) {
	if err := a.ValidateConfig(config); err != nil {
		return nil, err
	}
	encoded, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal Docker Agent configuration: %w", err)
	}
	return encoded, nil
}

// ValidateConfig checks the subset of the pinned Docker Agent contract Horizon emits.
func (a *DefaultAdapter) ValidateConfig(config *Config) error {
	if config == nil {
		return ErrInvalidConfig("configuration is required")
	}
	if config.Version != ConfigVersion {
		return ErrInvalidConfig("version must be " + ConfigVersion)
	}
	root, ok := config.Agents[RootAgentName]
	if !ok {
		return ErrInvalidConfig("root agent is required")
	}
	if root.Model != HorizonModelName {
		return ErrInvalidConfig("root agent must reference " + HorizonModelName)
	}
	if strings.TrimSpace(root.Instruction) == "" {
		return ErrInvalidConfig("root agent instruction is required")
	}
	model, ok := config.Models[HorizonModelName]
	if !ok {
		return ErrInvalidConfig("generated model is required")
	}
	if model.Provider != "google" || strings.TrimSpace(model.Model) == "" {
		return ErrInvalidConfig("generated model must use the supported google provider and a model name")
	}
	return nil
}

// UnsupportedFieldError identifies a Horizon field that the pinned contract cannot enforce safely yet.
type UnsupportedFieldError struct {
	Field  string
	Reason string
}

func (e *UnsupportedFieldError) Error() string {
	return "unsupported " + e.Field + ": " + e.Reason
}

// ErrInvalidConfig is returned when generated Docker Agent configuration is invalid.
type ErrInvalidConfig string

func (e ErrInvalidConfig) Error() string {
	return "invalid Docker Agent configuration: " + string(e)
}
