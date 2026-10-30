package run

import (
	"time"
)

// RunStatus represents the lifecycle state of a run.
type RunStatus string

const (
	RunStatusQueued     RunStatus = "queued"
	RunStatusRunning    RunStatus = "running"
	RunStatusSucceeded  RunStatus = "succeeded"
	RunStatusFailed     RunStatus = "failed"
	RunStatusCancelled  RunStatus = "cancelled"
	RunStatusTimedOut   RunStatus = "timed_out"
)

// RunRecord represents one execution instance of an agent.
type RunRecord struct {
	ID              string    `json:"id"`
	AgentID         string    `json:"agent_id"`
	AgentVersion    int       `json:"agent_version"`
	TriggerType     string    `json:"trigger_type"` // "manual" | "scheduled"
	Status          RunStatus `json:"status"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ExitCode        *int      `json:"exit_code,omitempty"`
	OutputRef       string    `json:"output_ref,omitempty"` // path or reference to captured output
	ErrorMessage    string    `json:"error_message,omitempty"`
	ContainerID     string    `json:"container_id,omitempty"`
	CancelReason    string    `json:"cancel_reason,omitempty"`
}