package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// EngineConfig holds configuration for the Podman engine.
type EngineConfig struct {
	SocketPath       string
	ImageName        string
	APIVersion       string
	RequestTimeout   time.Duration
	ContainerTimeout time.Duration
}

// DefaultEngineConfig returns sensible defaults.
func DefaultEngineConfig() EngineConfig {
	xdg := os.Getenv("XDG_RUNTIME_DIR")
	if xdg == "" {
		xdg = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	sock := filepath.Join(xdg, "podman", "podman.sock")

	return EngineConfig{
		SocketPath:       sock,
		ImageName:        "localhost/horizon-sandbox:latest",
		APIVersion:       "v4.0.0",
		RequestTimeout:   30 * time.Second,
		ContainerTimeout: 10 * time.Minute,
	}
}

type Engine struct {
	config       EngineConfig
	httpClient   *http.Client
	streamClient *http.Client
}

type createReq struct {
	Img        string     `json:"Image"`
	Cmd        []string   `json:"Cmd"`
	Env        []string   `json:"Env,omitempty"`
	WorkingDir string     `json:"WorkingDir,omitempty"`
	HostConfig hostConfig `json:"HostConfig"`
}

type hostConfig struct {
	Binds       []string  `json:"Binds,omitempty"`
	AutoRemove  bool      `json:"AutoRemove,omitempty"`
	NetworkMode string    `json:"NetworkMode,omitempty"`
	CapDrop     []string  `json:"CapDrop,omitempty"`
	NanoCPUs    int64     `json:"NanoCpus,omitempty"`
	Memory      int64     `json:"Memory,omitempty"`
	PidsLimit   int64     `json:"PidsLimit,omitempty"`
	SecurityOpt []string  `json:"SecurityOpt,omitempty"`
	LogConfig   logConfig `json:"LogConfig,omitempty"`
}

type logConfig struct {
	Type string `json:"Type"`
}

type createResp struct {
	ID string `json:"Id"`
}

type waitResp struct {
	StatusCode int `json:"StatusCode"`
}

// NewEngine creates a new Podman engine with the given config.
func NewEngine(config EngineConfig) (*Engine, error) {
	if _, err := os.Stat(config.SocketPath); err != nil {
		return nil, fmt.Errorf("podman socket not found at %s (run: systemctl --user start podman.socket)", config.SocketPath)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", config.SocketPath)
		},
	}
	client := &http.Client{Timeout: config.RequestTimeout, Transport: transport}
	streamClient := &http.Client{Transport: transport}

	return &Engine{
		config:       config,
		httpClient:   client,
		streamClient: streamClient,
	}, nil
}

// NewDefaultEngine creates an engine with default configuration.
func NewDefaultEngine() (*Engine, error) {
	return NewEngine(DefaultEngineConfig())
}

// demuxStream strips the 8-byte Docker/Podman multiplexed stream header.
func demuxStream(r io.Reader, out io.Writer) error {
	header := make([]byte, 8)
	for {
		_, err := io.ReadFull(r, header)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
		frameSize := binary.BigEndian.Uint32(header[4:8])
		if frameSize == 0 {
			continue
		}
		if _, err := io.CopyN(out, r, int64(frameSize)); err != nil {
			return err
		}
	}
}

// RunOptions holds options for running an agent.
type RunOptions struct {
	WorkspaceDir       string
	YAMLFile           string
	Prompt             string
	APIKey             string
	OutputWriter       io.Writer
	Execution          ExecutionConfig
	OnContainerCreated func(containerID string)
}

// ExecutionConfig is the validated runtime configuration for one execution.
// The engine applies global policy before converting it into Podman settings.
type ExecutionConfig struct {
	Image            string
	NetworkAccess    bool
	ContainerTimeout time.Duration
	NanoCPUs         int64
	MemoryBytes      int64
}

// RunResult contains the result of a run.
type RunResult struct {
	ContainerID string
	ExitCode    int
	Error       error
}

