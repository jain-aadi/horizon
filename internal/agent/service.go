package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jain-aadi/horizon/internal/run"
	"github.com/jain-aadi/horizon/internal/storage"
)

var (
	ErrInvalidAgent = errors.New("invalid agent definition")
)

// Service handles agent lifecycle operations.
type Service struct {
	repo         *storage.AgentRepository
	runRepo      *storage.RunRepository
	defaultImage string
}

// NewService creates a new agent service.
func NewService(repo *storage.AgentRepository, runRepo *storage.RunRepository, defaultImage string) *Service {
	return &Service{repo: repo, runRepo: runRepo, defaultImage: defaultImage}
}

// CreateAgent saves a new agent definition.
func (s *Service) CreateAgent(ctx context.Context, def *AgentDefinition) error {
	if err := s.prepareDefinition(def); err != nil {
		return err
	}

	def.ID = uuid.NewString()
	now := time.Now().UTC()
	def.CreatedAt = now
	def.UpdatedAt = now
	def.Version = 1
	row, err := def.ToStorageRow()
	if err != nil {
		return err
	}
	return s.repo.Save(ctx, row)
}

// GetAgent retrieves an agent by ID.
func (s *Service) GetAgent(ctx context.Context, id string) (*AgentDefinition, error) {
	row, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return storageRowToAgent(row), nil
}

// ListAgents returns all saved agents.
func (s *Service) ListAgents(ctx context.Context) ([]*AgentDefinition, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	agents := make([]*AgentDefinition, len(rows))
	for i, row := range rows {
		agents[i] = storageRowToAgent(row)
	}
	return agents, nil
}

// UpdateAgent updates an existing agent definition.
func (s *Service) UpdateAgent(ctx context.Context, def *AgentDefinition) error {
	if def == nil || def.ID == "" {
		return ErrInvalidAgent
	}
	if err := s.prepareDefinition(def); err != nil {
		return err
	}

	existing, err := s.repo.GetByID(ctx, def.ID)
	if err != nil {
		return err
	}
	def.CreatedAt = existing.CreatedAt
	def.UpdatedAt = time.Now().UTC()
	def.Version = existing.Version + 1
	row, err := def.ToStorageRow()
	if err != nil {
		return err
	}
	return s.repo.Save(ctx, row)
}

// DeleteAgent removes an agent and its run history.
func (s *Service) DeleteAgent(ctx context.Context, id string) error {
	if err := s.runRepo.DeleteByAgentID(ctx, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// GetAgentWithRuns returns an agent with its run history.
func (s *Service) GetAgentWithRuns(ctx context.Context, id string) (*AgentDefinition, error) {
	row, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	runRows, err := s.runRepo.ListByAgentID(ctx, id)
	if err != nil {
		return nil, err
	}
	agent := storageRowToAgent(row)
	agent.RunHistory = make([]run.RunRecord, len(runRows))
	for i, rr := range runRows {
		agent.RunHistory[i] = storageRunRowToRun(rr)
	}
	return agent, nil
}

func (s *Service) prepareDefinition(def *AgentDefinition) error {
	if def == nil {
		return ErrInvalidAgent
	}
	def.ApplyDefaults(s.defaultImage)
	return def.Validate()
}

// storageRowToAgent converts storage.AgentRow to AgentDefinition.
func storageRowToAgent(row *storage.AgentRow) *AgentDefinition {
	def := &AgentDefinition{
		ID:           row.ID,
		Name:         row.Name,
		Description:  row.Description,
		Goal:         row.Goal,
		Instructions: row.Instructions,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		Version:      row.Version,
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

// storageRunRowToRun converts storage.RunRow to run.RunRecord.
func storageRunRowToRun(row *storage.RunRow) run.RunRecord {
	rec := run.RunRecord{
		ID:           row.ID,
		AgentID:      row.AgentID,
		AgentVersion: row.AgentVersion,
		TriggerType:  row.TriggerType,
		Status:       run.RunStatus(row.Status),
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
