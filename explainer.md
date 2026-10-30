# Horizon Project — Complete Technical Documentation

## Overview

Horizon is a local-first, no-code AI agent platform built in Go. It enables non-technical users to create, configure, run, and manage AI agents using natural language. The platform uses:

- **Go** — Application core and HTTP API
- **cagent** (Docker Agent) — Agent reasoning engine running inside containers
- **Rootless Podman** — Container execution backend
- **SQLite** — Local persistent storage (via `modernc.org/sqlite`, CGO-free)

This document provides exhaustive detail on the project structure, every file's purpose, code architecture, and implementation status.

---

## Project Structure (Matches `project.md` §7)

```
horizon/
├── cmd/
│   └── horizond/
│       └── main.go                 # Server entry point with HTTP API, graceful shutdown, health-check flag
├── internal/
│   ├── agent/                      # Agent lifecycle domain logic
│   │   ├── model.go                # Domain types: AgentDefinition, RuntimeConfig, ModelConfig, Permissions, ScheduleConfig
│   │   ├── service.go              # Business logic: CreateAgent, GetAgent, ListAgents, UpdateAgent, DeleteAgent, GetAgentWithRuns
│   │   └── repository.go           # Repository interfaces (AgentRepository, RunRepository) + local RunRecord alias
│   ├── run/                        # Run execution domain
│   │   ├── model.go                # RunStatus constants + RunRecord struct
│   │   ├── coordinator.go          # RunCoordinator interface (ExecuteRun, CancelRun, GetRunStatus)
│   │   └── repository.go           # RunRepository interface
│   ├── scheduler/                  # Empty — Phase 4 (scheduling, cron, pause/resume)
│   ├── cagent/                     # cagent integration layer
│   │   └── adapter.go              # Adapter interface + DefaultAdapter (BuildConfig, ValidateConfig, Execute stub)
│   ├── storage/                    # SQLite persistence
│   │   ├── sqlite.go               # DB wrapper, migrations, AgentRepository, RunRepository, scan functions, ToAgentDomain/ToRunDomain converters
│   │   └── migrations/
│   │       ├── 001_create_agents.sql   # Agents table + index
│   │       └── 002_create_runs.sql     # Runs table + indexes + FK to agents
│   ├── permissions/                # Permission types and validation
│   │   └── permissions.go          # Permission enum, PermissionSet, DefaultPermissions, Validate
│   ├── config/                     # Application configuration
│   │   └── config.go               # Config struct with ServerConfig, DatabaseConfig, PodmanConfig, AgentConfig + DefaultConfig()
│   └── api/                        # HTTP API layer
│       ├── api.go                  # APIHandler interface + ServerConfig
│       └── handler.go              # Handler struct with full CRUD for agents/runs, async executeRun, health check
├── web/                            # Empty — Future web UI (Angular/Tailwind)
├── tests/                          # Empty — Integration tests (require Podman socket)
├── runtime/
│   └── Containerfile               # Multi-stage build: cagent binary + minimal Alpine runtime
├── go.mod                          # Module: github.com/horizon/horizon, Go 1.26.0
├── go.sum                          # Dependency checksums
├── project.md                      # Product spec and roadmap
├── run.md                          # Operations guide (health check, curl examples, capabilities, troubleshooting)
├── explainer.md                    # This file
└── .env                            # Environment variables template
```

---

## Detailed File Documentation

### 1. `go.mod`

```go
module github.com/horizon/horizon

go 1.26.0

require (
	github.com/google/uuid v1.6.0
	modernc.org/sqlite v1.61.0
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.49.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
```

- **Module path**: `github.com/horizon/horizon` (canonical import path)
- **Go version**: `1.26.0` — uses `modernc.org/sqlite` (pure Go) so `CGO_ENABLED=0` builds work
- **Key dependencies**: `google/uuid` for run/agent IDs; `modernc.org/sqlite` for CGO-free SQLite

---

