package api

import (
	"context"
	"encoding/json"
	"errors"
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

type scriptedEngine struct {
	runFn   func(context.Context, sandbox.RunOptions) (*sandbox.RunResult, error)
	removed chan string
}

func (e *scriptedEngine) HealthCheck(context.Context) error { return nil }

func (e *scriptedEngine) RunAgent(ctx context.Context, opts sandbox.RunOptions) (*sandbox.RunResult, error) {
	return e.runFn(ctx, opts)
}

func (e *scriptedEngine) RemoveContainer(containerID string) error {
	if e.removed != nil {
		e.removed <- containerID
	}
	return nil
}

func newTestServer(t *testing.T) *httptest.Server {
	return newTestServerWithEngine(t, immediateEngine{t: t}, time.Second)
}

func newTestServerWithEngine(t *testing.T, engine Engine, defaultTimeout time.Duration) *httptest.Server {
	t.Helper()

	db, err := storage.NewDB(filepath.Join(t.TempDir(), "horizon.db"))
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	handler := NewHandler(
		storage.NewAgentRepository(db),
		storage.NewRunRepository(db),
		engine,
		&Config{DefaultImage: handlerTestDefaultImage, DataDir: t.TempDir(), DefaultTimeout: defaultTimeout},
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

func createHTTPTestAgent(t *testing.T, serverURL string) agent.AgentDefinition {
	t.Helper()
	response, err := http.Post(serverURL+"/api/agents", "application/json", strings.NewReader(`{"name":"HTTP test agent","goal":"Exercise the HTTP lifecycle.","instructions":"Return a concise result.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"},"permissions":{"network_access":true}}`))
	if err != nil {
		t.Fatalf("POST /api/agents error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/agents status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	var definition agent.AgentDefinition
	if err := json.NewDecoder(response.Body).Decode(&definition); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}
	return definition
}

func startHTTPTestRun(t *testing.T, serverURL, agentID string) run.RunRecord {
	t.Helper()
	response, err := http.Post(serverURL+"/api/runs", "application/json", strings.NewReader(`{"agent_id":"`+agentID+`","prompt":"Run the HTTP test"}`))
	if err != nil {
		t.Fatalf("POST /api/runs error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /api/runs status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	var record run.RunRecord
	if err := json.NewDecoder(response.Body).Decode(&record); err != nil {
		t.Fatalf("decode created run: %v", err)
	}
	return record
}

func waitForHTTPRun(t *testing.T, serverURL, runID string) run.RunRecord {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(serverURL + "/api/runs/" + runID)
		if err != nil {
			t.Fatalf("GET /api/runs/{id} error = %v", err)
		}
		var record run.RunRecord
		decodeErr := json.NewDecoder(response.Body).Decode(&record)
		response.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode run response: %v", decodeErr)
		}
		if run.IsTerminalStatus(record.Status) {
			return record
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach a terminal state", runID)
	return run.RunRecord{}
}

func TestAgentHTTPRetrievalListingAndDeletion(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()
	created := createHTTPTestAgent(t, server.URL)

	getResponse, err := http.Get(server.URL + "/api/agents/" + created.ID)
	if err != nil {
		t.Fatalf("GET /api/agents/{id} error = %v", err)
	}
	defer getResponse.Body.Close()
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/agents/{id} status = %d, want %d", getResponse.StatusCode, http.StatusOK)
	}
	var fetched agent.AgentDefinition
	if err := json.NewDecoder(getResponse.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode fetched agent: %v", err)
	}
	if fetched.ID != created.ID || fetched.Version != created.Version {
		t.Errorf("fetched agent = %#v, want created identity and version", fetched)
	}

	listResponse, err := http.Get(server.URL + "/api/agents")
	if err != nil {
		t.Fatalf("GET /api/agents error = %v", err)
	}
	defer listResponse.Body.Close()
	var definitions []agent.AgentDefinition
	if err := json.NewDecoder(listResponse.Body).Decode(&definitions); err != nil {
		t.Fatalf("decode listed agents: %v", err)
	}
	if len(definitions) != 1 || definitions[0].ID != created.ID {
		t.Errorf("listed agents = %#v, want one created agent", definitions)
	}

	deleteRequest, err := http.NewRequest(http.MethodDelete, server.URL+"/api/agents/"+created.ID, nil)
	if err != nil {
		t.Fatalf("NewRequest(DELETE) error = %v", err)
	}
	deleteResponse, err := http.DefaultClient.Do(deleteRequest)
	if err != nil {
		t.Fatalf("DELETE /api/agents/{id} error = %v", err)
	}
	deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /api/agents/{id} status = %d, want %d", deleteResponse.StatusCode, http.StatusNoContent)
	}

	missingResponse, err := http.Get(server.URL + "/api/agents/" + created.ID)
	if err != nil {
		t.Fatalf("GET deleted agent error = %v", err)
	}
	missingResponse.Body.Close()
	if missingResponse.StatusCode != http.StatusNotFound {
		t.Errorf("GET deleted agent status = %d, want %d", missingResponse.StatusCode, http.StatusNotFound)
	}
}

func TestHTTPRejectsMalformedAndUnknownResources(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()
	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{name: "malformed agent", method: http.MethodPost, path: "/api/agents", body: `{`, want: http.StatusBadRequest},
		{name: "invalid agent", method: http.MethodPost, path: "/api/agents", body: `{"name":"missing required fields"}`, want: http.StatusBadRequest},
		{name: "malformed run", method: http.MethodPost, path: "/api/runs", body: `{`, want: http.StatusBadRequest},
		{name: "missing run agent", method: http.MethodPost, path: "/api/runs", body: `{"prompt":"missing agent"}`, want: http.StatusBadRequest},
		{name: "unknown run agent", method: http.MethodPost, path: "/api/runs", body: `{"agent_id":"does-not-exist","prompt":"missing agent"}`, want: http.StatusNotFound},
		{name: "unknown run", method: http.MethodGet, path: "/api/runs/does-not-exist", want: http.StatusNotFound},
		{name: "unknown run output", method: http.MethodGet, path: "/api/runs/does-not-exist/output", want: http.StatusNotFound},
		{name: "unknown run cancellation", method: http.MethodPost, path: "/api/runs/does-not-exist?action=cancel", body: `{}`, want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := http.NewRequest(tt.method, server.URL+tt.path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("NewRequest() error = %v", err)
			}
			if tt.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("HTTP request error = %v", err)
			}
			response.Body.Close()
			if response.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", response.StatusCode, tt.want)
			}
		})
	}
}

