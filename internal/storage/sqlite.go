package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("record not found")
)

// DB wraps the database connection and provides migration support.
type DB struct {
	*sql.DB
}

// NewDB opens a SQLite database and runs migrations.
func NewDB(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	d := &DB{db}
	if err := d.runMigrations(); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return d, nil
}

// runMigrations executes all schema migrations in order.
func (d *DB) runMigrations() error {
	migrations := []string{
		createAgentsTable,
		createRunsTable,
	}

	for i, migration := range migrations {
		if _, err := d.Exec(migration); err != nil {
			return fmt.Errorf("migration %d failed: %w", i+1, err)
		}
	}
	return nil
}

// AgentRepository handles agent persistence.
type AgentRepository struct {
	db *DB
}

// NewAgentRepository creates a new agent repository.
func NewAgentRepository(db *DB) *AgentRepository {
	return &AgentRepository{db: db}
}

// AgentRow represents a row in the agents table.
type AgentRow struct {
	ID            string
	Name          string
	Description   string
	Goal          string
	Instructions  string
	RuntimeConfig json.RawMessage
	ModelConfig   json.RawMessage
	Capabilities  json.RawMessage
	Permissions   json.RawMessage
	Schedule      json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Version       int
}

// Save inserts or updates an agent definition.
func (r *AgentRepository) Save(ctx context.Context, row *AgentRow) error {
	query := `
		INSERT INTO agents (id, name, description, goal, instructions, runtime_config, model_config, capabilities, permissions, schedule, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			description=excluded.description,
			goal=excluded.goal,
			instructions=excluded.instructions,
			runtime_config=excluded.runtime_config,
			model_config=excluded.model_config,
			capabilities=excluded.capabilities,
			permissions=excluded.permissions,
			schedule=excluded.schedule,
			updated_at=excluded.updated_at,
			version=excluded.version
	`
	_, err := r.db.ExecContext(ctx, query,
		row.ID, row.Name, row.Description, row.Goal, row.Instructions,
		row.RuntimeConfig, row.ModelConfig, row.Capabilities, row.Permissions,
		row.Schedule, row.CreatedAt, row.UpdatedAt, row.Version,
	)
	return err
}

// GetByID retrieves an agent by ID.
func (r *AgentRepository) GetByID(ctx context.Context, id string) (*AgentRow, error) {
	query := `SELECT id, name, description, goal, instructions, runtime_config, model_config, capabilities, permissions, schedule, created_at, updated_at, version FROM agents WHERE id = ?`
	row := r.db.QueryRowContext(ctx, query, id)
	return r.scanAgent(row)
}

// List returns all agents.
func (r *AgentRepository) List(ctx context.Context) ([]*AgentRow, error) {
	query := `SELECT id, name, description, goal, instructions, runtime_config, model_config, capabilities, permissions, schedule, created_at, updated_at, version FROM agents ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []*AgentRow
	for rows.Next() {
		a, err := r.scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// Delete removes an agent.
func (r *AgentRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// scanAgent scans an agent from a row.
func (r *AgentRepository) scanAgent(scanner interface {
	Scan(dest ...any) error
}) (*AgentRow, error) {
	var row AgentRow
	err := scanner.Scan(
		&row.ID, &row.Name, &row.Description, &row.Goal, &row.Instructions,
		&row.RuntimeConfig, &row.ModelConfig, &row.Capabilities, &row.Permissions, &row.Schedule,
		&row.CreatedAt, &row.UpdatedAt, &row.Version,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

type RunRepository struct {
	db *DB
}

// NewRunRepository creates a new run repository.
func NewRunRepository(db *DB) *RunRepository {
	return &RunRepository{db: db}
}

// RunRow represents a row in the runs table.
type RunRow struct {
	ID           string
	AgentID      string
	AgentVersion int
	TriggerType  string
	Status       string
	StartedAt    time.Time
	CompletedAt  sql.NullTime
	ExitCode     sql.NullInt64
	OutputRef    sql.NullString
	ErrorMessage sql.NullString
	ContainerID  sql.NullString
	CancelReason sql.NullString
}

// Save inserts a run record.
func (r *RunRepository) Save(ctx context.Context, row *RunRow) error {
	query := `
		INSERT INTO runs (id, agent_id, agent_version, trigger_type, status, started_at, completed_at, exit_code, output_ref, error_message, container_id, cancel_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status,
			completed_at=excluded.completed_at,
			exit_code=excluded.exit_code,
			output_ref=excluded.output_ref,
			error_message=excluded.error_message,
			container_id=excluded.container_id,
			cancel_reason=excluded.cancel_reason
	`
	_, err := r.db.ExecContext(ctx, query,
		row.ID, row.AgentID, row.AgentVersion, row.TriggerType, row.Status,
		row.StartedAt, row.CompletedAt, row.ExitCode, row.OutputRef,
		row.ErrorMessage, row.ContainerID, row.CancelReason,
	)
	return err
}

// GetByID retrieves a run by ID.
func (r *RunRepository) GetByID(ctx context.Context, id string) (*RunRow, error) {
	query := `SELECT id, agent_id, agent_version, trigger_type, status, started_at, completed_at, exit_code, output_ref, error_message, container_id, cancel_reason FROM runs WHERE id = ?`
	row := r.db.QueryRowContext(ctx, query, id)
	return r.scanRun(row)
}

// ListByAgentID returns runs for an agent.
func (r *RunRepository) ListByAgentID(ctx context.Context, agentID string) ([]*RunRow, error) {
	query := `SELECT id, agent_id, agent_version, trigger_type, status, started_at, completed_at, exit_code, output_ref, error_message, container_id, cancel_reason FROM runs WHERE agent_id = ? ORDER BY started_at DESC`
	rows, err := r.db.QueryContext(ctx, query, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*RunRow
	for rows.Next() {
		rec, err := r.scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, rec)
	}
	return runs, rows.Err()
}

// DeleteByAgentID removes all runs for an agent.
func (r *RunRepository) DeleteByAgentID(ctx context.Context, agentID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM runs WHERE agent_id = ?`, agentID)
	return err
}

