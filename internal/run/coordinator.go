package run

import (
	"context"
)

// RunCoordinator handles run execution coordination.
type RunCoordinator interface {
	StartRun(ctx context.Context, agentID string, triggerType string, prompt string) (*RunRecord, error)
	CancelRun(ctx context.Context, runID string, reason string) error
	GetRunStatus(ctx context.Context, runID string) (RunStatus, error)
	GetRun(ctx context.Context, runID string) (*RunRecord, error)
	ReadOutput(ctx context.Context, runID string) ([]byte, error)
}
