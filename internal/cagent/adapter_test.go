package cagent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jain-aadi/horizon/internal/agent"
	"gopkg.in/yaml.v3"
)

func validDefinition() *agent.AgentDefinition {
	return &agent.AgentDefinition{
		Name:         "Daily brief",
		Description:  "Summarizes the configured daily inputs.",
		Goal:         "Prepare a concise daily brief.",
		Instructions: "Return exactly three bullet points.",
		RuntimeConfig: agent.RuntimeConfig{
			Image: "localhost/horizon-sandbox:test",
		},
		ModelConfig: agent.ModelConfig{
			Provider: "google",
			Model:    "gemini-3.5-flash-lite",
		},
		Permissions: agent.Permissions{NetworkAccess: true},
	}
}

func TestBuildConfigMapsValidatedDefinition(t *testing.T) {
	adapter := NewDefaultAdapter()
	definition := validDefinition()

	config, err := adapter.BuildConfig(definition)
	if err != nil {
		t.Fatalf("BuildConfig() error = %v", err)
	}
	if config.Version != ConfigVersion {
		t.Errorf("Version = %q, want %q", config.Version, ConfigVersion)
	}

	root, ok := config.Agents[RootAgentName]
	if !ok {
		t.Fatalf("Agents[%q] is missing", RootAgentName)
	}
	if root.Model != HorizonModelName {
		t.Errorf("root Model = %q, want %q", root.Model, HorizonModelName)
	}
	if root.Description != definition.Description {
		t.Errorf("root Description = %q, want %q", root.Description, definition.Description)
	}
	wantInstruction := "Goal:\n" + definition.Goal + "\n\nInstructions:\n" + definition.Instructions
	if root.Instruction != wantInstruction {
		t.Errorf("root Instruction = %q, want %q", root.Instruction, wantInstruction)
	}

	model, ok := config.Models[HorizonModelName]
	if !ok {
		t.Fatalf("Models[%q] is missing", HorizonModelName)
	}
	if model.Provider != definition.ModelConfig.Provider || model.Model != definition.ModelConfig.Model {
		t.Errorf("model = %#v, want provider=%q model=%q", model, definition.ModelConfig.Provider, definition.ModelConfig.Model)
	}

	encoded, err := adapter.MarshalConfig(config)
	if err != nil {
		t.Fatalf("MarshalConfig() error = %v", err)
	}
	var decoded Config
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal generated YAML error = %v\n%s", err, encoded)
	}
	if err := adapter.ValidateConfig(&decoded); err != nil {
		t.Fatalf("ValidateConfig() after YAML round trip error = %v", err)
	}
}

func TestBuildConfigRejectsUnsupportedDefinitionFields(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*agent.AgentDefinition)
		wantField string
	}{
		{
			name: "capabilities",
			mutate: func(definition *agent.AgentDefinition) {
				definition.Capabilities = []string{"web-search"}
			},
			wantField: "capabilities",
		},
		{
			name: "filesystem access",
			mutate: func(definition *agent.AgentDefinition) {
				definition.Permissions.FilesystemAccess = []string{"/workspace:ro"}
			},
			wantField: "permissions.filesystem_access",
		},
		{
			name: "shell access",
			mutate: func(definition *agent.AgentDefinition) {
				definition.Permissions.ShellAccess = true
			},
			wantField: "permissions.shell_access",
		},
		{
			name: "secret access",
			mutate: func(definition *agent.AgentDefinition) {
				definition.Permissions.SecretAccess = []string{"GOOGLE_API_KEY"}
			},
			wantField: "permissions.secret_access",
		},
		{
			name: "model API key reference",
			mutate: func(definition *agent.AgentDefinition) {
				definition.ModelConfig.APIKeyRef = "google-api-key"
			},
			wantField: "model_config.api_key_ref",
		},
		{
			name: "model parameters",
			mutate: func(definition *agent.AgentDefinition) {
				definition.ModelConfig.Parameters = map[string]any{"temperature": 0.2}
			},
			wantField: "model_config.parameters",
		},
	}

	adapter := NewDefaultAdapter()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			definition := validDefinition()
			tt.mutate(definition)

			_, err := adapter.BuildConfig(definition)
			if err == nil {
				t.Fatal("BuildConfig() error = nil, want unsupported field error")
			}
			var unsupported *UnsupportedFieldError
			if !errors.As(err, &unsupported) {
				t.Fatalf("BuildConfig() error = %T %v, want *UnsupportedFieldError", err, err)
			}
			if unsupported.Field != tt.wantField {
				t.Errorf("UnsupportedFieldError.Field = %q, want %q", unsupported.Field, tt.wantField)
			}
		})
	}
}

func TestValidateConfigRejectsInvalidContract(t *testing.T) {
	adapter := NewDefaultAdapter()
	config := &Config{
		Version: ConfigVersion,
		Agents: map[string]AgentConfig{
			RootAgentName: {
				Model:       HorizonModelName,
				Instruction: "Do the task.",
			},
		},
		Models: map[string]ModelConfig{
			HorizonModelName: {
				Provider: "google",
				Model:    "gemini-3.5-flash-lite",
			},
		},
	}
	if err := adapter.ValidateConfig(config); err != nil {
		t.Fatalf("ValidateConfig(valid config) error = %v", err)
	}

	config.Version = "15"
	if err := adapter.ValidateConfig(config); err == nil {
		t.Fatal("ValidateConfig(invalid version) error = nil")
	}
}

func TestWorkspaceFixtureUsesPinnedContract(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "workspace", "test-agent.yaml")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture error = %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(fixture, &config); err != nil {
		t.Fatalf("unmarshal fixture error = %v", err)
	}
	if err := NewDefaultAdapter().ValidateConfig(&config); err != nil {
		t.Fatalf("ValidateConfig(fixture) error = %v", err)
	}
}
