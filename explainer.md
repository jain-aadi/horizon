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
│   │   ├── coordinator.go          # RunCoordinator lifecycle contract
│   │   ├── repository.go           # Domain repository + SQLite adapter
│   │   └── service.go              # Per-run materialization, execution, output, cancellation
│   ├── scheduler/                  # Empty — Phase 4 (scheduling, cron, pause/resume)
│   ├── cagent/                     # cagent integration layer
│   │   └── adapter.go              # Adapter interface + DefaultAdapter (BuildConfig, ValidateConfig, Execute stub)
│   ├── storage/                    # SQLite persistence
│   │   ├── sqlite.go               # DB wrapper, transactional versioned migrations, repositories, scan functions
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
├── go.mod                          # Module: github.com/jain-aadi/horizon, Go 1.26.0
├── go.sum                          # Dependency checksums
├── project.md                      # Product spec and roadmap
├── run.md                          # Operations guide (health check, curl examples, capabilities, troubleshooting)
├── explainer.md                    # This file
└── .env                            # Optional local environment overrides (ignored by Git)
```

---

## Detailed File Documentation

### 1. `go.mod`

```go
module github.com/jain-aadi/horizon

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

- **Module path**: `github.com/jain-aadi/horizon` (canonical import path)
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

	"github.com/jain-aadi/horizon/internal/api"
	"github.com/jain-aadi/horizon/internal/config"
	"github.com/jain-aadi/horizon/internal/sandbox"
	"github.com/jain-aadi/horizon/internal/storage"
)
```

**Responsibilities**:
1. **Flag parsing** — `db` (database path, default `./data/horizon.db`), `port` (default 8080), `health-check` (bool)
2. **Config loading** — `config.DefaultConfig()` with flag overrides
3. **Database init** — `storage.NewDB(path)` creates a missing parent directory for file-backed databases, enables foreign keys, and runs migrations automatically
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

**`DefaultConfig()`** returns a configuration with sensible defaults. Optional `.env` values, inherited environment variables, and explicitly supplied `--db` / `--port` flags override those defaults in that order. File-based configuration is not implemented and no `--config` flag is exposed.

`LoadFromEnv()` configures `HORIZON_DB_PATH`, `HORIZON_PORT`, `HORIZON_SOCKET`,
`HORIZON_IMAGE`, `HORIZON_REQUEST_TIMEOUT`, `HORIZON_CONTAINER_TIMEOUT`, and
`HORIZON_AGENT_TIMEOUT`. It rejects a nonnumeric or out-of-range port and zero,
negative, or invalid timeout durations. The daemon loads the optional `.env` file
before invoking `LoadFromEnv()`; the loader accepts unquoted `KEY=VALUE` lines and
does not overwrite a value inherited from the process environment. `HORIZON_AGENT_TIMEOUT`
bounds every materialized agent run through its execution context.

---

### 4. `internal/sandbox/podman.go` — Podman Engine (Fixed)

**Core types**:
- `EngineConfig` — `SocketPath`, `ImageName`, `APIVersion`, `RequestTimeout`, `ContainerTimeout`
- `Engine` — wraps `config` + `*http.Client` with Unix socket dialer
- `RunOptions` — `WorkspaceDir`, `YAMLFile`, `Prompt`, `APIKey`, `OutputWriter`, `Env`, `OnContainerCreated`
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
- Returns `(*storage.AgentRow, error)` so serialization failures are not discarded

**Validation and defaults:** `ApplyDefaults()` trims user-facing fields, normalizes
the model provider, and supplies the configured runtime image when omitted.
`Validate()` requires a name, goal, instructions, a whitespace-free runtime image,
and the initial supported cagent model contract: provider `google` plus a model name.
Validation errors identify a stable field name and message for HTTP clients.

---

### 6. `internal/agent/service.go` — Agent Business Logic

**`Service` struct**:
```go
type Service struct {
	repo         *storage.AgentRepository
	runRepo      *storage.RunRepository
	defaultImage string
}
```

**Methods**:

| Method | Description |
|---|---|
| `CreateAgent(ctx, *AgentDefinition)` | Applies defaults, validates fields, assigns a UUID and UTC timestamps, sets version `1`, then saves |
| `GetAgent(ctx, id)` | `repo.GetByID()` → `storageRowToAgent()` |
| `ListAgents(ctx)` | `repo.List()` → map `storageRowToAgent()` |
| `UpdateAgent(ctx, def)` | Applies defaults and validation, loads existing, preserves `CreatedAt`, increments `Version`, saves |
| `DeleteAgent(ctx, id)` | Deletes runs via `runRepo.DeleteByAgentID()`, then agent |
| `GetAgentWithRuns(ctx, id)` | Loads agent + runs via `runRepo.ListByAgentID()`, merges |

**Conversion helpers** (package-private):
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
	StartRun(ctx context.Context, agentID string, triggerType string, prompt string) (*RunRecord, error)
	CancelRun(ctx context.Context, runID string, reason string) error
	GetRunStatus(ctx context.Context, runID string) (RunStatus, error)
	GetRun(ctx context.Context, runID string) (*RunRecord, error)
	ReadOutput(ctx context.Context, runID string) ([]byte, error)
}
```

