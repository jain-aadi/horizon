# Horizon

A local-first, no-code AI agent platform built in Go. Create, configure, run, and manage AI agents using natural language — all running locally on your machine.

## Architecture

| Component | Technology |
|-----------|------------|
| Application Core | Go 1.26 (stdlib only) |
| Agent Runtime | [Docker Agent](https://github.com/docker/docker-agent) v1.158.0, pinned to commit `7f94f376a5d10320591d959b351ca1eeb07c959a` |
| Container Backend | Rootless Podman |
| Persistence | SQLite via `modernc.org/sqlite` (CGO-free) |
| API | RESTful HTTP (JSON) |

## Quick Start

### Prerequisites

- **Go 1.26+**
- **Rootless Podman** with socket at `$XDG_RUNTIME_DIR/podman/podman.sock`
- **GOOGLE_API_KEY** environment variable (for Gemini provider)

```bash
# Install Podman (Ubuntu/Debian/WSL)
sudo apt update && sudo apt install -y podman
systemctl --user start podman.socket
systemctl --user enable podman.socket

# Build sandbox image (one-time)
podman build -t localhost/horizon-sandbox:latest ./runtime

# Set API key
export GOOGLE_API_KEY=your_key_here
```

### Run the Server

```bash
# Health check (verifies Podman + image)
CGO_ENABLED=0 go run ./cmd/horizond --health-check

# Start HTTP API server
CGO_ENABLED=0 go run ./cmd/horizond
# → Starting Horizon server on 127.0.0.1:8080
```

### Test the API

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

# Inspect one run, retrieve its bounded local output, or cancel it
curl http://127.0.0.1:8080/api/runs/<RUN_ID>
curl http://127.0.0.1:8080/api/runs/<RUN_ID>/output
curl -X POST "http://127.0.0.1:8080/api/runs/<RUN_ID>?action=cancel" \
  -H "Content-Type: application/json" \
  -d '{"reason":"No longer needed"}'
```

Agent creation requires `name`, `goal`, `instructions`, and a Google
`model_config.provider` / `model_config.model`. If `runtime_config.image` is omitted,
Horizon applies the configured default image. Validation failures return HTTP `400`
with JSON in the form `{"error":"validation_error","field":"...","message":"..."}`.

## Docker Agent Configuration Contract

The execution coordinator materializes every accepted run into its own local workspace
under `<database-directory>/runs/<RUN_ID>/`. It converts the saved agent definition to
the pinned Docker Agent v1.158.0 schema-`16` YAML through `gopkg.in/yaml.v3`, then runs
that exact generated `agent.yaml` in Podman. It never uses a shared project-root YAML.

Capabilities, filesystem/network/shell/secret permissions, model API-key references,
and arbitrary model parameters are rejected during run materialization rather than
silently represented in YAML. Credentials continue to come only from the daemon's
execution environment, never from HTTP input or generated agent YAML.

Run logs are captured to a bounded local `output.log` in that workspace. The database
stores only a safe relative output reference; retrieve it through
`GET /api/runs/{id}/output`, which validates containment before reading. Cancellation
persists `cancelled`, cancels the active context, and requests removal of a known
container; late worker results cannot overwrite that terminal state.

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `GOOGLE_API_KEY` | *required* | Google Gemini API key |
| `HORIZON_DB_PATH` | `./data/horizon.db` | SQLite database path |
| `HORIZON_PORT` | `8080` | HTTP server port |
| `HORIZON_SOCKET` | `$XDG_RUNTIME_DIR/podman/podman.sock` | Podman socket path |
| `HORIZON_IMAGE` | `localhost/horizon-sandbox:latest` | Sandbox container image |
| `HORIZON_REQUEST_TIMEOUT` | `30s` | Podman API request timeout |
| `HORIZON_CONTAINER_TIMEOUT` | `10m` | Maximum duration of one container execution |
| `HORIZON_AGENT_TIMEOUT` | `10m` | Maximum duration of one materialized agent run |

At startup, Horizon applies configuration in this order: built-in defaults, optional
`.env` entries, inherited process environment, then explicitly supplied `--db` and
`--port` flags. The `.env` file supports one unquoted `KEY=VALUE` entry per line;
blank lines, `#` comments, and malformed lines are ignored. Timeout values use Go
duration syntax and must be greater than zero (for example, `30s` or `10m`). Horizon
exits at startup when `HORIZON_PORT` is not an integer from 1 through 65535 or a
configured timeout is invalid.

## Storage Startup and Upgrades

For a normal file path, Horizon creates a missing parent directory before opening
SQLite. Startup applies the versioned SQL migrations embedded from
`internal/storage/migrations/` and records each successful version in
`schema_migrations`. Existing data is retained on restart; schema changes must be
added as a new, additive migration rather than by editing an already applied one.

## Project Structure

```
horizon/
├── cmd/horizond/main.go        # Server entry point
├── internal/
│   ├── agent/                  # Agent domain (model, service, repository)
│   ├── run/                    # Run domain (model, coordinator, repository)
│   ├── scheduler/              # Phase 4: cron, scheduling
│   ├── cagent/                 # Pinned Docker Agent YAML adapter
│   ├── storage/sqlite.go       # SQLite persistence + versioned embedded migrations
│   ├── permissions/            # Permission types & validation
│   ├── config/config.go        # Application configuration
│   ├── api/handler.go          # HTTP handlers (CRUD + execution)
│   └── sandbox/podman.go       # Podman engine (create, run, logs, health)
├── runtime/Containerfile       # Pinned Docker Agent sandbox image build
├── tests/                      # Integration tests (require Podman)
├── web/                        # Future web UI
├── go.mod / go.sum
├── project.md                  # Product spec & roadmap
├── run.md                      # Operations guide
├── explainer.md                # Technical documentation
└── .env                        # Optional local environment overrides (ignored by Git)
```

## Capabilities (Phase 1)

- ✅ Agent lifecycle: create, list, get, update, delete via HTTP API
- ✅ Run execution: saved definitions materialize to isolated per-run Docker Agent YAML workspaces
- ✅ Persistence: SQLite stores agents, run history, schedules across restarts
- ✅ Run status: `queued` → `running` → `succeeded`/`failed`/`cancelled`/`timed_out`
- ✅ Container management: auto-create, start, stream logs, wait, auto-remove
- ✅ Health checks: Podman socket reachability + image existence
- ✅ Output and cancellation: bounded local output endpoint and active-run cancellation with terminal-state protection

## Development

```bash
# Build
CGO_ENABLED=0 go build ./...

# Lint
CGO_ENABLED=0 go vet ./...

# Run tests (require Podman socket + image + GOOGLE_API_KEY)
CGO_ENABLED=0 go test ./... -tags integration -count=1
```

## Documentation

- [`run.md`](run.md) — Operations guide, curl examples, troubleshooting
- [`explainer.md`](explainer.md) — Complete technical documentation
- [`project.md`](project.md) — Product specification and roadmap

## License

MIT