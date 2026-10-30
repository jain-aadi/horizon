package agent

import (
	"encoding/json"
	"time"

	"github.com/horizon/horizon/internal/run"
	"github.com/horizon/horizon/internal/storage"
)

// AgentDefinition represents the durable description of an agent.
type AgentDefinition struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Goal        string                 `json:"goal"`
	Instructions string                `json:"instructions"`
	RuntimeConfig RuntimeConfig         `json:"runtime_config"`
	ModelConfig   ModelConfig           `json:"model_config"`
	Capabilities  []string              `json:"capabilities"`
	Permissions   Permissions           `json:"permissions"`
	Schedule      *ScheduleConfig       `json:"schedule,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	Version       int                   `json:"version"`
	RunHistory    []run.RunRecord       `json:"run_history,omitempty"`
}

// RuntimeConfig holds container runtime settings.
type RuntimeConfig struct {
	Image         string            `json:"image"`
	Entrypoint    []string          `json:"entrypoint,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	WorkingDir    string            `json:"working_dir,omitempty"`
	ResourceLimits ResourceLimits   `json:"resource_limits,omitempty"`
}

// ResourceLimits defines container resource constraints.
type ResourceLimits struct {
	CPULimit    string `json:"cpu_limit,omitempty"`
	MemoryLimit string `json:"memory_limit,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
}

// ModelConfig holds model/provider configuration.
type ModelConfig struct {
	Provider  string            `json:"provider"`
	Model     string            `json:"model"`
	APIKeyRef string            `json:"api_key_ref,omitempty"`
	Parameters map[string]any   `json:"parameters,omitempty"`
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

// ToStorageRow converts AgentDefinition to storage.AgentRow.
func (def *AgentDefinition) ToStorageRow() *storage.AgentRow {
	runtimeConfig, _ := json.Marshal(def.RuntimeConfig)
	modelConfig, _ := json.Marshal(def.ModelConfig)
	capabilities, _ := json.Marshal(def.Capabilities)
	permissions, _ := json.Marshal(def.Permissions)
	var schedule json.RawMessage
	if def.Schedule != nil {
		schedule, _ = json.Marshal(def.Schedule)
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
	}
}