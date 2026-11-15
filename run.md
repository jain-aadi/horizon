# Horizon — Run, Test, and Capabilities Guide

## Prerequisites

| Item | Requirement |
|---|---|
| Go | `go 1.26.0` (project `go.mod`) |
| Podman | Rootless Podman with socket at `$XDG_RUNTIME_DIR/podman/podman.sock` (default) |
| Credentials | `GOOGLE_API_KEY` environment variable exported before running |
| Agent runtime | Docker Agent v1.158.0 at commit `7f94f376a5d10320591d959b351ca1eeb07c959a`, built by `runtime/Containerfile` |
| Database | `./data/horizon.db` will be created automatically on first start |
| Port | API listens on `127.0.0.1:8080` by default |

## Running the Server

### 1. Quick health check (no image build needed)

```bash
CGO_ENABLED=0 go run ./cmd/horizond --health-check
```

Expected output:
```
Health check passed
```

This verifies:
- Podman socket is reachable
- The configured sandbox image exists (or will be built on first run)

### 2. Start the HTTP API server

```bash
CGO_ENABLED=0 go run ./cmd/horizond
```

Server starts and logs:
```
Starting Horizon server on 127.0.0.1:8080
```

The server runs until interrupted (Ctrl+C). Graceful shutdown is handled.

### 3. Using environment variables

| Variable | Default | Description |
|---|---|---|
| `GOOGLE_API_KEY` | _(required)_ | API key for Google Gemini provider used by Docker Agent |
| `HORIZON_DB_PATH` | `./data/horizon.db` | Path to SQLite database |
| `HORIZON_PORT` | `8080` | HTTP server port |
| `HORIZON_SOCKET` | `$XDG_RUNTIME_DIR/podman/podman.sock` | Podman socket path |
| `HORIZON_IMAGE` | `localhost/horizon-sandbox:latest` | Container image for agent execution |
| `HORIZON_REQUEST_TIMEOUT` | `30s` | Podman API request timeout |
| `HORIZON_CONTAINER_TIMEOUT` | `10m` | Maximum duration of one container execution |
| `HORIZON_AGENT_TIMEOUT` | `10m` | Maximum duration of one materialized agent run |

Configuration precedence is: built-in defaults, optional `.env` entries, inherited
process environment, then explicitly supplied `--db` and `--port` flags. The `.env`
file supports one unquoted `KEY=VALUE` entry per line; blank lines, `#` comments,
and malformed lines are ignored. Timeout values use Go duration syntax and must be
greater than zero, for example `30s` or `10m`. Startup fails when `HORIZON_PORT` is
not an integer from 1 through 65535 or a configured timeout is invalid.

### 4. Pinned Docker Agent contract

The sandbox image builds Docker Agent v1.158.0 and verifies the tag resolves to
commit `7f94f376a5d10320591d959b351ca1eeb07c959a`. The runtime command is
`docker-agent run --exec --last /workspace/<agent.yaml> <prompt>`; the existing
Podman engine supplies the `run --exec --last` arguments after the image entrypoint.

The execution coordinator materializes the saved agent definition for every accepted
run into `<database-directory>/runs/<RUN_ID>/agent.yaml`. It uses the Task 5 adapter's
schema-`16` YAML with one `root` agent and one Google model; no shared project-root
`agent.yaml` is used. Unsupported capabilities, filesystem/network/shell/secret
permission requests, model API-key references, and arbitrary model parameters fail the
run during materialization rather than silently weakening the requested policy.

The same workspace holds a bounded `output.log`. The run row stores only a safe
relative output reference, and `GET /api/runs/{id}/output` resolves it under the run
artifact root before reading. `POST /api/runs/{id}?action=cancel` persists cancellation,
cancels the active context, and requests removal of the known container.

### 5. Test with curl

```bash
# Health check
curl http://127.0.0.1:8080/api/health

# Create an agent
curl -X POST http://127.0.0.1:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{"name":"test-agent","goal":"Report status","instructions":"Say hello","model_config":{"provider":"google","model":"gemini-3.5-flash-lite"}}'

# List agents
curl http://127.0.0.1:8080/api/agents

# Create a run
curl -X POST http://127.0.0.1:8080/api/runs \
  -H "Content-Type: application/json" \
  -d '{"agent_id":"<AGENT_ID>","prompt":"Report your status"}'

# List runs for an agent
curl "http://127.0.0.1:8080/api/runs?agent_id=<AGENT_ID>"

# Inspect output or cancel an active run
curl http://127.0.0.1:8080/api/runs/<RUN_ID>/output
curl -X POST "http://127.0.0.1:8080/api/runs/<RUN_ID>?action=cancel" \
  -H "Content-Type: application/json" \
  -d '{"reason":"No longer needed"}'
```

Agent creation requires `name`, `goal`, `instructions`, and a Google
`model_config.provider` / `model_config.model`. Omit `runtime_config.image` to use
the configured default image. Validation failures return HTTP `400` with JSON in the
form `{"error":"validation_error","field":"...","message":"..."}`.