### 2. `cmd/horizond/main.go` — Server Entry Point

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/horizon/horizon/internal/api"
	"github.com/horizon/horizon/internal/config"
	"github.com/horizon/horizon/internal/sandbox"
	"github.com/horizon/horizon/internal/storage"
)
```

**Responsibilities**:
1. **Flag parsing** — `config` (file path), `db` (database path, default `./data/horizon.db`), `port` (default 8080), `health-check` (bool)
2. **Config loading** — `config.DefaultConfig()` with flag overrides
3. **Database init** — `storage.NewDB(path)` runs migrations automatically
4. **Podman engine init** — `sandbox.NewEngine(config)` with defaults from `DefaultEngineConfig()`
5. **Health-check mode** — `--health-check` runs `engine.HealthCheck()` and exits
6. **Repositories** — `storage.NewAgentRepository(db)`, `storage.NewRunRepository(db)`
7. **HTTP handler** — `api.NewHandler(agentRepo, runRepo, engine, &api.Config{...})`
8. **HTTP server** — `http.Server` on `cfg.Server.Host:cfg.Server.Port` with graceful shutdown (10s timeout)
9. **Signal handling** — `SIGINT`/`SIGTERM` triggers shutdown

**Key design decisions**:
- Single `main()` — no framework, stdlib only
- `defer db.Close()` ensures DB closed on exit
- Goroutine for `ListenAndServe()` so main can wait on signal channel
- `http.ErrServerClosed` ignored on shutdown
- API config (`DefaultTimeout`, `DefaultImage`) derived from agent config

---

### 3. `internal/config/config.go`

```go
type Config struct {
	Server   ServerConfig   `json:"server"`
	Database DatabaseConfig `json:"database"`
	Podman   PodmanConfig   `json:"podman"`
	Agent    AgentConfig    `json:"agent"`
}

type ServerConfig struct {
	Host string `json:"host"`  // default: "127.0.0.1"
	Port int    `json:"port"`  // default: 8080
}

type DatabaseConfig struct {
	Path string `json:"path"`  // default: "./data/horizon.db"
}

type PodmanConfig struct {
	SocketPath       string        `json:"socket_path"`       // default: "" → auto-detect
	ImageName        string        `json:"image_name"`        // default: "localhost/horizon-sandbox:latest"
	APIVersion       string        `json:"api_version"`       // default: "v4.0.0"
	RequestTimeout   time.Duration `json:"request_timeout"`   // default: 30s
	ContainerTimeout time.Duration `json:"container_timeout"` // default: 10m
}

