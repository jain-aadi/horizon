package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// EngineConfig holds configuration for the Podman engine.
type EngineConfig struct {
	SocketPath      string
	ImageName       string
	APIVersion      string
	RequestTimeout  time.Duration
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
		SocketPath:      sock,
		ImageName:       "localhost/horizon-sandbox:latest",
		APIVersion:      "v4.0.0",
		RequestTimeout:  30 * time.Second,
		ContainerTimeout: 10 * time.Minute,
	}
}

type Engine struct {
	config     EngineConfig
	httpClient *http.Client
}

type createReq struct {
	Img        string     `json:"Image"`
	Cmd        []string   `json:"Cmd"`
	Env        []string   `json:"Env,omitempty"`
	WorkingDir string     `json:"WorkingDir,omitempty"`
	HostConfig hostConfig `json:"HostConfig"`
}

type hostConfig struct {
	Binds        []string `json:"Binds,omitempty"`
	AutoRemove   bool     `json:"AutoRemove,omitempty"`
	NetworkMode  string   `json:"NetworkMode,omitempty"`
	CapDrop      []string `json:"CapDrop,omitempty"`
	LogConfig    logConfig `json:"LogConfig,omitempty"`
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

	client := &http.Client{
		Timeout: config.RequestTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", config.SocketPath)
			},
		},
	}

	return &Engine{
		config:     config,
		httpClient: client,
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
	WorkspaceDir string
	YAMLFile     string
	Prompt       string
	APIKey       string
	OutputWriter io.Writer
	Env          map[string]string
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
	absWorkspace, err := filepath.Abs(opts.WorkspaceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}

	// Build environment variables
	env := []string{}
	for k, v := range opts.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	if opts.APIKey != "" {
		env = append(env, "GOOGLE_API_KEY="+opts.APIKey)
	}

	payload := createReq{
		Img:        e.config.ImageName,
		Cmd:        e.buildCmd(opts.YAMLFile, opts.Prompt),
		Env:        env,
		WorkingDir: "/workspace",
		HostConfig: hostConfig{
			Binds:       []string{fmt.Sprintf("%s:/workspace:ro,Z", absWorkspace)},
			AutoRemove:  false,
			NetworkMode: "slirp4netns",
			CapDrop:     []string{"ALL"},
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
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("container create failed (%d): %s", resp.StatusCode, string(b))
	}

	var created createResp
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("decode container create response: %w", err)
	}
	containerID := created.ID

	// Ensure cleanup runs even on error/panic
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		delReq, _ := http.NewRequestWithContext(cleanupCtx, "DELETE",
			fmt.Sprintf("http://d/%s/containers/%s?force=true", e.config.APIVersion, containerID), nil)
		if delResp, err := e.httpClient.Do(delReq); err == nil {
			delResp.Body.Close()
		}
	}()

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
		b, _ := io.ReadAll(startResp.Body)
		startResp.Body.Close()
		return nil, fmt.Errorf("container start failed (%d): %s", startResp.StatusCode, string(b))
	}
	startResp.Body.Close()

	// Stream logs in real time
	logCtx, logCancel := context.WithCancel(ctx)
	defer logCancel()
	if err := e.StreamLogs(logCtx, containerID, opts.OutputWriter); err != nil {
		return nil, fmt.Errorf("log stream failed: %w", err)
	}

	// Wait for the agent process to finish with timeout
	waitCtx, waitCancel := context.WithTimeout(ctx, e.config.ContainerTimeout)
	defer waitCancel()

	waitURL := fmt.Sprintf("http://d/%s/containers/%s/wait?condition=not-running", e.config.APIVersion, containerID)
	waitReq, err := http.NewRequestWithContext(waitCtx, "POST", waitURL, nil)
	if err != nil {
		return nil, fmt.Errorf("wait request: %w", err)
	}

	waitHTTPResp, err := e.httpClient.Do(waitReq)
	if err != nil {
		if waitCtx.Err() == context.DeadlineExceeded {
			return &RunResult{ContainerID: containerID, ExitCode: -1, Error: fmt.Errorf("container wait timeout after %v", e.config.ContainerTimeout)}, nil
		}
		return nil, fmt.Errorf("container wait failed: %w", err)
	}
	defer waitHTTPResp.Body.Close()

	if waitHTTPResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(waitHTTPResp.Body)
		return &RunResult{ContainerID: containerID, ExitCode: -1, Error: fmt.Errorf("container wait failed (%d): %s", waitHTTPResp.StatusCode, string(b))}, nil
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
		filepath.Join("/workspace", yamlFile),
		prompt,
	}
}

// StreamLogs streams container logs to the output writer.
func (e *Engine) StreamLogs(ctx context.Context, containerID string, out io.Writer) error {
	logURL := fmt.Sprintf("http://d/%s/containers/%s/logs?follow=true&stdout=true&stderr=true", e.config.APIVersion, containerID)
	logReq, err := http.NewRequestWithContext(ctx, "GET", logURL, nil)
	if err != nil {
		return err
	}

	logResp, err := e.httpClient.Do(logReq)
	if err != nil {
		return err
	}
	defer logResp.Body.Close()

	return demuxStream(logResp.Body, out)
}

// RemoveContainer forcefully removes a container.
func (e *Engine) RemoveContainer(containerID string) error {
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
	return nil
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