---

## Capabilities (Phase 1)

### ✅ Covered

| Category | Details |
|---|---|
| **Agent lifecycle** | Create, list, get, update, delete agents via HTTP API |
| **Run execution** | Manual trigger materializes a saved definition into a per-run Docker Agent YAML workspace, then executes it in Podman |
| **Persistence** | SQLite stores agent definitions, run history, schedules across restarts |
| **Run status** | `queued` → `running` → `succeeded`/`failed`/`cancelled`/`timed_out` |
| **Container management** | Auto-create, start, stream bounded output, wait for exit, and request removal on cancellation; engine cleanup also removes containers |
| **Health checks** | Podman socket reachability + image existence check |
| **Cancel runs** | Active runs are durably cancelled via `POST /api/runs/{id}?action=cancel`; late completion cannot replace `cancelled` |
| **Configuration** | `.env`, process environment, and explicit `--db` / `--port` overrides; database path, image, socket path, API timeout, and container timeout are applied |
| **HTTP API** | RESTful endpoints: `/api/agents`, `/api/runs`, `/api/health` |

### ❌ Not Yet Covered (Phase 2+)

| Category | Notes |
|---|---|
| **Scheduled runs** | `scheduler/` directory exists but no persistence or cron integration yet |
| **Retry / backoff** | No automatic retries on transient Podman failures |
| **Agent templates** | No reusable agent templates or duplication |
| **Advanced permissions** | Basic permission set only; fine-grained control not implemented |
| **Multiple model providers** | Currently only Google Gemini via `GOOGLE_API_KEY` |
| **Web UI** | `web/` directory exists; UI not yet built |
| **Integration tests** | `tests/` exists; no tests written yet (require Podman socket) |
| **Docker compatibility** | Image `localhost/horizon-sandbox:latest` must be built/pulled separately |
| **Volume mounts beyond /workspace** | Only `/workspace:ro,Z` mount is configured; no additional host paths |
| **Resource limits** | CPU/memory limits configurable but not enforced in current sandbox |
| **Network access inside container** | `slirp4netns` provides user-mode NAT egress, which Google model calls require; Horizon does not yet enforce `network_access` or outbound-destination restrictions |
| **Secret management** | `GOOGLE_API_KEY` passed as env var; no secret vault integration |
| **CLI** | No CLI binary built yet (only HTTP API; `horizond` and `horizond.exe` binaries exist from prior build) |

---

## Building the Sandbox Image (One-time)

The `runtime/Containerfile` defines how to build the sandbox image:

```bash
docker build -t localhost/horizon-sandbox:latest ./runtime
```

Or with Podman:

```bash
podman build -t localhost/horizon-sandbox:latest ./runtime
```

**Note:** The image contains `cagent` (Docker Agent) and minimal runtime tools. It is **not** built automatically — the `Containerfile` is provided for reference and future use. The server can run without the image if only health-check mode is used, but agent execution will fail until the image is available.

---

## Running Integration Tests (If Podman is Available)

```bash
# Ensure Podman socket and image are available
podman system connection list
podman images | grep horizon-sandbox

# Run tests with build tag
CGO_ENABLED=0 go test ./... -tags integration -count=1
```

Tests require:
- Podman socket reachable at default path
- `localhost/horizon-sandbox:latest` image available
- `GOOGLE_API_KEY` set

---

## Development Workflow

```bash
# 1. Lint / build
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go build ./...

# 2. Run health check
CGO_ENABLED=0 go run ./cmd/horizond --health-check

# 3. Start server in background
CGO_ENABLED=0 go run ./cmd/horizond &
SERVER_PID=$!

# 4. Run curl tests
# ... tests ...

# 5. Stop server
kill $SERVER_PID
```

---

## What Happens on First Run

1. The parent directory for SQLite database `./data/horizon.db` is created when needed; versioned schema migrations are applied and recorded in `schema_migrations`
2. Agent and run repositories initialize
3. Podman engine connects to socket (health check verifies)
4. HTTP API serves on `127.0.0.1:8080`
5. All subsequent state (agents, runs) persists in SQLite

---

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---|---|---|
| `podman socket not found` | Podman not running or socket path wrong | Start Podman: `systemctl --user start podman.socket`; check `$XDG_RUNTIME_DIR` |
| `container create failed (500)` | Image not found | Build: `podman build -t localhost/horizon-sandbox:latest ./runtime` |
| `GOOGLE_API_KEY` empty | No API key set | Export: `export GOOGLE_API_KEY=your_key_here` |
| `container wait timeout` | Agent takes longer than 10 min | Increase `HORIZON_CONTAINER_TIMEOUT` env var, or optimize agent prompt |
| `500 Internal Server Error` on `/api/health` | Podman unreachable | Ensure Podman socket exists and is accessible |