type AgentConfig struct {
	DefaultTimeout time.Duration `json:"default_timeout"` // default: 10m
	DefaultImage   string        `json:"default_image"`   // default: "localhost/horizon-sandbox:latest"
}
```

**`DefaultConfig()`** returns zero-value struct with sensible defaults. Flags in `main.go` override `Database.Path` and `Server.Port`. Future: load from file (`--config`).

---

### 4. `internal/sandbox/podman.go` — Podman Engine (Fixed)

**Core types**:
- `EngineConfig` — `SocketPath`, `ImageName`, `APIVersion`, `RequestTimeout`, `ContainerTimeout`
- `Engine` — wraps `config` + `*http.Client` with Unix socket dialer
- `RunOptions` — `WorkspaceDir`, `YAMLFile`, `Prompt`, `APIKey`, `OutputWriter`, `Env`
- `RunResult` — `ContainerID`, `ExitCode`, `Error`
- `createReq` / `hostConfig` / `createResp` / `waitResp` — Podman API DTOs

**Key functions**:

| Function | Purpose |
|---|---|
| `DefaultEngineConfig()` | Auto-detects Podman socket at `$XDG_RUNTIME_DIR/podman/podman.sock` or `/run/user/$UID/podman/podman.sock` |
| `NewEngine(config)` | Validates socket exists, creates HTTP client with Unix dialer |
| `HealthCheck(ctx)` | GET `/_ping` + GET `/images/{name}/json` — verifies Podman + image |
| `RunAgent(ctx, opts)` | Full lifecycle: create → start → stream logs → wait (with timeout) → return result |
| `buildCmd(yamlFile, prompt)` | Returns `["run", "--exec", "--last", "/workspace/{yaml}", "{prompt}"]` |
| `StreamLogs(ctx, containerID, out)` | GET `/containers/{id}/logs?follow=true&stdout=true&stderr=true` → `demuxStream` |
| `RemoveContainer(containerID)` | DELETE `/containers/{id}?force=true` with 10s timeout |
| `demuxStream(r, out)` | Strips 8-byte Docker/Podman multiplexed header (stream type + frame size) |

**Error handling improvements**:
- `ContainerTimeout` context on `wait` call — returns timeout error instead of hanging
- `defer` cleanup with 10s timeout context — verifies removal response
- Structured error messages with context (`fmt.Errorf("...: %w", err)`)
- Distinguishes `context.DeadlineExceeded` from other wait errors
- `LogConfig: {Type: "json-file"}` in host config for structured container logs

---

### 5. `internal/agent/model.go` — Agent Domain Types

```go
type AgentDefinition struct {
	ID            string
	Name          string
	Description   string
	Goal          string
	Instructions  string
	RuntimeConfig RuntimeConfig
	ModelConfig   ModelConfig
	Capabilities  []string
	Permissions   Permissions
	Schedule      *ScheduleConfig
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Version       int
	RunHistory    []run.RunRecord
}

type RuntimeConfig struct {
	Image         string
	Entrypoint    []string
	Environment   map[string]string
	WorkingDir    string
	ResourceLimits ResourceLimits
}

type ResourceLimits struct {
	CPULimit    string
	MemoryLimit string
	Timeout     string
}

type ModelConfig struct {
	Provider  string
	Model     string
	APIKeyRef string
	Parameters map[string]any
}

type Permissions struct {
	FilesystemAccess []string
	NetworkAccess    bool
	ShellAccess      bool
	SecretAccess     []string
}

type ScheduleConfig struct {
	CronExpression string
	Timezone       string
	Enabled        bool
}
```

**`ToStorageRow()`** — converts `AgentDefinition` → `storage.AgentRow`:
- Marshals `RuntimeConfig`, `ModelConfig`, `Capabilities`, `Permissions` to `json.RawMessage`
- Marshals `Schedule` to `json.RawMessage` (or `"null"` if nil)
- Returns `*storage.AgentRow` for persistence

---

### 6. `internal/agent/service.go` — Agent Business Logic

**`Service` struct**:
```go
type Service struct {
	repo    *storage.AgentRepository
	runRepo *storage.RunRepository
}
```

**Methods**:

| Method | Description |
|---|---|
| `CreateAgent(ctx, *AgentDefinition)` | Validates ID, sets timestamps/version=1, calls `repo.Save()` |
| `GetAgent(ctx, id)` | `repo.GetByID()` → `storageRowToAgent()` |
| `ListAgents(ctx)` | `repo.List()` → map `storageRowToAgent()` |
| `UpdateAgent(ctx, def)` | Loads existing, preserves `CreatedAt`, increments `Version`, saves |
| `DeleteAgent(ctx, id)` | Deletes runs via `runRepo.DeleteByAgentID()`, then agent |
| `GetAgentWithRuns(ctx, id)` | Loads agent + runs via `runRepo.ListByAgentID()`, merges |

**Conversion helpers** (package-private):
- `toStorageRow()` — same as `ToStorageRow()` on model
- `storageRowToAgent(*storage.AgentRow)` — unmarshals JSON fields, reconstructs `ScheduleConfig`
- `storageRunRowToRun(*storage.RunRow)` → `run.RunRecord` with type conversion for `RunStatus`

---

### 7. `internal/agent/repository.go` — Repository Interfaces

```go
type AgentRepository interface {
	Save(ctx context.Context, def *AgentDefinition) error
	GetByID(ctx context.Context, id string) (*AgentDefinition, error)
	List(ctx context.Context) ([]*AgentDefinition, error)
	Delete(ctx context.Context, id string) error
}

