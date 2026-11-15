package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jain-aadi/horizon/internal/agent"
	"github.com/jain-aadi/horizon/internal/storage"
)

const handlerTestDefaultImage = "localhost/horizon-sandbox:test"

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
		nil,
		&Config{DefaultImage: handlerTestDefaultImage},
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
		strings.NewReader(`{"id":"client-controlled-id","name":"Daily brief","goal":"Prepare a concise daily brief.","instructions":"Summarize the configured input.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"}}`),
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
		strings.NewReader(`{"goal":"Prepare a concise daily brief.","instructions":"Summarize the configured input.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"}}`),
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
		strings.NewReader(`{"name":"Daily brief","goal":"Prepare a concise daily brief.","instructions":"Summarize the configured input.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"}}`),
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
		strings.NewReader(`{"id":"client-controlled-id","name":"Updated daily brief","goal":"Prepare a revised brief.","instructions":"Summarize the configured input in five bullet points.","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"},"version":99}`),
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
