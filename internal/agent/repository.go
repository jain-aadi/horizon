package agent

import (
	"context"
)

// AgentRepository defines the persistence interface for agents.
type AgentRepository interface {
	Save(ctx context.Context, def *AgentDefinition) error
	GetByID(ctx context.Context, id string) (*AgentDefinition, error)
	List(ctx context.Context) ([]*AgentDefinition, error)
	Delete(ctx context.Context, id string) error
}

// RunRepository defines the persistence interface for runs.
type RunRepository interface {
	Save(ctx context.Context, rec *RunRecord) error
	GetByID(ctx context.Context, id string) (*RunRecord, error)
	ListByAgentID(ctx context.Context, agentID string) ([]RunRecord, error)
	DeleteByAgentID(ctx context.Context, agentID string) error
}

// RunRecord is a local alias to avoid import cycles.
type RunRecord struct {
	ID              string
	AgentID         string
	AgentVersion    int
	TriggerType     string
	Status          string
	StartedAt       string
	CompletedAt     *string
	ExitCode        *int
	OutputRef       string
	ErrorMessage    string
	ContainerID     string
	CancelReason    string
}