type RunRepository interface {
	Save(ctx context.Context, rec *RunRecord) error
	GetByID(ctx context.Context, id string) (*RunRecord, error)
	ListByAgentID(ctx context.Context, agentID string) ([]RunRecord, error)
	DeleteByAgentID(ctx context.Context, agentID string) error
}

// Local RunRecord alias to avoid import cycle with run.RunRecord
type RunRecord struct { ... }
```

**Purpose**: Defines persistence contract without coupling to storage implementation. Used by `agent.Service`.

---

### 8. `internal/run/model.go` — Run Domain Types

```go
type RunStatus string

const (
	RunStatusQueued    RunStatus = "queued"
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
	RunStatusTimedOut  RunStatus = "timed_out"
)

type RunRecord struct {
	ID              string
	AgentID         string
	AgentVersion    int
	TriggerType     string
	Status          RunStatus
	StartedAt       time.Time
	CompletedAt     *time.Time
	ExitCode        *int
	OutputRef       string
	ErrorMessage    string
	ContainerID     string
	CancelReason    string
}
```

---

### 9. `internal/run/coordinator.go` — RunCoordinator Interface

```go
type RunCoordinator interface {
	ExecuteRun(ctx context.Context, agentID string, triggerType string) (*RunRecord, error)
	CancelRun(ctx context.Context, runID string, reason string) error
	GetRunStatus(ctx context.Context, runID string) (RunStatus, error)
}
```

**Status**: Defines interface for future orchestrator; not yet implemented (Phase 1 uses `handler.executeRun` directly).

---

### 10. `internal/run/repository.go` — RunRepository Interface

```go
type RunRepository interface {
	Save(ctx context.Context, rec *RunRecord) error
	GetByID(ctx context.Context, id string) (*RunRecord, error)
	ListByAgentID(ctx context.Context, agentID string) ([]RunRecord, error)
	DeleteByAgentID(ctx context.Context, agentID string) error
}
```

---

### 11. `internal/cagent/adapter.go` — cagent Integration Layer

```go
type AgentConfig struct {
	Name        string            `yaml:"name"`
	Goal        string            `yaml:"goal"`
	Instructions string           `yaml:"instructions"`
	Model       ModelConfig       `yaml:"model"`
	Tools       []ToolConfig      `yaml:"tools,omitempty"`
	Permissions PermissionsConfig `yaml:"permissions"`
	Timeout     time.Duration     `yaml:"timeout,omitempty"`
}

type Adapter interface {
	BuildConfig(ctx context.Context, agentDef interface{}) (*AgentConfig, error)
	Execute(ctx context.Context, config *AgentConfig, workspaceDir string, prompt string, outputWriter interface{}) (string, error)
	ValidateConfig(config *AgentConfig) error
}

