package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/horizon/horizon/internal/agent"
	"github.com/horizon/horizon/internal/run"
	"github.com/horizon/horizon/internal/sandbox"
	"github.com/horizon/horizon/internal/storage"
)

// Handler implements the HTTP API.
type Handler struct {
	agentRepo *storage.AgentRepository
	runRepo   *storage.RunRepository
	engine    *sandbox.Engine
	config    *Config
}

// Config holds handler configuration.
type Config struct {
	DefaultTimeout time.Duration
	DefaultImage   string
}

// NewHandler creates a new API handler.
func NewHandler(agentRepo *storage.AgentRepository, runRepo *storage.RunRepository, engine *sandbox.Engine, cfg *Config) *Handler {
	return &Handler{
		agentRepo: agentRepo,
		runRepo:   runRepo,
		engine:    engine,
		config:    cfg,
	}
}

// RegisterRoutes registers HTTP routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", h.handleHealth)
	mux.HandleFunc("/api/agents", h.handleAgents)
	mux.HandleFunc("/api/agents/", h.handleAgentByID)
	mux.HandleFunc("/api/runs", h.handleRuns)
	mux.HandleFunc("/api/runs/", h.handleRunByID)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := h.engine.HealthCheck(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (h *Handler) handleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listAgents(w, r)
	case http.MethodPost:
		h.createAgent(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleAgentByID(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/agents/"):]
	if id == "" {
		http.Error(w, "Agent ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getAgent(w, r, id)
	case http.MethodPut:
		h.updateAgent(w, r, id)
	case http.MethodDelete:
		h.deleteAgent(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := h.agentRepo.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	agents := make([]*agent.AgentDefinition, len(rows))
	for i, row := range rows {
		agentDef := &agent.AgentDefinition{
			ID:           row.ID,
			Name:         row.Name,
			Description:  row.Description,
			Goal:         row.Goal,
			Instructions: row.Instructions,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
			Version:      row.Version,
		}
		json.Unmarshal(row.RuntimeConfig, &agentDef.RuntimeConfig)
		json.Unmarshal(row.ModelConfig, &agentDef.ModelConfig)
		json.Unmarshal(row.Capabilities, &agentDef.Capabilities)
		json.Unmarshal(row.Permissions, &agentDef.Permissions)
		if len(row.Schedule) > 0 && string(row.Schedule) != "null" {
			var sched agent.ScheduleConfig
			json.Unmarshal(row.Schedule, &sched)
			agentDef.Schedule = &sched
		}
		agents[i] = agentDef
	}
	json.NewEncoder(w).Encode(agents)
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req agent.AgentDefinition
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		req.ID = uuid.New().String()
	}

	row := req.ToStorageRow()
	if err := h.agentRepo.Save(ctx, row); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(req)
}

func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	row, err := h.agentRepo.GetByID(ctx, id)
	if err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, "Agent not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	agentDef := &agent.AgentDefinition{
		ID:           row.ID,
		Name:         row.Name,
		Description:  row.Description,
		Goal:         row.Goal,
		Instructions: row.Instructions,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		Version:      row.Version,
	}
	json.Unmarshal(row.RuntimeConfig, &agentDef.RuntimeConfig)
	json.Unmarshal(row.ModelConfig, &agentDef.ModelConfig)
	json.Unmarshal(row.Capabilities, &agentDef.Capabilities)
	json.Unmarshal(row.Permissions, &agentDef.Permissions)
	if len(row.Schedule) > 0 && string(row.Schedule) != "null" {
		var sched agent.ScheduleConfig
		json.Unmarshal(row.Schedule, &sched)
		agentDef.Schedule = &sched
	}
	json.NewEncoder(w).Encode(agentDef)
}

func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	var req agent.AgentDefinition
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.ID = id

	existingRow, err := h.agentRepo.GetByID(ctx, id)
	if err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, "Agent not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.CreatedAt = existingRow.CreatedAt
	req.UpdatedAt = time.Now()
	req.Version = existingRow.Version + 1

	row := req.ToStorageRow()
	if err := h.agentRepo.Save(ctx, row); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(req)
}

func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	if err := h.agentRepo.Delete(ctx, id); err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, "Agent not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listRuns(w, r)
	case http.MethodPost:
		h.createRun(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleRunByID(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/runs/"):]
	if id == "" {
		http.Error(w, "Run ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getRun(w, r, id)
	case http.MethodPost:
		// Check for cancel action
		if r.URL.Query().Get("action") == "cancel" {
			h.cancelRun(w, r, id)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		http.Error(w, "agent_id query parameter required", http.StatusBadRequest)
		return
	}

	rows, err := h.runRepo.ListByAgentID(ctx, agentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	runs := make([]run.RunRecord, len(rows))
	for i, row := range rows {
		runs[i] = run.RunRecord{
			ID:           row.ID,
			AgentID:      row.AgentID,
			AgentVersion: row.AgentVersion,
			TriggerType:  row.TriggerType,
			Status:       run.RunStatus(row.Status),
			StartedAt:    row.StartedAt,
		}
		if row.CompletedAt.Valid {
			runs[i].CompletedAt = &row.CompletedAt.Time
		}
		if row.ExitCode.Valid {
			val := int(row.ExitCode.Int64)
			runs[i].ExitCode = &val
		}
		if row.OutputRef.Valid {
			runs[i].OutputRef = row.OutputRef.String
		}
		if row.ErrorMessage.Valid {
			runs[i].ErrorMessage = row.ErrorMessage.String
		}
		if row.ContainerID.Valid {
			runs[i].ContainerID = row.ContainerID.String
		}
		if row.CancelReason.Valid {
			runs[i].CancelReason = row.CancelReason.String
		}
	}
	json.NewEncoder(w).Encode(runs)
}

func (h *Handler) createRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		AgentID     string            `json:"agent_id"`
		TriggerType string            `json:"trigger_type"`
		Prompt      string            `json:"prompt"`
		Env         map[string]string `json:"env,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.AgentID == "" {
		http.Error(w, "agent_id is required", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	if req.TriggerType == "" {
		req.TriggerType = "manual"
	}

	// Get agent definition
	agentRow, err := h.agentRepo.GetByID(ctx, req.AgentID)
	if err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, "Agent not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create run record
	runID := uuid.New().String()
	runRecord := &storage.RunRow{
		ID:           runID,
		AgentID:      req.AgentID,
		AgentVersion: agentRow.Version,
		TriggerType:  req.TriggerType,
		Status:       "queued",
		StartedAt:    time.Now(),
	}

	if err := h.runRepo.Save(ctx, runRecord); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Execute agent asynchronously
	go h.executeRun(context.Background(), runRecord, agentRow, req.Prompt, req.Env)

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"run_id": runID, "status": "queued"})
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	runRow, err := h.runRepo.GetByID(ctx, id)
	if err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, "Run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	runRec := run.RunRecord{
		ID:           runRow.ID,
		AgentID:      runRow.AgentID,
		AgentVersion: runRow.AgentVersion,
		TriggerType:  runRow.TriggerType,
		Status:       run.RunStatus(runRow.Status),
		StartedAt:    runRow.StartedAt,
	}
	if runRow.CompletedAt.Valid {
		runRec.CompletedAt = &runRow.CompletedAt.Time
	}
	if runRow.ExitCode.Valid {
		val := int(runRow.ExitCode.Int64)
		runRec.ExitCode = &val
	}
	if runRow.OutputRef.Valid {
		runRec.OutputRef = runRow.OutputRef.String
	}
	if runRow.ErrorMessage.Valid {
		runRec.ErrorMessage = runRow.ErrorMessage.String
	}
	if runRow.ContainerID.Valid {
		runRec.ContainerID = runRow.ContainerID.String
	}
	if runRow.CancelReason.Valid {
		runRec.CancelReason = runRow.CancelReason.String
	}
	json.NewEncoder(w).Encode(runRec)
}

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	runRecord, err := h.runRepo.GetByID(ctx, id)
	if err != nil {
		if err == storage.ErrNotFound {
			http.Error(w, "Run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if runRecord.Status == "running" {
		runRecord.Status = "cancelled"
		now := time.Now()
		runRecord.CompletedAt = sql.NullTime{Time: now, Valid: true}
		runRecord.CancelReason = sql.NullString{String: "User cancelled", Valid: true}
		h.runRepo.Save(ctx, runRecord)
	}

	runRec := run.RunRecord{
		ID:           runRecord.ID,
		AgentID:      runRecord.AgentID,
		AgentVersion: runRecord.AgentVersion,
		TriggerType:  runRecord.TriggerType,
		Status:       run.RunStatus(runRecord.Status),
		StartedAt:    runRecord.StartedAt,
	}
	if runRecord.CompletedAt.Valid {
		runRec.CompletedAt = &runRecord.CompletedAt.Time
	}
	if runRecord.ExitCode.Valid {
		val := int(runRecord.ExitCode.Int64)
		runRec.ExitCode = &val
	}
	if runRecord.OutputRef.Valid {
		runRec.OutputRef = runRecord.OutputRef.String
	}
	if runRecord.ErrorMessage.Valid {
		runRec.ErrorMessage = runRecord.ErrorMessage.String
	}
	if runRecord.ContainerID.Valid {
		runRec.ContainerID = runRecord.ContainerID.String
	}
	if runRecord.CancelReason.Valid {
		runRec.CancelReason = runRecord.CancelReason.String
	}
	json.NewEncoder(w).Encode(runRec)
}

func (h *Handler) executeRun(ctx context.Context, runRecord *storage.RunRow, agentRow *storage.AgentRow, prompt string, env map[string]string) {
	// Update status to running
	runRecord.Status = "running"
	h.runRepo.Save(ctx, runRecord)

	// Build engine options
	opts := sandbox.RunOptions{
		WorkspaceDir: ".",          // TODO: Use proper workspace from agent config
		YAMLFile:     "agent.yaml", // TODO: Generate from agent definition
		Prompt:       prompt,
		OutputWriter: io.Discard, // discard output for now
		Env:          env,
	}

	// TODO: Get API key from secure storage
	// opts.APIKey = ...

	result, err := h.engine.RunAgent(ctx, opts)
	if err != nil {
		runRecord.Status = "failed"
		runRecord.ErrorMessage = sql.NullString{String: err.Error(), Valid: true}
	} else {
		runRecord.ContainerID = sql.NullString{String: result.ContainerID, Valid: true}
		runRecord.ExitCode = sql.NullInt64{Int64: int64(result.ExitCode), Valid: true}
		if result.ExitCode == 0 {
			runRecord.Status = "succeeded"
		} else {
			runRecord.Status = "failed"
		}
	}

	now := time.Now()
	runRecord.CompletedAt = sql.NullTime{Time: now, Valid: true}
	h.runRepo.Save(ctx, runRecord)
}
