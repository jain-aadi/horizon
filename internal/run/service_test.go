package run_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jain-aadi/horizon/internal/agent"
	"github.com/jain-aadi/horizon/internal/cagent"
	run "github.com/jain-aadi/horizon/internal/run"
	"github.com/jain-aadi/horizon/internal/sandbox"
	"github.com/jain-aadi/horizon/internal/storage"
)

const runTestImage = "localhost/horizon-sandbox:test"

type fakeExecutor struct {
	runFn     func(context.Context, sandbox.RunOptions) (*sandbox.RunResult, error)
	removed   chan string
	started   chan struct{}
	startOnce sync.Once
}

func (f *fakeExecutor) RunAgent(ctx context.Context, opts sandbox.RunOptions) (*sandbox.RunResult, error) {
	f.startOnce.Do(func() { close(f.started) })
	return f.runFn(ctx, opts)
}

func (f *fakeExecutor) RemoveContainer(containerID string) error {
	f.removed <- containerID
	return nil
}

func newTestService(t *testing.T, executor run.Executor, timeout time.Duration, maxOutputBytes int64) (*run.Service, *agent.Service, string) {
	t.Helper()

	db, err := storage.NewDB(filepath.Join(t.TempDir(), "horizon.db"))
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	agentRepo := storage.NewAgentRepository(db)
	runRepo := storage.NewRunRepository(db)
	agentService := agent.NewService(agentRepo, runRepo, runTestImage)
	dataDir := t.TempDir()
	return run.NewService(
		run.NewStorageRepository(runRepo),
		executor,
		run.Config{
			DataDir:        dataDir,
			AgentTimeout:   timeout,
			MaxOutputBytes: maxOutputBytes,
			LookupAgent: func(ctx context.Context, agentID string) (*run.AgentSnapshot, error) {
				definition, err := agentService.GetAgent(ctx, agentID)
				if err != nil {
					return nil, err
				}
				return &run.AgentSnapshot{
					ID:         definition.ID,
					Version:    definition.Version,
					Definition: definition,
					Execution: sandbox.ExecutionConfig{
						Image:         definition.RuntimeConfig.Image,
						NetworkAccess: definition.Permissions.NetworkAccess,
					},
				}, nil
			},
			Materialize: func(snapshot *run.AgentSnapshot) ([]byte, error) {
				definition, ok := snapshot.Definition.(*agent.AgentDefinition)
				if !ok {
					return nil, fmt.Errorf("unexpected snapshot definition type %T", snapshot.Definition)
				}
				adapter := cagent.NewDefaultAdapter()
				config, err := adapter.BuildConfig(definition)
				if err != nil {
					return nil, err
				}
				return adapter.MarshalConfig(config)
			},
		},
	), agentService, dataDir
}

func createTestAgent(t *testing.T, service *agent.Service) *agent.AgentDefinition {
	t.Helper()

	definition := &agent.AgentDefinition{
		Name:         "Daily brief",
		Goal:         "Prepare a concise daily brief.",
		Instructions: "Return exactly three bullet points.",
		ModelConfig: agent.ModelConfig{
			Provider: "google",
			Model:    "gemini-3.5-flash-lite",
		},
		Permissions: agent.Permissions{NetworkAccess: true},
	}
	if err := service.CreateAgent(context.Background(), definition); err != nil {
		t.Fatalf("CreateAgent() error = %v", err)
	}
	return definition
}

func waitForTerminalRun(t *testing.T, service *run.Service, runID string) *run.RunRecord {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		record, err := service.GetRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("GetRun() error = %v", err)
		}
		if run.IsTerminalStatus(record.Status) {
			return record
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach a terminal state", runID)
	return nil
}