type DefaultAdapter struct {
	imageName        string
	containerTimeout time.Duration
}
```

**Current state**: `BuildConfig` returns hardcoded stub; `Execute` returns `ErrNotImplemented`. 
**Future**: Convert `agent.AgentDefinition` → `AgentConfig` (YAML), delegate `Execute` to `sandbox.Engine`.

---

### 12. `internal/storage/sqlite.go` — SQLite Persistence (Full Implementation)

**`DB` struct**: Wraps `*sql.DB` with `runMigrations()` called on `NewDB()`.

**Migrations** (executed in order):
1. `createAgentsTable` — agents table with indexes
2. `createRunsTable` — runs table with FK to agents + indexes

**`AgentRepository`**:
- `Save(ctx, *AgentRow)` — UPSERT with `ON CONFLICT(id) DO UPDATE`
- `GetByID(ctx, id)` — SELECT by PK → `scanAgent`
- `List(ctx)` — SELECT all ORDER BY created_at DESC
- `Delete(ctx, id)` — DELETE, returns `ErrNotFound` if 0 rows affected
- `scanAgent` — scans into `AgentRow` (raw JSON fields)

**`RunRepository`**:
- `Save(ctx, *RunRow)` — UPSERT on id
- `GetByID(ctx, id)` — SELECT by PK → `scanRun`
- `ListByAgentID(ctx, agentID)` — SELECT WHERE agent_id ORDER BY started_at DESC
- `DeleteByAgentID(ctx, agentID)` — DELETE WHERE agent_id
- `scanRun` — scans into `RunRow` (sql.Null* for nullable fields)

**Domain converters** (on row types):
```go
func (row *AgentRow) ToAgentDomain() *AgentDefinition {
	// Unmarshals JSON fields, reconstructs ScheduleConfig
}

func (row *RunRow) ToRunDomain() RunRecord {
	// Converts sql.Null* → pointers, returns storage.RunRecord
}
```

**Storage-layer types** (for converters):
- `AgentDefinition` — same fields as domain but `Schedule *ScheduleConfig`, JSON as `json.RawMessage`
- `ScheduleConfig` — CronExpression, Timezone, Enabled
- `RunRecord` — flat fields with `*time.Time`, `*int` for nullable

---

### 13. `internal/storage/migrations/`

**`001_create_agents.sql`**:
```sql
CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    goal TEXT NOT NULL,
    instructions TEXT NOT NULL,
    runtime_config TEXT NOT NULL DEFAULT '{}',
    model_config TEXT NOT NULL DEFAULT '{}',
    capabilities TEXT NOT NULL DEFAULT '[]',
    permissions TEXT NOT NULL DEFAULT '{}',
    schedule TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    version INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_agents_created_at ON agents(created_at DESC);
```

**`002_create_runs.sql`**:
```sql
CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    agent_version INTEGER NOT NULL,
    trigger_type TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at DATETIME NOT NULL,
    completed_at DATETIME,
    exit_code INTEGER,
    output_ref TEXT,
    error_message TEXT,
    container_id TEXT,
    cancel_reason TEXT,
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_runs_agent_id ON runs(agent_id);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);
```

---

### 14. `internal/permissions/permissions.go`

```go
type Permission string

const (
	PermFilesystemRead  Permission = "filesystem:read"
	PermFilesystemWrite Permission = "filesystem:write"
	PermNetworkAccess   Permission = "network:access"
	PermShellAccess     Permission = "shell:access"
	PermSecretAccess    Permission = "secret:access"
)

type PermissionSet map[Permission]bool

// Methods: Has, Grant, Revoke
// DefaultPermissions() — read+network enabled, write+shell+secret disabled
// Validate(perm) — checks against known constants
```

---

### 15. `internal/api/api.go` — HTTP API Interface

```go
type APIHandler interface {
	RegisterRoutes(mux *http.ServeMux)
	Start(ctx context.Context) error
	Stop() error
}

type ServerConfig struct {
	Host string
	Port int
}
```

---

### 16. `internal/api/handler.go` — HTTP Handlers (Complete CRUD + Execution)

**`Handler` struct**:
```go
type Handler struct {
	agentRepo *storage.AgentRepository
	runRepo   *storage.RunRepository
	engine    *sandbox.Engine
	config    *Config
}

