package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func newTestDB(t *testing.T, path string) *DB {
	t.Helper()

	db, err := NewDB(path)
	if err != nil {
		t.Fatalf("NewDB(%q) error = %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testAgentRow(id string, createdAt time.Time) *AgentRow {
	return &AgentRow{
		ID:            id,
		Name:          "Test agent " + id,
		Description:   "Test description",
		Goal:          "Test goal",
		Instructions:  "Test instructions",
		RuntimeConfig: json.RawMessage(`{"image":"localhost/horizon-sandbox:test"}`),
		ModelConfig:   json.RawMessage(`{"provider":"google","model":"gemini-3.5-flash-lite"}`),
		Capabilities:  json.RawMessage(`[]`),
		Permissions:   json.RawMessage(`{}`),
		Schedule:      json.RawMessage(`null`),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
		Version:       1,
	}
}

func migrationRecords(t *testing.T, db *DB) map[int]time.Time {
	t.Helper()

	rows, err := db.Query(`SELECT version, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query schema_migrations error = %v", err)
	}
	defer rows.Close()

	records := make(map[int]time.Time)
	for rows.Next() {
		var version int
		var appliedAt time.Time
		if err := rows.Scan(&version, &appliedAt); err != nil {
			t.Fatalf("scan migration record error = %v", err)
		}
		records[version] = appliedAt
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration records error = %v", err)
	}
	return records
}

func TestNewDBCreatesParentDirectoryAndAppliesMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "data", "horizon.db")
	db := newTestDB(t, path)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file stat error = %v", err)
	}

	for _, table := range []string{"agents", "runs", "schema_migrations"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("query %s table error = %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %q count = %d, want 1", table, count)
		}
	}

	records := migrationRecords(t, db)
	if len(records) != 2 || records[1].IsZero() || records[2].IsZero() {
		t.Errorf("migration records = %#v, want non-zero records for versions 1 and 2", records)
	}
}

func TestNewDBSupportsInMemoryDatabase(t *testing.T) {
	db := newTestDB(t, ":memory:")

	var foreignKeysEnabled int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeysEnabled); err != nil {
		t.Fatalf("PRAGMA foreign_keys error = %v", err)
	}
	if foreignKeysEnabled != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1", foreignKeysEnabled)
	}

	records := migrationRecords(t, db)
	if len(records) != 2 || records[1].IsZero() || records[2].IsZero() {
		t.Errorf("migration records = %#v, want versions 1 and 2", records)
	}
}

func TestNewDBReopenPreservesDataAndMigrationHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "horizon.db")
	now := time.Now().UTC().Truncate(time.Microsecond)

	firstDB := newTestDB(t, path)
	agentRepo := NewAgentRepository(firstDB)
	if err := agentRepo.Save(context.Background(), testAgentRow("agent-1", now)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	before := migrationRecords(t, firstDB)
	if err := firstDB.Close(); err != nil {
		t.Fatalf("firstDB.Close() error = %v", err)
	}

	secondDB, err := NewDB(path)
	if err != nil {
		t.Fatalf("reopen NewDB() error = %v", err)
	}
	defer secondDB.Close()

	after := migrationRecords(t, secondDB)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("migration records after reopen = %#v, want %#v", after, before)
	}
	stored, err := NewAgentRepository(secondDB).GetByID(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetByID() after reopen error = %v", err)
	}
	if stored.Name != "Test agent agent-1" {
		t.Errorf("stored Name = %q, want %q", stored.Name, "Test agent agent-1")
	}
}

func TestNewDBAdoptsExistingSchemaWithoutDataLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	now := time.Now().UTC().Truncate(time.Microsecond)

	legacy, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open legacy database error = %v", err)
	}
	if _, err := legacy.Exec(createAgentsTable); err != nil {
		legacy.Close()
		t.Fatalf("create legacy agents schema error = %v", err)
	}
	if _, err := legacy.Exec(createRunsTable); err != nil {
		legacy.Close()
		t.Fatalf("create legacy runs schema error = %v", err)
	}
	legacyDB := &DB{legacy}
	if err := NewAgentRepository(legacyDB).Save(context.Background(), testAgentRow("legacy-agent", now)); err != nil {
		legacy.Close()
		t.Fatalf("save legacy agent error = %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database error = %v", err)
	}

	db := newTestDB(t, path)
	records := migrationRecords(t, db)
	if len(records) != 2 || records[1].IsZero() || records[2].IsZero() {
		t.Errorf("migration records = %#v, want versions 1 and 2", records)
	}
	stored, err := NewAgentRepository(db).GetByID(context.Background(), "legacy-agent")
	if err != nil {
		t.Fatalf("GetByID() after migration adoption error = %v", err)
	}
	if stored.CreatedAt != now {
		t.Errorf("stored CreatedAt = %s, want %s", stored.CreatedAt, now)
	}
}

func TestNewDBRollsBackAllSchemaChangesWhenMigrationFails(t *testing.T) {
	originalMigrations := migrations
	migrations = append(append([]migration(nil), originalMigrations...), migration{
		version: 3,
		name:    "invalid_migration",
		sql:     "CREATE TABLE temporary_table (id INTEGER); THIS IS NOT SQL;",
	})
	t.Cleanup(func() { migrations = originalMigrations })

	path := filepath.Join(t.TempDir(), "failed-migration.db")
	if _, err := NewDB(path); err == nil {
		t.Fatal("NewDB() error = nil, want migration failure")
	}

	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open database after migration failure error = %v", err)
	}
	defer db.Close()

	for _, table := range []string{"agents", "runs", "schema_migrations", "temporary_table"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("query %s table error = %v", table, err)
		}
		if count != 0 {
			t.Errorf("table %q exists after failed migration, want rollback", table)
		}
	}
}

func TestRepositoriesPersistOrderAndCascade(t *testing.T) {
	db := newTestDB(t, filepath.Join(t.TempDir(), "horizon.db"))
	agentRepo := NewAgentRepository(db)
	runRepo := NewRunRepository(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)
	older := testAgentRow("agent-older", now)
	newer := testAgentRow("agent-newer", now.Add(time.Minute))
	if err := agentRepo.Save(ctx, older); err != nil {
		t.Fatalf("save older agent error = %v", err)
	}
	if err := agentRepo.Save(ctx, newer); err != nil {
		t.Fatalf("save newer agent error = %v", err)
	}

	agents, err := agentRepo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(agents) != 2 || agents[0].ID != newer.ID || agents[1].ID != older.ID {
		t.Errorf("List() IDs = %v, want [%s %s]", agentIDs(agents), newer.ID, older.ID)
	}

	completedAt := now.Add(30 * time.Second)
	run := &RunRow{
		ID:           "run-1",
		AgentID:      newer.ID,
		AgentVersion: newer.Version,
		TriggerType:  "manual",
		Status:       "succeeded",
		StartedAt:    now,
		CompletedAt:  sql.NullTime{Time: completedAt, Valid: true},
		ExitCode:     sql.NullInt64{Int64: 0, Valid: true},
		OutputRef:    sql.NullString{String: "outputs/run-1.log", Valid: true},
	}
	if err := runRepo.Save(ctx, run); err != nil {
		t.Fatalf("runRepo.Save() error = %v", err)
	}

	storedRun, err := runRepo.GetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("runRepo.GetByID() error = %v", err)
	}
	if !storedRun.CompletedAt.Valid || !storedRun.CompletedAt.Time.Equal(completedAt) {
		t.Errorf("stored CompletedAt = %#v, want %s", storedRun.CompletedAt, completedAt)
	}
	if !storedRun.ExitCode.Valid || storedRun.ExitCode.Int64 != 0 {
		t.Errorf("stored ExitCode = %#v, want 0", storedRun.ExitCode)
	}
	if !storedRun.OutputRef.Valid || storedRun.OutputRef.String != "outputs/run-1.log" {
		t.Errorf("stored OutputRef = %#v, want outputs/run-1.log", storedRun.OutputRef)
	}

	if err := agentRepo.Delete(ctx, newer.ID); err != nil {
		t.Fatalf("agentRepo.Delete() error = %v", err)
	}
	if _, err := runRepo.GetByID(ctx, run.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("runRepo.GetByID() after agent delete error = %v, want ErrNotFound", err)
	}
	if _, err := agentRepo.GetByID(ctx, "missing-agent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("agentRepo.GetByID() missing error = %v, want ErrNotFound", err)
	}
	if _, err := runRepo.GetByID(ctx, "missing-run"); !errors.Is(err, ErrNotFound) {
		t.Errorf("runRepo.GetByID() missing error = %v, want ErrNotFound", err)
	}
	if err := agentRepo.Delete(ctx, "missing-agent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("agentRepo.Delete() missing error = %v, want ErrNotFound", err)
	}
}

func agentIDs(rows []*AgentRow) []string {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}
