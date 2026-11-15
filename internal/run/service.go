package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jain-aadi/horizon/internal/sandbox"
	"github.com/jain-aadi/horizon/internal/storage"
)

const (
	defaultAgentTimeout = 10 * time.Minute
	defaultMaxOutput    = 4 << 20
)

var (
	// ErrRunNotCancellable is returned for a terminal run or one that is not active on this process.
	ErrRunNotCancellable = errors.New("run is not cancellable")
	// ErrOutputUnavailable is returned when a run has no readable local output artifact.
	ErrOutputUnavailable = errors.New("run output is unavailable")
	// ErrInvalidAgentID is returned when a run cannot be associated with an agent.
	ErrInvalidAgentID = errors.New("agent ID is required")
	// ErrAgentNotFound is returned when a requested run agent does not exist.
	ErrAgentNotFound = errors.New("agent not found")
)

// Executor is the execution boundary used to run and stop one materialized Docker Agent run.
type Executor interface {
	RunAgent(ctx context.Context, opts sandbox.RunOptions) (*sandbox.RunResult, error)
	RemoveContainer(containerID string) error
}

// Config configures local run artifacts and execution limits.
type Config struct {
	DataDir        string
	AgentTimeout   time.Duration
	MaxOutputBytes int64
	APIKey         string
	LookupAgent    AgentLookup
	Materialize    ConfigMaterializer
}

// AgentSnapshot is the immutable agent identity and definition captured for one execution.
// Definition is intentionally opaque to keep the run lifecycle independent from the agent model.
type AgentSnapshot struct {
	ID         string
	Version    int
	Definition any
	Execution  sandbox.ExecutionConfig
}

// AgentLookup loads the agent definition that a run will snapshot before launch.
type AgentLookup func(ctx context.Context, agentID string) (*AgentSnapshot, error)

// ConfigMaterializer converts a captured definition into pinned Docker Agent YAML.
type ConfigMaterializer func(snapshot *AgentSnapshot) ([]byte, error)

type activeRun struct {
	cancel      context.CancelFunc
	containerID string
	cancelled   bool
}

// Service persists, materializes, executes, and cancels agent runs.
type Service struct {
	runs     RunRepository
	executor Executor
	config   Config

	mu     sync.Mutex
	active map[string]*activeRun
}

// NewService creates a run service with conservative local artifact defaults.
func NewService(runs RunRepository, executor Executor, config Config) *Service {
	if config.DataDir == "" {
		config.DataDir = "."
	}
	if config.AgentTimeout <= 0 {
		config.AgentTimeout = defaultAgentTimeout
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = defaultMaxOutput
	}
	return &Service{
		runs:     runs,
		executor: executor,
		config:   config,
		active:   make(map[string]*activeRun),
	}
}

// StartRun records a queued execution snapshot and starts background materialization and execution.
func (s *Service) StartRun(ctx context.Context, agentID string, triggerType string, prompt string) (*RunRecord, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, ErrInvalidAgentID
	}
	if s.executor == nil {
		return nil, errors.New("run executor is not configured")
	}
	if s.config.LookupAgent == nil || s.config.Materialize == nil {
		return nil, errors.New("run materialization is not configured")
	}

	snapshot, err := s.config.LookupAgent(ctx, agentID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("%w: %v", ErrAgentNotFound, err)
		}
		return nil, err
	}
	if snapshot == nil || snapshot.ID == "" || snapshot.Version < 1 {
		return nil, errors.New("invalid agent snapshot")
	}
	if triggerType == "" {
		triggerType = "manual"
	}

	record := &RunRecord{
		ID:           uuid.NewString(),
		AgentID:      snapshot.ID,
		AgentVersion: snapshot.Version,
		TriggerType:  triggerType,
		Status:       RunStatusQueued,
		StartedAt:    time.Now().UTC(),
	}
	if err := s.runs.Save(ctx, record); err != nil {
		return nil, err
	}

	executionCtx, cancel := context.WithTimeout(context.Background(), s.config.AgentTimeout)
	s.mu.Lock()
	s.active[record.ID] = &activeRun{cancel: cancel}
	s.mu.Unlock()

	go s.execute(executionCtx, record.ID, snapshot, prompt)
	return record, nil
}

