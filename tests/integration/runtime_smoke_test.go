//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jain-aadi/horizon/internal/sandbox"
)

const smokeAgentYAML = `version: "16"
models:
  horizon-model:
    provider: google
    model: gemini-3.5-flash-lite
agents:
  root:
    model: horizon-model
    description: Horizon real-runtime smoke test
    instruction: |
      Goal:
      Verify the Horizon runtime.

      Instructions:
      Respond with exactly: Horizon runtime smoke test passed.
`

func TestPodmanCagentSmoke(t *testing.T) {
	if os.Getenv("HORIZON_RUN_REAL_RUNTIME_TESTS") != "1" {
		t.Skip("set HORIZON_RUN_REAL_RUNTIME_TESTS=1 to enable the real Podman/cagent smoke test")
	}

	socketPath := strings.TrimSpace(os.Getenv("HORIZON_SOCKET"))
	if socketPath == "" {
		t.Skip("set HORIZON_SOCKET to an explicit rootless Podman socket path")
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Skipf("Podman socket is unavailable: %v", err)
	}

	apiKey := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	if apiKey == "" {
		t.Skip("set GOOGLE_API_KEY to enable the real Podman/cagent smoke test")
	}

	image := strings.TrimSpace(os.Getenv("HORIZON_IMAGE"))
	if image == "" {
		image = "localhost/horizon-sandbox:latest"
	}
	engine, err := sandbox.NewEngine(sandbox.EngineConfig{
		SocketPath:       socketPath,
		ImageName:        image,
		APIVersion:       "v4.0.0",
		RequestTimeout:   30 * time.Second,
		ContainerTimeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := engine.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck() error = %v", err)
	}

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "agent.yaml"), []byte(smokeAgentYAML), 0o600); err != nil {
		t.Fatalf("write smoke agent YAML: %v", err)
	}

	var output bytes.Buffer
	result, err := engine.RunAgent(ctx, sandbox.RunOptions{
		WorkspaceDir: workspace,
		YAMLFile:     "agent.yaml",
		Prompt:       "Run the smoke test now.",
		APIKey:       apiKey,
		OutputWriter: &output,
		Execution: sandbox.ExecutionConfig{
			Image:            image,
			NetworkAccess:    true,
			ContainerTimeout: 2 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}
	if result == nil || result.Error != nil || result.ExitCode != 0 {
		t.Fatalf("RunAgent() result = %#v, want successful exit", result)
	}
	if !strings.Contains(output.String(), "Horizon runtime smoke test passed") {
		t.Fatalf("runtime output did not contain smoke-test confirmation: %q", output.String())
	}
}