// RunAgent creates and starts the sandbox, returning the result.
// Output is streamed to out in real time. On any failure, output is captured
// and included in the returned error.
func (e *Engine) RunAgent(ctx context.Context, opts RunOptions) (*RunResult, error) {
	execution, err := e.resolveExecution(opts.Execution)
	if err != nil {
		return nil, err
	}
	absWorkspace, err := filepath.Abs(opts.WorkspaceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}

	// Only the provider credential is injected from the daemon's secure environment.
	env := []string{}
	if opts.APIKey != "" {
		env = append(env, "GOOGLE_API_KEY="+opts.APIKey)
	}

	payload := createReq{
		Img:        execution.Image,
		Cmd:        e.buildCmd(opts.YAMLFile, opts.Prompt),
		Env:        env,
		WorkingDir: "/workspace",
		HostConfig: hostConfig{
			Binds:       []string{fmt.Sprintf("%s:/workspace:ro,Z", absWorkspace)},
			AutoRemove:  false,
			NetworkMode: execution.networkMode,
			CapDrop:     []string{"ALL"},
			NanoCPUs:    execution.nanoCPUs,
			Memory:      execution.memoryBytes,
			PidsLimit:   256,
			SecurityOpt: []string{"no-new-privileges"},
			LogConfig:   logConfig{Type: "json-file"},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal create request: %w", err)
	}

	createURL := fmt.Sprintf("http://d/%s/containers/create", e.config.APIVersion)
	req, err := http.NewRequestWithContext(ctx, "POST", createURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("container create failed (%d): %s", resp.StatusCode, readDiagnostic(resp.Body, opts.APIKey))
	}

	var created createResp
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("decode container create response: %w", err)
	}
	containerID := created.ID

	// Ensure cleanup runs even on error/panic
	defer func() {
		if err := e.removeContainer(containerID, opts.APIKey); err != nil {
			log.Printf("Podman cleanup failed for container %s: %v", containerID, err)
		} else {
			log.Printf("Podman cleaned up container %s", containerID)
		}
	}()
	if opts.OnContainerCreated != nil {
		opts.OnContainerCreated(containerID)
	}

	// Start the container
	startURL := fmt.Sprintf("http://d/%s/containers/%s/start", e.config.APIVersion, containerID)
	startReq, err := http.NewRequestWithContext(ctx, "POST", startURL, nil)
	if err != nil {
		return nil, fmt.Errorf("start request: %w", err)
	}

	startResp, err := e.httpClient.Do(startReq)
	if err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}

	if startResp.StatusCode != http.StatusNoContent && startResp.StatusCode != http.StatusOK {
		diagnostic := readDiagnostic(startResp.Body, opts.APIKey)
		startResp.Body.Close()
		return nil, fmt.Errorf("container start failed (%d): %s", startResp.StatusCode, diagnostic)
	}
	startResp.Body.Close()

	// Run the bounded wait and follow-log requests concurrently. A follow stream
	// normally ends only when the container exits, so it cannot be allowed to
	// delay creation of the execution deadline.
	executionCtx, executionCancel := context.WithTimeout(ctx, execution.ContainerTimeout)
	defer executionCancel()

	type waitOutcome struct {
		result *RunResult
		err    error
	}
	waitDone := make(chan waitOutcome, 1)
	go func() {
		result, err := e.waitForContainer(executionCtx, containerID, execution.ContainerTimeout, opts.APIKey)
		waitDone <- waitOutcome{result: result, err: err}
	}()

	logsDone := make(chan error, 1)
	go func() {
		logsDone <- e.streamLogs(executionCtx, containerID, opts.OutputWriter, opts.APIKey)
	}()

	select {
	case wait := <-waitDone:
		if wait.err != nil || (wait.result != nil && wait.result.Error != nil) {
			executionCancel()
			<-logsDone
			return wait.result, wait.err
		}
		// A successful wait can arrive before Podman has flushed its final log
		// frames. Keep the execution context alive until the stream reaches EOF.
		if logErr := <-logsDone; logErr != nil {
			return nil, fmt.Errorf("log stream failed: %w", logErr)
		}
		return wait.result, nil
	case logErr := <-logsDone:
		if logErr != nil {
			executionCancel()
			wait := <-waitDone
			if errors.Is(executionCtx.Err(), context.DeadlineExceeded) {
				return wait.result, wait.err
			}
			return nil, fmt.Errorf("log stream failed: %w", logErr)
		}
		wait := <-waitDone
		return wait.result, wait.err
	}
}

func (e *Engine) waitForContainer(ctx context.Context, containerID string, timeout time.Duration, providerSecret string) (*RunResult, error) {
	waitURL := fmt.Sprintf("http://d/%s/containers/%s/wait?condition=not-running", e.config.APIVersion, containerID)
	waitReq, err := http.NewRequestWithContext(ctx, "POST", waitURL, nil)
	if err != nil {
		return nil, fmt.Errorf("wait request: %w", err)
	}

	waitHTTPResp, err := e.streamClient.Do(waitReq)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &RunResult{ContainerID: containerID, ExitCode: -1, Error: fmt.Errorf("container wait timeout after %v", timeout)}, nil
		}
		return nil, fmt.Errorf("container wait failed: %w", err)
	}
	defer waitHTTPResp.Body.Close()

	if waitHTTPResp.StatusCode != http.StatusOK {
		return &RunResult{ContainerID: containerID, ExitCode: -1, Error: fmt.Errorf("container wait failed (%d): %s", waitHTTPResp.StatusCode, readDiagnostic(waitHTTPResp.Body, providerSecret))}, nil
	}

	var waitResult waitResp
	if err := json.NewDecoder(waitHTTPResp.Body).Decode(&waitResult); err != nil {
		return &RunResult{ContainerID: containerID, ExitCode: -1, Error: fmt.Errorf("decode container exit status: %w", err)}, nil
	}

	return &RunResult{
		ContainerID: containerID,
		ExitCode:    waitResult.StatusCode,
		Error:       nil,
	}, nil
}

// buildCmd constructs the cagent command line.
func (e *Engine) buildCmd(yamlFile, prompt string) []string {
	return []string{
		"run",
		"--exec",
		"--last",
		path.Join("/workspace", yamlFile),
		prompt,
	}
}

