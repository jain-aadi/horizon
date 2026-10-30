package run

import (
	"context"
)

// RunCoordinator handles run execution coordination.
type RunCoordinator interface {
	ExecuteRun(ctx context.Context, agentID string, triggerType string) (*RunRecord, error)
	CancelRun(ctx context.Context, runID string, reason string) error
	GetRunStatus(ctx context.Context, runID string) (RunStatus, error)
}