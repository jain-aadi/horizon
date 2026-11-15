package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("record not found")
)

//go:embed migrations/001_create_agents.sql
var createAgentsTable string

//go:embed migrations/002_create_runs.sql
var createRunsTable string

type migration struct {
	version int
	name    string
	sql     string
}

var migrations = []migration{
	{version: 1, name: "create_agents", sql: createAgentsTable},
	{version: 2, name: "create_runs", sql: createRunsTable},
}

const createSchemaMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at DATETIME NOT NULL
);
`

// DB wraps the database connection and provides migration support.
type DB struct {
	*sql.DB
}

// NewDB opens a SQLite database and runs migrations.
func NewDB(path string) (*DB, error) {
	if err := ensureDatabaseDirectory(path); err != nil {
		return nil, fmt.Errorf("prepare database directory: %w", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	d := &DB{db}
	if err := d.runMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return d, nil
}

func ensureDatabaseDirectory(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("database path is required")
	}
	if path == ":memory:" || strings.Contains(strings.ToLower(path), "mode=memory") || strings.HasPrefix(path, "file:") {
		return nil
	}

	directory := filepath.Dir(path)
	if directory == "." || directory == "" {
		return nil
	}
	return os.MkdirAll(directory, 0o700)
}

func sqliteDSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_foreign_keys=on&_busy_timeout=5000"
}

// runMigrations applies each embedded migration exactly once in version order.
func (d *DB) runMigrations() error {
	if err := validateMigrations(migrations); err != nil {
		return err
	}

	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(createSchemaMigrationsTable); err != nil {
		return fmt.Errorf("create schema migration table: %w", err)
	}

	for _, migration := range migrations {
		var appliedVersion int
		err := tx.QueryRow(`SELECT version FROM schema_migrations WHERE version = ?`, migration.version).Scan(&appliedVersion)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(migration.sql); err != nil {
				return fmt.Errorf("apply migration %d (%s): %w", migration.version, migration.name, err)
			}
			if _, err := tx.Exec(
				`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				migration.version,
				migration.name,
				time.Now().UTC(),
			); err != nil {
				return fmt.Errorf("record migration %d (%s): %w", migration.version, migration.name, err)
			}
		case err != nil:
			return fmt.Errorf("check migration %d (%s): %w", migration.version, migration.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func validateMigrations(migrations []migration) error {
	previousVersion := 0
	for _, migration := range migrations {
		if migration.version <= previousVersion {
			return fmt.Errorf("migration versions must be strictly increasing: %d follows %d", migration.version, previousVersion)
		}
		if strings.TrimSpace(migration.name) == "" {
			return fmt.Errorf("migration %d has an empty name", migration.version)
		}
		if strings.TrimSpace(migration.sql) == "" {
			return fmt.Errorf("migration %d (%s) is empty", migration.version, migration.name)
		}
		previousVersion = migration.version
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
