package run

import (
	"database/sql"
	"time"

	"github.com/jain-aadi/horizon/internal/storage"
)

// RunStatus represents the lifecycle state of a run.
type RunStatus string

const (
	RunStatusQueued    RunStatus = "queued"
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
	RunStatusTimedOut  RunStatus = "timed_out"
)

// RunRecord represents one execution instance of an agent.
type RunRecord struct {
	ID           string     `json:"id"`
	AgentID      string     `json:"agent_id"`
	AgentVersion int        `json:"agent_version"`
	TriggerType  string     `json:"trigger_type"` // "manual" | "scheduled"
	Status       RunStatus  `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	ExitCode     *int       `json:"exit_code,omitempty"`
	OutputRef    string     `json:"output_ref,omitempty"` // path or reference to captured output
	ErrorMessage string     `json:"error_message,omitempty"`
	ContainerID  string     `json:"container_id,omitempty"`
	CancelReason string     `json:"cancel_reason,omitempty"`
}

// IsTerminalStatus reports whether a run has reached a terminal lifecycle state.
func IsTerminalStatus(status RunStatus) bool {
	switch status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled, RunStatusTimedOut:
		return true
	default:
		return false
	}
}

// FromStorageRow converts a storage record to the run domain model.
func FromStorageRow(row *storage.RunRow) *RunRecord {
	record := &RunRecord{
		ID:           row.ID,
		AgentID:      row.AgentID,
		AgentVersion: row.AgentVersion,
		TriggerType:  row.TriggerType,
		Status:       RunStatus(row.Status),
		StartedAt:    row.StartedAt,
	}
	if row.CompletedAt.Valid {
		completedAt := row.CompletedAt.Time
		record.CompletedAt = &completedAt
	}
	if row.ExitCode.Valid {
		exitCode := int(row.ExitCode.Int64)
		record.ExitCode = &exitCode
	}
	if row.OutputRef.Valid {
		record.OutputRef = row.OutputRef.String
	}
	if row.ErrorMessage.Valid {
		record.ErrorMessage = row.ErrorMessage.String
	}
	if row.ContainerID.Valid {
		record.ContainerID = row.ContainerID.String
	}
	if row.CancelReason.Valid {
		record.CancelReason = row.CancelReason.String
	}
	return record
}

// ToStorageRow converts the run domain model to the storage representation.
func (r *RunRecord) ToStorageRow() *storage.RunRow {
	row := &storage.RunRow{
		ID:           r.ID,
		AgentID:      r.AgentID,
		AgentVersion: r.AgentVersion,
		TriggerType:  r.TriggerType,
		Status:       string(r.Status),
		StartedAt:    r.StartedAt,
	}
	if r.CompletedAt != nil {
		row.CompletedAt = sql.NullTime{Time: *r.CompletedAt, Valid: true}
	}
	if r.ExitCode != nil {
		row.ExitCode = sql.NullInt64{Int64: int64(*r.ExitCode), Valid: true}
	}
	if r.OutputRef != "" {
		row.OutputRef = sql.NullString{String: r.OutputRef, Valid: true}
	}
	if r.ErrorMessage != "" {
		row.ErrorMessage = sql.NullString{String: r.ErrorMessage, Valid: true}
	}
	if r.ContainerID != "" {
		row.ContainerID = sql.NullString{String: r.ContainerID, Valid: true}
	}
	if r.CancelReason != "" {
		row.CancelReason = sql.NullString{String: r.CancelReason, Valid: true}
	}
	return row
}
