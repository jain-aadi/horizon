package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jain-aadi/horizon/internal/agent"
	"github.com/jain-aadi/horizon/internal/cagent"
	"github.com/jain-aadi/horizon/internal/run"
	"github.com/jain-aadi/horizon/internal/storage"
)

// Handler implements the HTTP API.
type Handler struct {
	agentService *agent.Service
	runRepo      *storage.RunRepository
	runService   run.RunCoordinator
	engine       Engine
	config       *Config
}

// Engine is the runtime contract required by the HTTP layer.
type Engine interface {
	run.Executor
	HealthCheck(ctx context.Context) error
}

// Config holds handler configuration.
type Config struct {
	DefaultTimeout time.Duration
	DefaultImage   string
	DataDir        string
	MaxOutputBytes int64
}

// NewHandler creates a new API handler.
func NewHandler(agentRepo *storage.AgentRepository, runRepo *storage.RunRepository, engine Engine, cfg *Config) *Handler {
	defaultImage := ""
	if cfg != nil {
		defaultImage = cfg.DefaultImage
	}
	agentService := agent.NewService(agentRepo, runRepo, defaultImage)
	adapter := cagent.NewDefaultAdapter()
	runConfig := run.Config{LookupAgent: func(ctx context.Context, agentID string) (*run.AgentSnapshot, error) {
		definition, err := agentService.GetAgent(ctx, agentID)
		if err != nil {
			return nil, err
		}
		return &run.AgentSnapshot{ID: definition.ID, Version: definition.Version, Definition: definition}, nil
	}, Materialize: func(snapshot *run.AgentSnapshot) ([]byte, error) {
		definition, ok := snapshot.Definition.(*agent.AgentDefinition)
		if !ok {
			return nil, fmt.Errorf("unexpected agent snapshot definition type %T", snapshot.Definition)
		}
		config, err := adapter.BuildConfig(definition)
		if err != nil {
			return nil, err
		}
		return adapter.MarshalConfig(config)
	}}
	if cfg != nil {
		runConfig.DataDir = cfg.DataDir
		runConfig.AgentTimeout = cfg.DefaultTimeout
		runConfig.MaxOutputBytes = cfg.MaxOutputBytes
	}
	runConfig.APIKey = os.Getenv("GOOGLE_API_KEY")

	return &Handler{
		agentService: agentService,
		runRepo:      runRepo,
		runService:   run.NewService(run.NewStorageRepository(runRepo), engine, runConfig),
		engine:       engine,
		config:       cfg,
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

type apiErrorResponse struct {
	Error   string `json:"error"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message,omitempty"`
}

func writeValidationError(w http.ResponseWriter, validationError *agent.ValidationError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(apiErrorResponse{
		Error:   "validation_error",
		Field:   validationError.Field,
		Message: validationError.Message,
	})
}

func writeAgentServiceError(w http.ResponseWriter, err error) {
	var validationError *agent.ValidationError
	if errors.As(err, &validationError) {
		writeValidationError(w, validationError)
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	log.Printf("agent service error: %v", err)
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

func writeRunServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, run.ErrAgentNotFound) {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(w, "Run not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, run.ErrInvalidAgentID) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, run.ErrRunNotCancellable) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if errors.Is(err, run.ErrOutputUnavailable) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	log.Printf("run service error: %v", err)
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		http.Error(w, "Podman engine is not configured", http.StatusServiceUnavailable)
		return
	}
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
	agents, err := h.agentService.ListAgents(r.Context())
	if err != nil {
		writeAgentServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	var req agent.AgentDefinition
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.agentService.CreateAgent(r.Context(), &req); err != nil {
		writeAgentServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(req)
}

func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request, id string) {
	agentDef, err := h.agentService.GetAgent(r.Context(), id)
	if err != nil {
		writeAgentServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agentDef)
}

func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request, id string) {
	var req agent.AgentDefinition
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.ID = id

	if err := h.agentService.UpdateAgent(r.Context(), &req); err != nil {
		writeAgentServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(req)
}

func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.agentService.DeleteAgent(r.Context(), id); err != nil {
		writeAgentServiceError(w, err)
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
	if strings.HasSuffix(id, "/output") {
		id = strings.TrimSuffix(id, "/output")
		if id == "" || r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.getRunOutput(w, r, id)
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

	runs := make([]*run.RunRecord, len(rows))
	for i, row := range rows {
		runs[i] = run.FromStorageRow(row)
	}
	json.NewEncoder(w).Encode(runs)
}

func (h *Handler) createRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID     string `json:"agent_id"`
		TriggerType string `json:"trigger_type"`
		Prompt      string `json:"prompt"`
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

	runRecord, err := h.runService.StartRun(r.Context(), req.AgentID, req.TriggerType, req.Prompt)
	if err != nil {
		writeRunServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(runRecord)
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request, id string) {
	runRecord, err := h.runService.GetRun(r.Context(), id)
	if err != nil {
		writeRunServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(runRecord)
}

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
	}
	if err := h.runService.CancelRun(r.Context(), id, req.Reason); err != nil {
		writeRunServiceError(w, err)
		return
	}
	runRecord, err := h.runService.GetRun(r.Context(), id)
	if err != nil {
		writeRunServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(runRecord)
}

func (h *Handler) getRunOutput(w http.ResponseWriter, r *http.Request, id string) {
	output, err := h.runService.ReadOutput(r.Context(), id)
	if err != nil {
		writeRunServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(output)
}
