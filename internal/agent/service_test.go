package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/horizon/horizon/internal/storage"
)

const testDefaultImage = "localhost/horizon-sandbox:test"

func newTestService(t *testing.T, defaultImage string) *Service {
	t.Helper()

	db, err := storage.NewDB(filepath.Join(t.TempDir(), "horizon.db"))
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return NewService(storage.NewAgentRepository(db), storage.NewRunRepository(db), defaultImage)
}

func validAgentDefinition() *AgentDefinition {
	return &AgentDefinition{
		Name:         "Daily brief",
		Goal:         "Prepare a concise daily brief.",
		Instructions: "Summarize the configured input in three bullet points.",
		ModelConfig: ModelConfig{
			Provider: "google",
			Model:    "gemini-3.5-flash-lite",
		},
	}
}

func TestCreateAgentAssignsLifecycleFieldsAndDefaultImage(t *testing.T) {
	service := newTestService(t, testDefaultImage)
	definition := validAgentDefinition()

	if err := service.CreateAgent(context.Background(), definition); err != nil {
		t.Fatalf("CreateAgent() error = %v", err)
	}

	if definition.ID == "" {
		t.Error("CreateAgent() did not assign an ID")
	}
	if definition.Version != 1 {
		t.Errorf("Version = %d, want 1", definition.Version)
	}
	if definition.CreatedAt.IsZero() || definition.UpdatedAt.IsZero() {
		t.Errorf("timestamps = (%s, %s), want both set", definition.CreatedAt, definition.UpdatedAt)
	}
	if definition.CreatedAt != definition.UpdatedAt {
		t.Errorf("timestamps = (%s, %s), want equal on creation", definition.CreatedAt, definition.UpdatedAt)
	}
	if definition.RuntimeConfig.Image != testDefaultImage {
		t.Errorf("RuntimeConfig.Image = %q, want %q", definition.RuntimeConfig.Image, testDefaultImage)
	}

	stored, err := service.GetAgent(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("GetAgent() error = %v", err)
	}
	if stored.Version != 1 {
		t.Errorf("stored Version = %d, want 1", stored.Version)
	}
	if stored.RuntimeConfig.Image != testDefaultImage {
		t.Errorf("stored RuntimeConfig.Image = %q, want %q", stored.RuntimeConfig.Image, testDefaultImage)
	}
}

func TestCreateAgentRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name           string
		noDefaultImage bool
		mutate         func(*AgentDefinition)
		wantFieldName  string
	}{
		{
			name: "missing name",
			mutate: func(definition *AgentDefinition) {
				definition.Name = " \t"
			},
			wantFieldName: "name",
		},
		{
			name: "missing goal",
			mutate: func(definition *AgentDefinition) {
				definition.Goal = ""
			},
			wantFieldName: "goal",
		},
		{
			name: "missing instructions",
			mutate: func(definition *AgentDefinition) {
				definition.Instructions = ""
			},
			wantFieldName: "instructions",
		},
		{
			name:           "missing runtime image without default",
			noDefaultImage: true,
			wantFieldName:  "runtime_config.image",
		},
		{
			name: "runtime image contains whitespace",
			mutate: func(definition *AgentDefinition) {
				definition.RuntimeConfig.Image = "invalid image"
			},
			wantFieldName: "runtime_config.image",
		},
		{
			name: "unsupported provider",
			mutate: func(definition *AgentDefinition) {
				definition.ModelConfig.Provider = "other"
			},
			wantFieldName: "model_config.provider",
		},
		{
			name: "missing model",
			mutate: func(definition *AgentDefinition) {
				definition.ModelConfig.Model = ""
			},
			wantFieldName: "model_config.model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defaultImage := testDefaultImage
			if tt.noDefaultImage {
				defaultImage = ""
			}
			service := newTestService(t, defaultImage)
			definition := validAgentDefinition()
			if tt.mutate != nil {
				tt.mutate(definition)
			}

			err := service.CreateAgent(context.Background(), definition)
			if err == nil {
				t.Fatal("CreateAgent() error = nil, want validation error")
			}

			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("CreateAgent() error = %T %v, want *ValidationError", err, err)
			}
			if validationError.Field != tt.wantFieldName {
				t.Errorf("ValidationError.Field = %q, want %q", validationError.Field, tt.wantFieldName)
			}
		})
	}
}

func TestUpdateAgentPreservesCreationFieldsAndIncrementsVersion(t *testing.T) {
	service := newTestService(t, testDefaultImage)
	definition := validAgentDefinition()
	if err := service.CreateAgent(context.Background(), definition); err != nil {
		t.Fatalf("CreateAgent() error = %v", err)
	}
	originalID := definition.ID
	originalCreatedAt := definition.CreatedAt
	originalUpdatedAt := definition.UpdatedAt

	definition.Name = "Updated daily brief"
	definition.CreatedAt = time.Time{}
	definition.UpdatedAt = time.Time{}
	definition.Version = 99
	if err := service.UpdateAgent(context.Background(), definition); err != nil {
		t.Fatalf("UpdateAgent() error = %v", err)
	}

	if definition.ID != originalID {
		t.Errorf("ID = %q, want %q", definition.ID, originalID)
	}
	if definition.CreatedAt != originalCreatedAt {
		t.Errorf("CreatedAt = %s, want %s", definition.CreatedAt, originalCreatedAt)
	}
	if definition.UpdatedAt.Before(originalUpdatedAt) {
		t.Errorf("UpdatedAt = %s, want after %s", definition.UpdatedAt, originalUpdatedAt)
	}
	if definition.Version != 2 {
		t.Errorf("Version = %d, want 2", definition.Version)
	}

	stored, err := service.GetAgent(context.Background(), originalID)
	if err != nil {
		t.Fatalf("GetAgent() after update error = %v", err)
	}
	if stored.Name != "Updated daily brief" {
		t.Errorf("stored Name = %q, want %q", stored.Name, "Updated daily brief")
	}
	if stored.CreatedAt != originalCreatedAt {
		t.Errorf("stored CreatedAt = %s, want %s", stored.CreatedAt, originalCreatedAt)
	}
	if stored.Version != 2 {
		t.Errorf("stored Version = %d, want 2", stored.Version)
	}
}