func (s *Service) execute(ctx context.Context, runID string, snapshot *AgentSnapshot, prompt string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.finish(runID, RunStatusFailed, nil, fmt.Errorf("run worker panic: %v", recovered))
		}
	}()

	if s.isCancelled(runID) {
		s.removeActive(runID)
		return
	}

	yamlPath, outputRef, outputFile, err := s.materializeRun(runID, snapshot)
	if err != nil {
		s.finish(runID, RunStatusFailed, nil, err)
		return
	}
	defer outputFile.Close()

	if !s.setOutputRef(runID, outputRef) {
		s.finish(runID, RunStatusFailed, nil, errors.New("persist run output reference"))
		return
	}
	if !s.markRunning(runID) {
		s.finish(runID, RunStatusFailed, nil, errors.New("transition run to running"))
		return
	}

	writer := &boundedWriter{writer: outputFile, remaining: s.config.MaxOutputBytes}
	result, err := s.executor.RunAgent(ctx, sandbox.RunOptions{
		WorkspaceDir: filepath.Dir(yamlPath),
		YAMLFile:     filepath.Base(yamlPath),
		Prompt:       prompt,
		APIKey:       s.config.APIKey,
		OutputWriter: writer,
		Execution:    snapshot.Execution,
		OnContainerCreated: func(containerID string) {
			s.recordContainerID(runID, containerID)
		},
	})
	if result != nil && result.Error != nil && err == nil {
		err = result.Error
	}
	if result != nil && result.ExitCode != 0 && err == nil {
		err = fmt.Errorf("agent container exited with code %d", result.ExitCode)
	}

	if s.isCancelled(runID) {
		s.removeActive(runID)
		return
	}
	if err != nil {
		if isTimeoutError(err) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.finish(runID, RunStatusTimedOut, result, err)
			return
		}
		s.finish(runID, RunStatusFailed, result, err)
		return
	}
	s.finish(runID, RunStatusSucceeded, result, nil)
}

func (s *Service) materializeRun(runID string, snapshot *AgentSnapshot) (string, string, *os.File, error) {
	yamlData, err := s.config.Materialize(snapshot)
	if err != nil {
		return "", "", nil, fmt.Errorf("materialize Docker Agent configuration: %w", err)
	}

	runDir := filepath.Join(s.config.DataDir, "runs", runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return "", "", nil, fmt.Errorf("create run workspace: %w", err)
	}
	yamlPath := filepath.Join(runDir, "agent.yaml")
	if err := os.WriteFile(yamlPath, yamlData, 0o600); err != nil {
		return "", "", nil, fmt.Errorf("write Docker Agent configuration: %w", err)
	}
	outputPath := filepath.Join(runDir, "output.log")
	outputFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", "", nil, fmt.Errorf("create run output file: %w", err)
	}
	return yamlPath, filepath.ToSlash(filepath.Join("runs", runID, "output.log")), outputFile, nil
}

func (s *Service) setOutputRef(runID string, outputRef string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	active, ok := s.active[runID]
	if !ok || active.cancelled {
		return false
	}
	record, err := s.runs.GetByID(context.Background(), runID)
	if err != nil || record.Status != RunStatusQueued {
		return false
	}
	record.OutputRef = outputRef
	return s.runs.Save(context.Background(), record) == nil
}

func (s *Service) markRunning(runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	active, ok := s.active[runID]
	if !ok || active.cancelled {
		return false
	}
	record, err := s.runs.GetByID(context.Background(), runID)
	if err != nil || record.Status != RunStatusQueued {
		return false
	}
	record.Status = RunStatusRunning
	return s.runs.Save(context.Background(), record) == nil
}

func (s *Service) recordContainerID(runID string, containerID string) {
	if containerID == "" {
		return
	}

	var remove bool
	s.mu.Lock()
	active, ok := s.active[runID]
	if !ok {
		s.mu.Unlock()
		return
	}
	active.containerID = containerID
	if active.cancelled {
		remove = true
	} else if record, err := s.runs.GetByID(context.Background(), runID); err == nil && !IsTerminalStatus(record.Status) {
		record.ContainerID = containerID
		_ = s.runs.Save(context.Background(), record)
	}
	s.mu.Unlock()

	if remove {
		_ = s.executor.RemoveContainer(containerID)
	}
}

