package run

import (
	"context"
)

// RunRepository defines the persistence interface for runs.
type RunRepository interface {
	Save(ctx context.Context, rec *RunRecord) error
	GetByID(ctx context.Context, id string) (*RunRecord, error)
	ListByAgentID(ctx context.Context, agentID string) ([]RunRecord, error)
	DeleteByAgentID(ctx context.Context, agentID string) error
}