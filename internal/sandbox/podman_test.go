package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func response(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    request,
	}
}

func testEngine(handler roundTripperFunc) *Engine {
	return &Engine{
		config: EngineConfig{
			ImageName:        "localhost/horizon-sandbox:test",
			APIVersion:       "v4.0.0",
			RequestTimeout:   time.Second,
			ContainerTimeout: time.Minute,
		},
		httpClient:       &http.Client{Transport: handler},
		streamClient:     &http.Client{Transport: handler},
		rootlessVerified: true,
	}
}

func testRunOptions() RunOptions {
	return RunOptions{
		WorkspaceDir: ".",
		YAMLFile:     "agent.yaml",
		Prompt:       "report status",
		APIKey:       "test-key",
		OutputWriter: io.Discard,
		Execution: ExecutionConfig{
			Image:            "localhost/horizon-sandbox:test",
			NetworkAccess:    true,
			ContainerTimeout: 30 * time.Second,
			NanoCPUs:         500_000_000,
			MemoryBytes:      64 * 1024 * 1024,
		},
	}
}

func TestBuildCmdUsesContainerRelativePOSIXPath(t *testing.T) {
	engine := &Engine{}
	command := engine.buildCmd("agent.yaml", "report status")

	want := []string{"run", "--exec", "--last", "/workspace/agent.yaml", "report status"}
	if len(command) != len(want) {
		t.Fatalf("buildCmd() = %#v, want %#v", command, want)
	}
	for i := range want {
		if command[i] != want[i] {
			t.Fatalf("buildCmd()[%d] = %q, want %q; full command %#v", i, command[i], want[i], command)
		}
	}
}

func TestEnsureRootlessRejectsRootfulPodman(t *testing.T) {
	engine := testEngine(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/libpod/info") {
			return response(request, http.StatusOK, `{"host":{"security":{"rootless":false}}}`), nil
		}
		t.Fatalf("unexpected Podman request: %s %s", request.Method, request.URL.Path)
		return nil, nil
	})

	if err := engine.ensureRootless(context.Background()); err == nil {
		t.Fatal("ensureRootless() error = nil, want rootful Podman rejection")
	}
}

func TestRunAgentRejectsUnverifiedPodman(t *testing.T) {
	engine := testEngine(func(request *http.Request) (*http.Response, error) {
		t.Fatal("RunAgent() contacted Podman before rootless verification")
		return nil, nil
	})
	engine.rootlessVerified = false

	if _, err := engine.RunAgent(context.Background(), testRunOptions()); err == nil {
		t.Fatal("RunAgent() error = nil, want rootless verification failure")
	}
}

func TestRunAgentBuildsLeastPrivilegeCreateRequest(t *testing.T) {
	var created createReq
	var deleted bool
	engine := testEngine(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/containers/create"):
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				t.Fatalf("decode create payload: %v", err)
			}
			return response(request, http.StatusCreated, `{"Id":"container-id"}`), nil
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/start"):
			return response(request, http.StatusNoContent, ""), nil
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/logs"):
			return response(request, http.StatusOK, ""), nil
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/wait"):
			return response(request, http.StatusOK, `{"StatusCode":0}`), nil
		case request.Method == http.MethodDelete && strings.Contains(request.URL.Path, "/containers/container-id"):
			deleted = true
			return response(request, http.StatusNoContent, ""), nil
		default:
			t.Fatalf("unexpected Podman request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})

	result, err := engine.RunAgent(context.Background(), testRunOptions())
	if err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}
	if result.ExitCode != 0 || result.ContainerID != "container-id" {
		t.Errorf("RunAgent() result = %#v", result)
	}
	if !deleted {
		t.Error("RunAgent() did not remove the completed container")
	}
	if created.Img != "localhost/horizon-sandbox:test" {
		t.Errorf("create Image = %q", created.Img)
	}
	if got := created.Cmd; len(got) != 5 || got[3] != "/workspace/agent.yaml" {
		t.Errorf("create Cmd = %#v, want Docker Agent command with container-relative YAML", got)
	}
	if got := created.Env; len(got) != 1 || got[0] != "GOOGLE_API_KEY=test-key" {
		t.Errorf("create Env = %#v, want secure allowlisted provider credential only", got)
	}
	if created.HostConfig.NetworkMode != "slirp4netns:allow_host_loopback=false" {
		t.Errorf("NetworkMode = %q", created.HostConfig.NetworkMode)
	}
	if got := created.HostConfig.Binds; len(got) != 1 || !strings.HasSuffix(got[0], ":/workspace:ro,Z") {
		t.Errorf("Binds = %#v, want one read-only workspace bind", got)
	}
	if got := created.HostConfig.CapDrop; len(got) != 1 || got[0] != "ALL" {
		t.Errorf("CapDrop = %#v, want [ALL]", got)
	}
	if created.HostConfig.NanoCPUs != 500_000_000 || created.HostConfig.Memory != 64*1024*1024 {
		t.Errorf("resource limits = NanoCPUs:%d Memory:%d, want 500000000 and 67108864", created.HostConfig.NanoCPUs, created.HostConfig.Memory)
	}
	if created.HostConfig.PidsLimit != 256 {
		t.Errorf("PidsLimit = %d, want 256", created.HostConfig.PidsLimit)
	}
	if got := created.HostConfig.SecurityOpt; len(got) != 1 || got[0] != "no-new-privileges" {
		t.Errorf("SecurityOpt = %#v, want [no-new-privileges]", got)
	}
	if created.HostConfig.UsernsMode != "host" {
		t.Errorf("UsernsMode = %q, want host to preserve rootless host-user mapping", created.HostConfig.UsernsMode)
	}
}

