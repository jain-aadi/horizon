package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jain-aadi/horizon/internal/agent"
	"github.com/jain-aadi/horizon/internal/run"
	"github.com/jain-aadi/horizon/internal/sandbox"
	"github.com/jain-aadi/horizon/internal/storage"
)

const handlerTestDefaultImage = "localhost/horizon-sandbox:test"

type immediateEngine struct {
	t *testing.T
}

func (immediateEngine) HealthCheck(context.Context) error { return nil }

func (e immediateEngine) RunAgent(_ context.Context, opts sandbox.RunOptions) (*sandbox.RunResult, error) {
	if e.t != nil && (opts.Execution.Image != handlerTestDefaultImage || !opts.Execution.NetworkAccess || opts.Execution.NanoCPUs != 500_000_000 || opts.Execution.MemoryBytes != 64*1024*1024 || opts.Execution.ContainerTimeout != time.Second) {
		e.t.Errorf("RunAgent() Execution = %#v, want saved image, approved network access, and parsed resource limits", opts.Execution)
	}
	if opts.OnContainerCreated != nil {
		opts.OnContainerCreated("api-test-container")
	}
	_, _ = io.WriteString(opts.OutputWriter, "api test output")
	return &sandbox.RunResult{ContainerID: "api-test-container", ExitCode: 0}, nil
}

func (immediateEngine) RemoveContainer(string) error { return nil }

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	db, err := storage.NewDB(filepath.Join(t.TempDir(), "horizon.db"))
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	handler := NewHandler(
		storage.NewAgentRepository(db),
		storage.NewRunRepository(db),
		immediateEngine{t: t},
		&Config{DefaultImage: handlerTestDefaultImage, DataDir: t.TempDir(), DefaultTimeout: time.Second},
	)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

func TestCreateAgentAppliesServiceDefaults(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	response, err := http.Post(
		server.URL+"/api/agents",
		"application/json",
		strings.NewReader(`{"id":"client-controlled-id","name":"Daily brief","goal":"Prepare a concise daily brief.","instructions":"Summarize the configured input.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"},"permissions":{"network_access":true}}`),
	)
	if err != nil {
		t.Fatalf("POST /api/agents error = %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/agents status = %d, want %d", response.StatusCode, http.StatusCreated)
	}

	var created agent.AgentDefinition
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if created.ID == "" {
		t.Error("created agent ID is empty")
	}
	if created.ID == "client-controlled-id" {
		t.Error("created agent retained a client-controlled ID")
	}
	if created.Version != 1 {
		t.Errorf("created Version = %d, want 1", created.Version)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Errorf("created timestamps = (%s, %s), want both set", created.CreatedAt, created.UpdatedAt)
	}
	if created.RuntimeConfig.Image != handlerTestDefaultImage {
		t.Errorf("created RuntimeConfig.Image = %q, want %q", created.RuntimeConfig.Image, handlerTestDefaultImage)
	}
}