`Service` implements this contract. It persists a `queued` row before launching a
background worker, snapshots the selected agent ID/version, materializes a unique
Docker Agent YAML workspace, captures bounded output, and persists a single terminal
outcome. It protects cancellation with an in-memory active-run registry so a late
worker completion cannot overwrite `cancelled`.

---

### 10. `internal/run/repository.go` — Run Repository Interface

```go
type RunRepository interface {
	Save(ctx context.Context, rec *RunRecord) error
	GetByID(ctx context.Context, id string) (*RunRecord, error)
	ListByAgentID(ctx context.Context, agentID string) ([]*RunRecord, error)
	DeleteByAgentID(ctx context.Context, agentID string) error
}
```

`StorageRepository` adapts `storage.RunRepository` rows to this domain interface, so
run lifecycle code no longer performs SQL-null conversions in HTTP handlers.

---

### 11. `internal/cagent/adapter.go` — Docker Agent Configuration Boundary

```go
type Adapter interface {
	BuildConfig(agentDef *agent.AgentDefinition) (*Config, error)
	MarshalConfig(config *Config) ([]byte, error)
	ValidateConfig(config *Config) error
}
```

The adapter targets Docker Agent `v1.158.0`, immutable commit
`7f94f376a5d10320591d959b351ca1eeb07c959a`, and YAML configuration schema version
`16`. It maps a validated Horizon definition to a single `agents.root` entry and a
single generated `models.horizon-model` definition. The root instruction contains both
the Horizon goal and instructions; `description` uses the saved description or falls
back to the agent name.

The configuration is serialized through `gopkg.in/yaml.v3`, then validated before it
is returned. The adapter deliberately rejects capabilities, filesystem/network/shell/
secret permissions, model API-key references, and arbitrary model parameters: Docker
Agent YAML alone cannot enforce Horizon's execution policy. Credentials remain outside
the YAML and are supplied by the controlled execution environment. The run service
calls this adapter for every accepted run and writes the resulting configuration to the
run's isolated workspace before container creation.

---

### 12. `internal/storage/sqlite.go` — SQLite Persistence (Full Implementation)

**`DB` struct**: Wraps `*sql.DB` with `runMigrations()` called on `NewDB()`. Ordinary file paths receive a missing parent directory automatically; `:memory:` and SQLite memory URIs do not create directories. The connection enables SQLite foreign-key enforcement before application queries run.

**Migrations**:
1. SQL files under `internal/storage/migrations/` are the canonical schema source and are embedded into the binary at build time.
2. `schema_migrations` records an integer version, migration name, and UTC application timestamp.
3. Pending migrations are applied and recorded in one transaction, in ascending version order. Reopening an existing database leaves already recorded migrations untouched and preserves durable data.
4. Existing pre-version-table databases remain compatible because the initial migrations use `CREATE TABLE IF NOT EXISTS` before their versions are recorded.

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