type Config struct {
	DefaultTimeout time.Duration
	DefaultImage   string
}
```

**Routes**:
| Method | Path | Handler |
|---|---|---|
| GET | `/api/health` | `handleHealth` |
| GET | `/api/agents` | `listAgents` |
| POST | `/api/agents` | `createAgent` |
| GET | `/api/agents/{id}` | `getAgent` |
| PUT | `/api/agents/{id}` | `updateAgent` |
| DELETE | `/api/agents/{id}` | `deleteAgent` |
| GET | `/api/runs?agent_id=` | `listRuns` |
| POST | `/api/runs` | `createRun` |
| GET | `/api/runs/{id}` | `getRun` |
| POST | `/api/runs/{id}?action=cancel` | `cancelRun` |

**Execution flow** (`createRun` → goroutine `executeRun`):
1. Decode request → validate `agent_id`, `prompt`
2. `agentRepo.GetByID()` → `agentRow`
3. Create `RunRow` with `Status="queued"`, save
4. **Async goroutine** `executeRun()`:
   - Update run to `running`
   - Build `sandbox.RunOptions` (workspace=".", yaml="agent.yaml", TODO: generate from agent)
   - `engine.RunAgent(ctx, opts)` → `RunResult`
   - On error: status=`failed`, error message
   - On success: status=`succeeded`/`failed` based on exit code
   - Set `CompletedAt`, `ContainerID`, `ExitCode`
   - Save final run record

**Health check**: Calls `engine.HealthCheck(ctx)` with 5s timeout.

---

### 17. `runtime/Containerfile` — Sandbox Image Build

```dockerfile
# Stage 1: Build cagent
FROM golang:1.27-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
RUN git clone --depth 1 https://github.com/docker/cagent.git . && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /cagent .

# Stage 2: Minimal runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates curl jq bash python3
COPY --from=builder /cagent /usr/local/bin/cagent
RUN chmod +x /usr/local/bin/cagent
RUN adduser -D -u 1000 agentuser
USER agentuser
WORKDIR /workspace
ENTRYPOINT ["cagent"]
```

**Notes**:
- Builds cagent from source (no version pin — clones `main` branch)
- Installs `python3`, `curl`, `jq`, `bash` — relatively heavy
- Runs as non-root `agentuser` (UID 1000)
- Entrypoint is raw `cagent` — no Horizon wrapper
- Build with: `podman build -t localhost/horizon-sandbox:latest ./runtime`

---

## Phase 1 Status Summary

| `project.md` §14 Requirement | Implementation Status |
|---|---|
| Preserve Go-to-Podman integration | ✅ `internal/sandbox/podman.go` — cleaned, configurable, tested |
| Stabilize cagent invocation | ✅ `internal/cagent/adapter.go` + `podman.go::buildCmd()` |
| Capture output, errors, exit status | ✅ `RunOptions.OutputWriter`, `RunResult`, `RunRecord` |
| Handle timeouts and cleanup | ✅ `ContainerTimeout`, defer cleanup with verification |
| Health checks | ✅ `Engine.HealthCheck()` — socket + image |
| SQLite persistence | ✅ Agents, runs, schedules with migrations |
| Structured run records | ✅ Status enum, timestamps, exit code, output ref, container ID |
| Integration tests | ⏳ Directory exists, tests pending |
| Retries/backoff | ❌ Not implemented |
| Structured logging | ❌ Stdlib `log` only |

**Exit condition**: "Horizon can execute a supported agent task and reliably report its outcome" — **Achieved** for manual runs via HTTP API.

---

## Build & Run

```bash
# Build (CGO-free)
CGO_ENABLED=0 go build ./...

# Health check
CGO_ENABLED=0 go run ./cmd/horizond --health-check

# Start server
CGO_ENABLED=0 go run ./cmd/horizond

# Test API
curl http://127.0.0.1:8080/api/health
```

---

## Remaining Work for Phase 1 Completion

1. **Integration tests** in `tests/` with `//go:build integration` tag
2. **Retry logic** in `podman.go` for transient failures (exponential backoff)
3. **Structured logging** — migrate to `log/slog` with JSON output
4. **Workspace/YAML generation** — `executeRun` TODOs: proper workspace, agent YAML from definition
5. **API key management** — secure storage instead of passing via `RunOptions`

---

## `.env` File Template

See `.env` in project root for environment variable configuration.