func (s *Service) finish(runID string, status RunStatus, result *sandbox.RunResult, runErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	active, ok := s.active[runID]
	if !ok || active.cancelled {
		delete(s.active, runID)
		return
	}
	record, err := s.runs.GetByID(context.Background(), runID)
	if err != nil || IsTerminalStatus(record.Status) {
		delete(s.active, runID)
		return
	}

	now := time.Now().UTC()
	record.Status = status
	record.CompletedAt = &now
	if result != nil {
		if result.ContainerID != "" {
			record.ContainerID = result.ContainerID
		}
		exitCode := result.ExitCode
		record.ExitCode = &exitCode
	}
	if runErr != nil {
		record.ErrorMessage = runErr.Error()
	}
	_ = s.runs.Save(context.Background(), record)
	delete(s.active, runID)
}

// CancelRun makes cancellation durable, cancels the run context, and requests container removal when known.
func (s *Service) CancelRun(_ context.Context, runID string, reason string) error {
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()

	s.mu.Lock()
	active, ok := s.active[runID]
	if !ok || active.cancelled {
		s.mu.Unlock()
		if _, err := s.runs.GetByID(persistCtx, runID); err != nil {
			return err
		}
		return ErrRunNotCancellable
	}

	record, err := s.runs.GetByID(persistCtx, runID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if IsTerminalStatus(record.Status) {
		s.mu.Unlock()
		return ErrRunNotCancellable
	}

	now := time.Now().UTC()
	record.Status = RunStatusCancelled
	record.CompletedAt = &now
	record.CancelReason = strings.TrimSpace(reason)
	if record.CancelReason == "" {
		record.CancelReason = "cancelled"
	}
	if err := s.runs.Save(persistCtx, record); err != nil {
		s.mu.Unlock()
		return err
	}

	active.cancelled = true
	containerID := active.containerID
	cancel := active.cancel
	s.mu.Unlock()

	cancel()
	if containerID != "" {
		_ = s.executor.RemoveContainer(containerID)
	}
	return nil
}

// GetRun returns the persisted run record.
func (s *Service) GetRun(ctx context.Context, runID string) (*RunRecord, error) {
	return s.runs.GetByID(ctx, runID)
}

// GetRunStatus returns the current persisted lifecycle status.
func (s *Service) GetRunStatus(ctx context.Context, runID string) (RunStatus, error) {
	record, err := s.GetRun(ctx, runID)
	if err != nil {
		return "", err
	}
	return record.Status, nil
}

// ReadOutput reads a run output artifact only after validating that its persisted reference is contained in DataDir.
func (s *Service) ReadOutput(ctx context.Context, runID string) ([]byte, error) {
	record, err := s.runs.GetByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if record.OutputRef == "" {
		return nil, ErrOutputUnavailable
	}
	outputPath, err := s.resolveOutputPath(runID, record.OutputRef)
	if err != nil {
		return nil, err
	}
	output, err := os.ReadFile(outputPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrOutputUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("read run output: %w", err)
	}
	return output, nil
}

func (s *Service) resolveOutputPath(runID string, outputRef string) (string, error) {
	expectedRef := filepath.ToSlash(filepath.Join("runs", runID, "output.log"))
	if outputRef != expectedRef || filepath.IsAbs(outputRef) {
		return "", ErrOutputUnavailable
	}
	base, err := filepath.Abs(s.config.DataDir)
	if err != nil {
		return "", fmt.Errorf("resolve output root: %w", err)
	}
	outputPath := filepath.Join(base, filepath.Clean(outputRef))
	relative, err := filepath.Rel(base, outputPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrOutputUnavailable
	}
	return outputPath, nil
}

func (s *Service) isCancelled(runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, ok := s.active[runID]
	return !ok || active.cancelled
}

func (s *Service) removeActive(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, runID)
}

func isTimeoutError(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout")
}

type boundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	originalLength := len(p)
	if w.remaining <= 0 {
		return originalLength, nil
	}
	if int64(len(p)) > w.remaining {
		p = p[:w.remaining]
	}
	written, err := w.writer.Write(p)
	w.remaining -= int64(written)
	if err != nil {
		return written, err
	}
	return originalLength, nil
}

var _ RunCoordinator = (*Service)(nil)