The files above are embedded by `sqlite.go`; they are not duplicated as Go string
constants. Add future changes as a new, additive, numerically ordered migration file.

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

### 16. `internal/api/handler.go` — HTTP Handlers (Complete CRUD + Coordinated Execution)

**`Handler` struct**:
```go
type Handler struct {
	agentService *agent.Service
	runRepo      *storage.RunRepository
	runService   run.RunCoordinator
	engine       Engine
	config       *Config
}

type Config struct {
	DefaultTimeout time.Duration
	DefaultImage   string
	DataDir        string
	MaxOutputBytes int64
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
| GET | `/api/runs/{id}/output` | `getRunOutput` |
| POST | `/api/runs/{id}?action=cancel` | `cancelRun` |

**Execution flow** (`createRun` → `run.Service.StartRun`):
1. Decode and validate `agent_id` and `prompt`; generic request-provided environment is not accepted.
2. Load the saved agent, persist a `queued` run with its agent version, and return HTTP `202`.
3. Background work writes `<database-directory>/runs/<RUN_ID>/agent.yaml` using the pinned adapter and creates bounded `output.log` alongside it.
4. The service transitions to `running`, invokes `Engine.RunAgent`, records the container ID at creation, and writes streamed logs to `output.log`.
5. It persists `succeeded`, `failed`, `timed_out`, or `cancelled` once with completion time, exit code, error, and output reference.
6. Cancellation persists `cancelled`, cancels the active context, and requests removal of a known container. The output route validates the stored relative reference remains under the artifact root before reading.

**Health check**: Calls `engine.HealthCheck(ctx)` with 5s timeout.

---

### 17. `runtime/Containerfile` — Sandbox Image Build

```dockerfile
# Stage 1: Build the pinned Docker Agent release
FROM golang:1.27-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
ARG DOCKER_AGENT_VERSION=v1.158.0
ARG DOCKER_AGENT_COMMIT=7f94f376a5d10320591d959b351ca1eeb07c959a
RUN git clone --depth 1 --branch "${DOCKER_AGENT_VERSION}" https://github.com/docker/docker-agent.git . && \
    test "$(git rev-parse HEAD)" = "${DOCKER_AGENT_COMMIT}" && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /docker-agent .

# Stage 2: Minimal runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates curl jq bash python3
COPY --from=builder /docker-agent /usr/local/bin/docker-agent
RUN chmod +x /usr/local/bin/docker-agent
RUN adduser -D -u 1000 agentuser
USER agentuser
WORKDIR /workspace
ENTRYPOINT ["docker-agent"]
```

**Notes**:
- Builds Docker Agent v1.158.0 and fails the image build unless the checkout is commit `7f94f376a5d10320591d959b351ca1eeb07c959a`
- Installs `python3`, `curl`, `jq`, `bash` — relatively heavy
- Runs as non-root `agentuser` (UID 1000)
- Entrypoint is `docker-agent`; Podman supplies `run --exec --last /workspace/<agent.yaml> <prompt>`
- Build with: `podman build -t localhost/horizon-sandbox:latest ./runtime`

---

## Phase 1 Status Summary

| `project.md` §14 Requirement | Implementation Status |
|---|---|
| Preserve Go-to-Podman integration | ✅ `internal/sandbox/podman.go` — cleaned, configurable, tested |
| Pin and validate Docker Agent configuration | ✅ `internal/cagent/adapter.go` + `workspace/test-agent.yaml` |
| Materialize saved agent configuration for execution | ✅ `run.Service` writes isolated, adapter-generated per-run YAML |
| Capture output, errors, exit status | ✅ Bounded `output.log`, `RunResult`, and durable `RunRecord` fields |
| Handle timeouts and cancellation | ✅ Agent timeout context, cancellation registry, known-container removal request, and terminal-state protection |
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