func TestServiceMaterializesConfigAndPersistsBoundedOutput(t *testing.T) {
	executor := &fakeExecutor{
		removed: make(chan string, 1),
		started: make(chan struct{}),
		runFn: func(_ context.Context, opts sandbox.RunOptions) (*sandbox.RunResult, error) {
			if opts.YAMLFile != "agent.yaml" {
				t.Errorf("YAMLFile = %q, want container-relative agent.yaml", opts.YAMLFile)
			}
			if opts.Execution.Image != runTestImage || !opts.Execution.NetworkAccess {
				t.Errorf("Execution = %#v, want saved image and approved network access", opts.Execution)
			}
			if opts.OnContainerCreated != nil {
				opts.OnContainerCreated("container-success")
			}
			_, _ = io.WriteString(opts.OutputWriter, "abcdefghij")
			return &sandbox.RunResult{ContainerID: "container-success", ExitCode: 0}, nil
		},
	}
	service, agentService, dataDir := newTestService(t, executor, time.Second, 5)
	definition := createTestAgent(t, agentService)

	created, err := service.StartRun(context.Background(), definition.ID, "manual", "Write the brief")
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if created.Status != run.RunStatusQueued {
		t.Errorf("created Status = %q, want %q", created.Status, run.RunStatusQueued)
	}
	if created.AgentVersion != definition.Version {
		t.Errorf("created AgentVersion = %d, want %d", created.AgentVersion, definition.Version)
	}

	completed := waitForTerminalRun(t, service, created.ID)
	if completed.Status != run.RunStatusSucceeded {
		t.Errorf("completed Status = %q, want %q", completed.Status, run.RunStatusSucceeded)
	}
	if completed.ContainerID != "container-success" {
		t.Errorf("completed ContainerID = %q, want %q", completed.ContainerID, "container-success")
	}
	if completed.OutputRef != filepath.ToSlash(filepath.Join("runs", created.ID, "output.log")) {
		t.Errorf("OutputRef = %q", completed.OutputRef)
	}

	output, err := service.ReadOutput(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("ReadOutput() error = %v", err)
	}
	if string(output) != "abcde" {
		t.Errorf("output = %q, want bounded output %q", output, "abcde")
	}

	config, err := osReadFile(filepath.Join(dataDir, "runs", created.ID, "agent.yaml"))
	if err != nil {
		t.Fatalf("read materialized YAML error = %v", err)
	}
	if !strings.Contains(string(config), "horizon-model") || !strings.Contains(string(config), "Daily brief") {
		t.Errorf("materialized YAML = %q, want generated Docker Agent config", config)
	}
}

func TestServiceCancellationWinsOverLateWorkerResult(t *testing.T) {
	executor := &fakeExecutor{
		removed: make(chan string, 1),
		started: make(chan struct{}),
		runFn: func(ctx context.Context, opts sandbox.RunOptions) (*sandbox.RunResult, error) {
			if opts.OnContainerCreated != nil {
				opts.OnContainerCreated("container-cancelled")
			}
			<-ctx.Done()
			return &sandbox.RunResult{ContainerID: "container-cancelled", ExitCode: 0}, nil
		},
	}
	service, agentService, _ := newTestService(t, executor, time.Second, 1024)
	definition := createTestAgent(t, agentService)

	created, err := service.StartRun(context.Background(), definition.ID, "manual", "Wait for cancellation")
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}

	if err := service.CancelRun(context.Background(), created.ID, "user requested cancellation"); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	select {
	case removedID := <-executor.removed:
		if removedID != "container-cancelled" {
			t.Errorf("RemoveContainer() ID = %q, want %q", removedID, "container-cancelled")
		}
	case <-time.After(time.Second):
		t.Fatal("CancelRun() did not request container removal")
	}

	completed := waitForTerminalRun(t, service, created.ID)
	if completed.Status != run.RunStatusCancelled {
		t.Errorf("completed Status = %q, want %q", completed.Status, run.RunStatusCancelled)
	}
	if completed.ExitCode != nil {
		t.Errorf("completed ExitCode = %v, want nil after cancellation", *completed.ExitCode)
	}
	if completed.CancelReason != "user requested cancellation" {
		t.Errorf("CancelReason = %q, want cancellation reason", completed.CancelReason)
	}
}

func TestServiceMarksDeadlineExceededAsTimedOut(t *testing.T) {
	executor := &fakeExecutor{
		removed: make(chan string, 1),
		started: make(chan struct{}),
		runFn: func(ctx context.Context, _ sandbox.RunOptions) (*sandbox.RunResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	service, agentService, _ := newTestService(t, executor, 10*time.Millisecond, 1024)
	definition := createTestAgent(t, agentService)

	created, err := service.StartRun(context.Background(), definition.ID, "manual", "Time out")
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	completed := waitForTerminalRun(t, service, created.ID)
	if completed.Status != run.RunStatusTimedOut {
		t.Errorf("completed Status = %q, want %q", completed.Status, run.RunStatusTimedOut)
	}
	if completed.ErrorMessage == "" {
		t.Error("completed ErrorMessage is empty, want timeout diagnostic")
	}
}

func osReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