// StreamLogs streams container logs to the output writer.
func (e *Engine) StreamLogs(ctx context.Context, containerID string, out io.Writer) error {
	return e.streamLogs(ctx, containerID, out, "")
}

func (e *Engine) streamLogs(ctx context.Context, containerID string, out io.Writer, providerSecret string) error {
	logURL := fmt.Sprintf("http://d/%s/containers/%s/logs?follow=true&stdout=true&stderr=true", e.config.APIVersion, containerID)
	logReq, err := http.NewRequestWithContext(ctx, "GET", logURL, nil)
	if err != nil {
		return err
	}

	logResp, err := e.streamClient.Do(logReq)
	if err != nil {
		return err
	}
	defer logResp.Body.Close()
	if logResp.StatusCode != http.StatusOK {
		return fmt.Errorf("container log stream failed (%d): %s", logResp.StatusCode, readDiagnostic(logResp.Body, providerSecret))
	}

	return demuxStream(logResp.Body, out)
}

// RemoveContainer forcefully removes a container.
func (e *Engine) RemoveContainer(containerID string) error {
	return e.removeContainer(containerID, "")
}

func (e *Engine) removeContainer(containerID string, providerSecret string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	delReq, err := http.NewRequestWithContext(ctx, "DELETE",
		fmt.Sprintf("http://d/%s/containers/%s?force=true", e.config.APIVersion, containerID), nil)
	if err != nil {
		return err
	}

	resp, err := e.httpClient.Do(delReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("container removal failed (%d): %s", resp.StatusCode, readDiagnostic(resp.Body, providerSecret))
}

type resolvedExecution struct {
	Image            string
	ContainerTimeout time.Duration
	networkMode      string
	nanoCPUs         int64
	memoryBytes      int64
}

func (e *Engine) resolveExecution(execution ExecutionConfig) (resolvedExecution, error) {
	if execution.Image == "" {
		return resolvedExecution{}, errors.New("runtime image is required")
	}
	if execution.Image != e.config.ImageName {
		return resolvedExecution{}, fmt.Errorf("runtime image %q is not allowed by global policy", execution.Image)
	}
	if execution.NanoCPUs < 0 || execution.MemoryBytes < 0 {
		return resolvedExecution{}, errors.New("runtime resource limits cannot be negative")
	}

	timeout := e.config.ContainerTimeout
	if execution.ContainerTimeout > 0 {
		if execution.ContainerTimeout > e.config.ContainerTimeout {
			return resolvedExecution{}, fmt.Errorf("requested container timeout %v exceeds global maximum %v", execution.ContainerTimeout, e.config.ContainerTimeout)
		}
		timeout = execution.ContainerTimeout
	}

	networkMode := "none"
	if execution.NetworkAccess {
		networkMode = "slirp4netns:allow_host_loopback=false"
	}
	return resolvedExecution{
		Image:            execution.Image,
		ContainerTimeout: timeout,
		networkMode:      networkMode,
		nanoCPUs:         execution.NanoCPUs,
		memoryBytes:      execution.MemoryBytes,
	}, nil
}

const maxDiagnosticBytes = 4096

var providerCredentialPattern = regexp.MustCompile(`(?i)(["']?GOOGLE_API_KEY["']?\s*(?:=|:)\s*["']?)[^\s"',}]+`)

func readDiagnostic(reader io.Reader, secrets ...string) string {
	data, err := io.ReadAll(io.LimitReader(reader, maxDiagnosticBytes+1))
	if err != nil {
		return "unable to read diagnostic"
	}
	diagnostic := strings.TrimSpace(string(data))
	for _, secret := range secrets {
		if secret != "" {
			diagnostic = strings.ReplaceAll(diagnostic, secret, "[REDACTED]")
		}
	}
	diagnostic = providerCredentialPattern.ReplaceAllString(diagnostic, "${1}[REDACTED]")
	if len(data) > maxDiagnosticBytes {
		if len(diagnostic) > maxDiagnosticBytes {
			diagnostic = diagnostic[:maxDiagnosticBytes]
		}
		return diagnostic + "…"
	}
	return diagnostic
}

// HealthCheck verifies the Podman socket is reachable and the image exists.
func (e *Engine) HealthCheck(ctx context.Context) error {
	// Check socket connectivity
	healthURL := fmt.Sprintf("http://d/%s/_ping", e.config.APIVersion)
	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		return fmt.Errorf("health check request: %w", err)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("podman unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("podman health check failed: %d", resp.StatusCode)
	}

	// Check image exists
	imageURL := fmt.Sprintf("http://d/%s/images/%s/json", e.config.APIVersion, e.config.ImageName)
	imgReq, _ := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	imgResp, err := e.httpClient.Do(imgReq)
	if err != nil {
		return fmt.Errorf("image check failed: %w", err)
	}
	defer imgResp.Body.Close()

	if imgResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("image %s not found (run: podman build -t %s runtime/)", e.config.ImageName, e.config.ImageName)
	}
	if imgResp.StatusCode != http.StatusOK {
		return fmt.Errorf("image check failed: %d", imgResp.StatusCode)
	}

	return nil
}
