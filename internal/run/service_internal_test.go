package run

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jain-aadi/horizon/internal/sandbox"
)

func TestResolveOutputPathRejectsEscapes(t *testing.T) {
	dataDir := t.TempDir()
	service := NewService(nil, nil, Config{DataDir: dataDir})
	runID := "run-id"

	for _, outputRef := range []string{
		"../outside.log",
		filepath.Join("runs", "..", "..", "outside.log"),
		filepath.Join(dataDir, "outside.log"),
		filepath.ToSlash(filepath.Join("runs", "another-run", "output.log")),
	} {
		if _, err := service.resolveOutputPath(runID, outputRef); err == nil {
			t.Errorf("resolveOutputPath(%q) error = nil, want containment error", outputRef)
		}
	}

	if _, err := service.resolveOutputPath(runID, filepath.ToSlash(filepath.Join("runs", runID, "output.log"))); err != nil {
		t.Errorf("resolveOutputPath() valid run artifact error = %v", err)
	}
}

type failingSaveRepository struct {
	record   *RunRecord
	failSave bool
}

func (r *failingSaveRepository) Save(_ context.Context, record *RunRecord) error {
	if r.failSave {
		return errors.New("save failed")
	}
	copy := *record
	r.record = &copy
	return nil
}

func (r *failingSaveRepository) GetByID(_ context.Context, id string) (*RunRecord, error) {
	if r.record == nil || r.record.ID != id {
		return nil, errors.New("not found")
	}
	copy := *r.record
	return &copy, nil
}

func (r *failingSaveRepository) ListByAgentID(context.Context, string) ([]*RunRecord, error) {
	return nil, nil
}

func (r *failingSaveRepository) DeleteByAgentID(context.Context, string) error { return nil }

func TestCancelRunDoesNotPoisonActiveStateWhenPersistenceFails(t *testing.T) {
	repository := &failingSaveRepository{record: &RunRecord{ID: "run-id", Status: RunStatusRunning}}
	service := NewService(repository, nil, Config{DataDir: t.TempDir()})
	executionCtx, executionCancel := context.WithCancel(context.Background())
	defer executionCancel()
	service.active["run-id"] = &activeRun{cancel: executionCancel}

	repository.failSave = true
	if err := service.CancelRun(context.Background(), "run-id", "stop"); err == nil {
		t.Fatal("CancelRun() error = nil, want persistence error")
	}
	if service.active["run-id"].cancelled {
		t.Fatal("active run was marked cancelled after persistence failed")
	}
	select {
	case <-executionCtx.Done():
		t.Fatal("execution context was cancelled after persistence failed")
	default:
	}

	repository.failSave = false
	service.finish("run-id", RunStatusSucceeded, &sandbox.RunResult{ExitCode: 0}, nil)
	if repository.record.Status != RunStatusSucceeded {
		t.Errorf("record Status = %q, want %q", repository.record.Status, RunStatusSucceeded)
	}
	if _, ok := service.active["run-id"]; ok {
		t.Error("active run remained registered after terminal completion")
	}
}
