package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jain-aadi/horizon/internal/run"
	"github.com/jain-aadi/horizon/internal/storage"
)

// AgentDefinition represents the durable description of an agent.
type AgentDefinition struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Goal          string          `json:"goal"`
	Instructions  string          `json:"instructions"`
	RuntimeConfig RuntimeConfig   `json:"runtime_config"`
	ModelConfig   ModelConfig     `json:"model_config"`
	Capabilities  []string        `json:"capabilities"`
	Permissions   Permissions     `json:"permissions"`
	Schedule      *ScheduleConfig `json:"schedule,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Version       int             `json:"version"`
	RunHistory    []run.RunRecord `json:"run_history,omitempty"`
}

// RuntimeConfig holds container runtime settings.
type RuntimeConfig struct {
	Image          string            `json:"image"`
	Entrypoint     []string          `json:"entrypoint,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	ResourceLimits ResourceLimits    `json:"resource_limits,omitempty"`
}

// ResourceLimits defines container resource constraints.
type ResourceLimits struct {
	CPULimit    string `json:"cpu_limit,omitempty"`
	MemoryLimit string `json:"memory_limit,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
}

// ModelConfig holds model/provider configuration.
type ModelConfig struct {
	Provider   string         `json:"provider"`
	Model      string         `json:"model"`
	APIKeyRef  string         `json:"api_key_ref,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// Permissions defines what the agent is allowed to do.
type Permissions struct {
	FilesystemAccess []string `json:"filesystem_access,omitempty"`
	NetworkAccess    bool     `json:"network_access"`
	ShellAccess      bool     `json:"shell_access"`
	SecretAccess     []string `json:"secret_access,omitempty"`
}

// ScheduleConfig holds recurring schedule settings.
type ScheduleConfig struct {
	CronExpression string `json:"cron_expression"`
	Timezone       string `json:"timezone"`
	Enabled        bool   `json:"enabled"`
}

// ValidationError identifies a client-supplied agent definition field that is invalid.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ApplyDefaults fills values that Horizon owns globally rather than requiring every
// caller to duplicate the same fallback logic.
func (def *AgentDefinition) ApplyDefaults(defaultImage string) {
	def.Name = strings.TrimSpace(def.Name)
	def.Description = strings.TrimSpace(def.Description)
	def.Goal = strings.TrimSpace(def.Goal)
	def.Instructions = strings.TrimSpace(def.Instructions)
	def.RuntimeConfig.Image = strings.TrimSpace(def.RuntimeConfig.Image)
	def.ModelConfig.Provider = strings.ToLower(strings.TrimSpace(def.ModelConfig.Provider))
	def.ModelConfig.Model = strings.TrimSpace(def.ModelConfig.Model)

	if def.RuntimeConfig.Image == "" {
		def.RuntimeConfig.Image = strings.TrimSpace(defaultImage)
	}
}

// Validate checks the fields required for Horizon's initial cagent integration.
func (def *AgentDefinition) Validate() error {
	if strings.TrimSpace(def.Name) == "" {
		return &ValidationError{Field: "name", Message: "is required"}
	}
	if strings.TrimSpace(def.Goal) == "" {
		return &ValidationError{Field: "goal", Message: "is required"}
	}
	if strings.TrimSpace(def.Instructions) == "" {
		return &ValidationError{Field: "instructions", Message: "is required"}
	}
	if def.RuntimeConfig.Image == "" {
		return &ValidationError{Field: "runtime_config.image", Message: "is required"}
	}
	if strings.ContainsAny(def.RuntimeConfig.Image, " 	\r\n") {
		return &ValidationError{Field: "runtime_config.image", Message: "must not contain whitespace"}
	}
	if def.ModelConfig.Provider == "" {
		return &ValidationError{Field: "model_config.provider", Message: "is required"}
	}
	if def.ModelConfig.Provider != "google" {
		return &ValidationError{Field: "model_config.provider", Message: "must be google"}
	}
	if def.ModelConfig.Model == "" {
		return &ValidationError{Field: "model_config.model", Message: "is required"}
	}
	return nil
}

// ToStorageRow converts AgentDefinition to storage.AgentRow.
func (def *AgentDefinition) ToStorageRow() (*storage.AgentRow, error) {
	runtimeConfig, err := json.Marshal(def.RuntimeConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal runtime config: %w", err)
	}
	modelConfig, err := json.Marshal(def.ModelConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal model config: %w", err)
	}
	capabilities, err := json.Marshal(def.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("marshal capabilities: %w", err)
	}
	permissions, err := json.Marshal(def.Permissions)
	if err != nil {
		return nil, fmt.Errorf("marshal permissions: %w", err)
	}
	var schedule json.RawMessage
	if def.Schedule != nil {
		schedule, err = json.Marshal(def.Schedule)
		if err != nil {
			return nil, fmt.Errorf("marshal schedule: %w", err)
		}
	} else {
		schedule = json.RawMessage("null")
	}

	return &storage.AgentRow{
		ID:            def.ID,
		Name:          def.Name,
		Description:   def.Description,
		Goal:          def.Goal,
		Instructions:  def.Instructions,
		RuntimeConfig: runtimeConfig,
		ModelConfig:   modelConfig,
		Capabilities:  capabilities,
		Permissions:   permissions,
		Schedule:      schedule,
		CreatedAt:     def.CreatedAt,
		UpdatedAt:     def.UpdatedAt,
		Version:       def.Version,
	}, nil
}