func TestRunAgentRejectsImageOutsideGlobalPolicy(t *testing.T) {
	called := false
	engine := testEngine(func(request *http.Request) (*http.Response, error) {
		called = true
		return response(request, http.StatusInternalServerError, "unexpected"), nil
	})
	opts := testRunOptions()
	opts.Execution.Image = "unapproved/image:latest"

	if _, err := engine.RunAgent(context.Background(), opts); err == nil {
		t.Fatal("RunAgent() error = nil, want image policy rejection")
	}
	if called {
		t.Error("RunAgent() contacted Podman before rejecting an unapproved image")
	}
}

func TestRunAgentUsesNoNetworkWhenNotApproved(t *testing.T) {
	var networkMode string
	engine := testEngine(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/containers/create"):
			var created createReq
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				t.Fatalf("decode create payload: %v", err)
			}
			networkMode = created.HostConfig.NetworkMode
			return response(request, http.StatusCreated, `{"Id":"container-id"}`), nil
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/start"):
			return response(request, http.StatusNoContent, ""), nil
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/logs"):
			return response(request, http.StatusOK, ""), nil
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/wait"):
			return response(request, http.StatusOK, `{"StatusCode":0}`), nil
		case request.Method == http.MethodDelete:
			return response(request, http.StatusNoContent, ""), nil
		default:
			t.Fatalf("unexpected Podman request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	opts := testRunOptions()
	opts.Execution.NetworkAccess = false

	if _, err := engine.RunAgent(context.Background(), opts); err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}
	if networkMode != "none" {
		t.Errorf("NetworkMode = %q, want none", networkMode)
	}
}

func TestRunAgentCleansUpAfterPostCreateFailures(t *testing.T) {
	tests := []struct {
		name        string
		failurePath string
		failureCode int
		wantResult  bool
	}{
		{name: "start", failurePath: "/start", failureCode: http.StatusInternalServerError},
		{name: "log stream", failurePath: "/logs", failureCode: http.StatusBadGateway},
		{name: "wait", failurePath: "/wait", failureCode: http.StatusInternalServerError, wantResult: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var deleteCalls int
			engine := testEngine(func(request *http.Request) (*http.Response, error) {
				switch {
				case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/containers/create"):
					return response(request, http.StatusCreated, `{"Id":"container-id"}`), nil
				case strings.HasSuffix(request.URL.Path, tt.failurePath):
					return response(request, tt.failureCode, strings.Repeat("x", 8192)), nil
				case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/start"):
					return response(request, http.StatusNoContent, ""), nil
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/logs"):
					return response(request, http.StatusOK, ""), nil
				case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/wait"):
					return response(request, http.StatusOK, `{"StatusCode":0}`), nil
				case request.Method == http.MethodDelete:
					deleteCalls++
					return response(request, http.StatusNoContent, ""), nil
				default:
					t.Fatalf("unexpected Podman request: %s %s", request.Method, request.URL.Path)
					return nil, nil
				}
			})

			result, err := engine.RunAgent(context.Background(), testRunOptions())
			if tt.wantResult {
				if err != nil || result == nil || result.Error == nil {
					t.Fatalf("RunAgent() = (%#v, %v), want result error", result, err)
				}
			} else if err == nil {
				t.Fatal("RunAgent() error = nil, want failure")
			}
			if deleteCalls != 1 {
				t.Errorf("cleanup delete calls = %d, want 1", deleteCalls)
			}
		})
	}
}