func TestHTTPRunFailureNonzeroExitAndTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		runFn   func(context.Context, sandbox.RunOptions) (*sandbox.RunResult, error)
		want    run.RunStatus
	}{
		{name: "runtime failure", timeout: time.Second, runFn: func(_ context.Context, _ sandbox.RunOptions) (*sandbox.RunResult, error) {
			return nil, errors.New("container startup failed")
		}, want: run.RunStatusFailed},
		{name: "nonzero exit", timeout: time.Second, runFn: func(_ context.Context, _ sandbox.RunOptions) (*sandbox.RunResult, error) {
			return &sandbox.RunResult{ContainerID: "failed-container", ExitCode: 7}, nil
		}, want: run.RunStatusFailed},
		{name: "timeout", timeout: 10 * time.Millisecond, runFn: func(ctx context.Context, _ sandbox.RunOptions) (*sandbox.RunResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}, want: run.RunStatusTimedOut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newTestServerWithEngine(t, &scriptedEngine{runFn: tt.runFn}, tt.timeout)
			defer server.Close()
			definition := createHTTPTestAgent(t, server.URL)
			created := startHTTPTestRun(t, server.URL, definition.ID)
			completed := waitForHTTPRun(t, server.URL, created.ID)
			if completed.Status != tt.want {
				t.Errorf("completed Status = %q, want %q", completed.Status, tt.want)
			}
			if completed.ErrorMessage == "" {
				t.Error("completed ErrorMessage is empty, want runtime diagnostic")
			}
		})
	}
}

func TestHTTPCancellationPersistsAndRequestsRemoval(t *testing.T) {
	started := make(chan struct{})
	removed := make(chan string, 1)
	engine := &scriptedEngine{removed: removed, runFn: func(ctx context.Context, opts sandbox.RunOptions) (*sandbox.RunResult, error) {
		if opts.OnContainerCreated != nil {
			opts.OnContainerCreated("cancelled-container")
		}
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	server := newTestServerWithEngine(t, engine, time.Second)
	defer server.Close()
	definition := createHTTPTestAgent(t, server.URL)
	created := startHTTPTestRun(t, server.URL, definition.ID)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runtime did not start")
	}

	cancelResponse, err := http.Post(server.URL+"/api/runs/"+created.ID+"?action=cancel", "application/json", strings.NewReader(`{"reason":"test cancellation"}`))
	if err != nil {
		t.Fatalf("POST cancel error = %v", err)
	}
	defer cancelResponse.Body.Close()
	if cancelResponse.StatusCode != http.StatusOK {
		t.Fatalf("POST cancel status = %d, want %d", cancelResponse.StatusCode, http.StatusOK)
	}
	var cancelled run.RunRecord
	if err := json.NewDecoder(cancelResponse.Body).Decode(&cancelled); err != nil {
		t.Fatalf("decode cancellation response: %v", err)
	}
	if cancelled.Status != run.RunStatusCancelled || cancelled.CancelReason != "test cancellation" {
		t.Errorf("cancelled record = %#v", cancelled)
	}
	select {
	case containerID := <-removed:
		if containerID != "cancelled-container" {
			t.Errorf("removed container = %q, want cancelled-container", containerID)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not request container removal")
	}
}