func TestCreateAgentReturnsStructuredValidationError(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	response, err := http.Post(
		server.URL+"/api/agents",
		"application/json",
		strings.NewReader(`{"goal":"Prepare a concise daily brief.","instructions":"Summarize the configured input.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"},"permissions":{"network_access":true}}`),
	)
	if err != nil {
		t.Fatalf("POST /api/agents error = %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /api/agents status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	var body struct {
		Error   string `json:"error"`
		Field   string `json:"field"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if body.Error != "validation_error" {
		t.Errorf("error = %q, want %q", body.Error, "validation_error")
	}
	if body.Field != "name" {
		t.Errorf("field = %q, want %q", body.Field, "name")
	}
	if body.Message != "is required" {
		t.Errorf("message = %q, want %q", body.Message, "is required")
	}
}

func TestUpdateAgentPreservesServiceManagedFields(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	createResponse, err := http.Post(
		server.URL+"/api/agents",
		"application/json",
		strings.NewReader(`{"name":"Daily brief","goal":"Prepare a concise daily brief.","instructions":"Summarize the configured input.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"},"permissions":{"network_access":true}}`),
	)
	if err != nil {
		t.Fatalf("POST /api/agents error = %v", err)
	}
	defer createResponse.Body.Close()

	var created agent.AgentDefinition
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("Decode create response error = %v", err)
	}

	time.Sleep(time.Millisecond)
	updateRequest, err := http.NewRequest(
		http.MethodPut,
		server.URL+"/api/agents/"+created.ID,
		strings.NewReader(`{"id":"client-controlled-id","name":"Updated daily brief","goal":"Prepare a revised brief.","instructions":"Summarize the configured input in five bullet points.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"},"permissions":{"network_access":true},"version":99}`),
	)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse, err := http.DefaultClient.Do(updateRequest)
	if err != nil {
		t.Fatalf("PUT /api/agents/{id} error = %v", err)
	}
	defer updateResponse.Body.Close()

	if updateResponse.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/agents/{id} status = %d, want %d", updateResponse.StatusCode, http.StatusOK)
	}

	var updated agent.AgentDefinition
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("Decode update response error = %v", err)
	}
	if updated.ID != created.ID {
		t.Errorf("updated ID = %q, want %q", updated.ID, created.ID)
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Errorf("updated CreatedAt = %s, want %s", updated.CreatedAt, created.CreatedAt)
	}
	if updated.Version != 2 {
		t.Errorf("updated Version = %d, want 2", updated.Version)
	}
	if updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Errorf("updated UpdatedAt = %s, want after %s", updated.UpdatedAt, created.UpdatedAt)
	}
}

func TestCreateRunMaterializesAndServesOutput(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	createAgentResponse, err := http.Post(
		server.URL+"/api/agents",
		"application/json",
		strings.NewReader(`{"name":"Daily brief","goal":"Prepare a concise daily brief.","instructions":"Summarize the configured input.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"},"permissions":{"network_access":true},"runtime_config":{"resource_limits":{"cpu_limit":"0.5","memory_limit":"64MiB","timeout":"1s"}}}`),
	)
	if err != nil {
		t.Fatalf("POST /api/agents error = %v", err)
	}
	defer createAgentResponse.Body.Close()
	var definition agent.AgentDefinition
	if err := json.NewDecoder(createAgentResponse.Body).Decode(&definition); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}

	createRunResponse, err := http.Post(
		server.URL+"/api/runs",
		"application/json",
		strings.NewReader(`{"agent_id":"`+definition.ID+`","prompt":"Create the daily brief"}`),
	)
	if err != nil {
		t.Fatalf("POST /api/runs error = %v", err)
	}
	defer createRunResponse.Body.Close()
	if createRunResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /api/runs status = %d, want %d", createRunResponse.StatusCode, http.StatusAccepted)
	}
	var created run.RunRecord
	if err := json.NewDecoder(createRunResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created run: %v", err)
	}
	if created.Status != run.RunStatusQueued {
		t.Errorf("created Status = %q, want %q", created.Status, run.RunStatusQueued)
	}

	var completed run.RunRecord
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(server.URL + "/api/runs/" + created.ID)
		if err != nil {
			t.Fatalf("GET /api/runs/{id} error = %v", err)
		}
		if err := json.NewDecoder(response.Body).Decode(&completed); err != nil {
			response.Body.Close()
			t.Fatalf("decode run response: %v", err)
		}
		response.Body.Close()
		if run.IsTerminalStatus(completed.Status) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if completed.Status != run.RunStatusSucceeded {
		t.Fatalf("completed Status = %q, want %q", completed.Status, run.RunStatusSucceeded)
	}
	if completed.OutputRef == "" || completed.ContainerID != "api-test-container" {
		t.Errorf("completed record = %#v, want output ref and container ID", completed)
	}

	outputResponse, err := http.Get(server.URL + "/api/runs/" + created.ID + "/output")
	if err != nil {
		t.Fatalf("GET /api/runs/{id}/output error = %v", err)
	}
	defer outputResponse.Body.Close()
	if outputResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/runs/{id}/output status = %d, want %d", outputResponse.StatusCode, http.StatusOK)
	}
	output, err := io.ReadAll(outputResponse.Body)
	if err != nil {
		t.Fatalf("read output response: %v", err)
	}
	if string(output) != "api test output" {
		t.Errorf("output = %q, want %q", output, "api test output")
	}
}