func TestRemoveContainerRejectsUnexpectedStatus(t *testing.T) {
	engine := testEngine(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusInternalServerError, "remove failed"), nil
	})
	if err := engine.RemoveContainer("container-id"); err == nil {
		t.Fatal("RemoveContainer() error = nil, want HTTP status error")
	}
}

func TestRunAgentDrainsFinalLogsAfterWaitCompletes(t *testing.T) {
	logStarted := make(chan struct{})
	waitStarted := make(chan struct{})
	var output bytes.Buffer
	engine := testEngine(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/containers/create"):
			return response(request, http.StatusCreated, `{"Id":"container-id"}`), nil
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/start"):
			return response(request, http.StatusNoContent, ""), nil
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/logs"):
			close(logStarted)
			select {
			case <-waitStarted:
				return response(request, http.StatusOK, "\x01\x00\x00\x00\x00\x00\x00\x0afinal log\n"), nil
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/wait"):
			close(waitStarted)
			return response(request, http.StatusOK, `{"StatusCode":0}`), nil
		case request.Method == http.MethodDelete:
			return response(request, http.StatusNoContent, ""), nil
		default:
			t.Fatalf("unexpected Podman request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	opts := testRunOptions()
	opts.OutputWriter = &output

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := engine.RunAgent(ctx, opts)
	if err != nil || result == nil || result.Error != nil || result.ExitCode != 0 {
		t.Fatalf("RunAgent() = (%#v, %v), want successful result", result, err)
	}
	if output.String() != "final log\n" {
		t.Fatalf("captured output = %q, want final log frame", output.String())
	}
	select {
	case <-logStarted:
	default:
		t.Fatal("RunAgent() did not start the follow-log request")
	}
	select {
	case <-waitStarted:
	default:
		t.Fatal("RunAgent() did not start wait while logs were still open")
	}
}

func TestRunAgentAppliesContainerTimeoutDuringFollowLogs(t *testing.T) {
	engine := testEngine(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/containers/create"):
			return response(request, http.StatusCreated, `{"Id":"container-id"}`), nil
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/start"):
			return response(request, http.StatusNoContent, ""), nil
		case (request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/logs")) || (request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/wait")):
			<-request.Context().Done()
			return nil, request.Context().Err()
		case request.Method == http.MethodDelete:
			return response(request, http.StatusNoContent, ""), nil
		default:
			t.Fatalf("unexpected Podman request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	opts := testRunOptions()
	opts.Execution.ContainerTimeout = 20 * time.Millisecond

	started := time.Now()
	result, err := engine.RunAgent(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want timeout result", err)
	}
	if result == nil || result.Error == nil || !strings.Contains(result.Error.Error(), "timeout") {
		t.Fatalf("RunAgent() = %#v, want timeout result", result)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("RunAgent() timeout elapsed = %v, want bounded execution", elapsed)
	}
}

func TestReadDiagnosticBoundsAndRedactsProviderCredential(t *testing.T) {
	const secret = "super-secret-value"
	for _, body := range []string{
		"Podman rejected GOOGLE_API_KEY=" + secret,
		"Podman rejected GOOGLE_API_KEY: " + secret,
		`{"GOOGLE_API_KEY":"` + secret + `"}`,
		"Podman echoed an unrelated value " + secret,
	} {
		diagnostic := readDiagnostic(strings.NewReader(body), secret)
		if strings.Contains(diagnostic, secret) {
			t.Fatalf("readDiagnostic(%q) leaked provider credential: %q", body, diagnostic)
		}
		if !strings.Contains(diagnostic, "[REDACTED]") {
			t.Fatalf("readDiagnostic(%q) = %q, want redacted credential marker", body, diagnostic)
		}
	}

	bounded := readDiagnostic(strings.NewReader(strings.Repeat(" ", maxDiagnosticBytes+1)))
	if len(bounded) > maxDiagnosticBytes+len("…") {
		t.Fatalf("readDiagnostic() length = %d, want <= %d", len(bounded), maxDiagnosticBytes+len("…"))
	}
}

func TestRunAgentRedactsProviderCredentialFromPodmanErrors(t *testing.T) {
	engine := testEngine(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/containers/create") {
			return response(request, http.StatusInternalServerError, `Podman rejected GOOGLE_API_KEY: test-key`), nil
		}
		t.Fatalf("unexpected Podman request: %s %s", request.Method, request.URL.Path)
		return nil, nil
	})

	_, err := engine.RunAgent(context.Background(), testRunOptions())
	if err == nil {
		t.Fatal("RunAgent() error = nil, want create failure")
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Fatalf("RunAgent() error leaked provider credential: %v", err)
	}
}