// scanRun scans a run record from a row.
func (r *RunRepository) scanRun(scanner interface {
	Scan(dest ...any) error
}) (*RunRow, error) {
	var row RunRow
	err := scanner.Scan(
		&row.ID, &row.AgentID, &row.AgentVersion, &row.TriggerType, &row.Status,
		&row.StartedAt, &row.CompletedAt, &row.ExitCode, &row.OutputRef, &row.ErrorMessage, &row.ContainerID, &row.CancelReason,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

// AgentDefinition represents an agent in the storage layer.
type AgentDefinition struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Goal          string          `json:"goal"`
	Instructions  string          `json:"instructions"`
	RuntimeConfig json.RawMessage `json:"runtime_config"`
	ModelConfig   json.RawMessage `json:"model_config"`
	Capabilities  json.RawMessage `json:"capabilities"`
	Permissions   json.RawMessage `json:"permissions"`
	Schedule      *ScheduleConfig `json:"schedule,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Version       int             `json:"version"`
}

// ScheduleConfig represents a schedule in the storage layer.
type ScheduleConfig struct {
	CronExpression string `json:"cron_expression"`
	Timezone       string `json:"timezone"`
	Enabled        bool   `json:"enabled"`
}

// ToAgentDomain converts AgentRow to agent.AgentDefinition.
func (row *AgentRow) ToAgentDomain() *AgentDefinition {
	def := &AgentDefinition{
		ID:            row.ID,
		Name:          row.Name,
		Description:   row.Description,
		Goal:          row.Goal,
		Instructions:  row.Instructions,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		Version:       row.Version,
	}
	json.Unmarshal(row.RuntimeConfig, &def.RuntimeConfig)
	json.Unmarshal(row.ModelConfig, &def.ModelConfig)
	json.Unmarshal(row.Capabilities, &def.Capabilities)
	json.Unmarshal(row.Permissions, &def.Permissions)
	if len(row.Schedule) > 0 && string(row.Schedule) != "null" {
		var sched ScheduleConfig
		json.Unmarshal(row.Schedule, &sched)
		def.Schedule = &sched
	}
	return def
}

// RunRecord represents a run in the storage layer.
type RunRecord struct {
	ID           string
	AgentID      string
	AgentVersion int
	TriggerType  string
	Status       string
	StartedAt    time.Time
	CompletedAt  *time.Time
	ExitCode     *int
	OutputRef    string
	ErrorMessage string
	ContainerID  string
	CancelReason string
}

// ToRunDomain converts RunRow to run.RunRecord.
func (row *RunRow) ToRunDomain() RunRecord {
	rec := RunRecord{
		ID:           row.ID,
		AgentID:      row.AgentID,
		AgentVersion: row.AgentVersion,
		TriggerType:  row.TriggerType,
		Status:       row.Status,
		StartedAt:    row.StartedAt,
	}
	if row.CompletedAt.Valid {
		rec.CompletedAt = &row.CompletedAt.Time
	}
	if row.ExitCode.Valid {
		val := int(row.ExitCode.Int64)
		rec.ExitCode = &val
	}
	if row.OutputRef.Valid {
		rec.OutputRef = row.OutputRef.String
	}
	if row.ErrorMessage.Valid {
		rec.ErrorMessage = row.ErrorMessage.String
	}
	if row.ContainerID.Valid {
		rec.ContainerID = row.ContainerID.String
	}
	if row.CancelReason.Valid {
		rec.CancelReason = row.CancelReason.String
	}
	return rec
}

// Migration SQL statements
const createAgentsTable = `
CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    goal TEXT NOT NULL,
    instructions TEXT NOT NULL,
    runtime_config TEXT NOT NULL DEFAULT '{}',
    model_config TEXT NOT NULL DEFAULT '{}',
    capabilities TEXT NOT NULL DEFAULT '[]',
    permissions TEXT NOT NULL DEFAULT '{}',
    schedule TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    version INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_agents_created_at ON agents(created_at DESC);
`

const createRunsTable = `
CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    agent_version INTEGER NOT NULL,
    trigger_type TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at DATETIME NOT NULL,
    completed_at DATETIME,
    exit_code INTEGER,
    output_ref TEXT,
    error_message TEXT,
    container_id TEXT,
    cancel_reason TEXT,
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_runs_agent_id ON runs(agent_id);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);
`