# Horizon

A local-first, no-code AI agent platform built in Go. Create, configure, run, and manage AI agents using natural language — all running locally on your machine.

## Architecture

| Component | Technology |
|-----------|------------|
| Application Core | Go 1.26 (stdlib only) |
| Agent Runtime | [cagent](https://github.com/docker/cagent) (Docker Agent) |
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
  -d '{"name":"test-agent","goal":"Report status","instructions":"Say hello"}'

# List agents
curl http://127.0.0.1:8080/api/agents

# Create a run
curl -X POST http://127.0.0.1:8080/api/runs \
  -H "Content-Type: application/json" \
  -d '{"agent_id":"<AGENT_ID>","prompt":"Report your status"}'

# List runs for an agent
curl "http://127.0.0.1:8080/api/runs?agent_id=<AGENT_ID>"
```

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `GOOGLE_API_KEY` | *required* | Google Gemini API key |
| `HORIZON_DB_PATH` | `./data/horizon.db` | SQLite database path |
| `HORIZON_PORT` | `8080` | HTTP server port |
| `HORIZON_SOCKET` | `$XDG_RUNTIME_DIR/podman/podman.sock` | Podman socket path |
| `HORIZON_IMAGE` | `localhost/horizon-sandbox:latest` | Sandbox container image |

## Project Structure

```
horizon/
├── cmd/horizond/main.go        # Server entry point
├── internal/
│   ├── agent/                  # Agent domain (model, service, repository)
│   ├── run/                    # Run domain (model, coordinator, repository)
│   ├── scheduler/              # Phase 4: cron, scheduling
│   ├── cagent/                 # cagent adapter (stub)
│   ├── storage/sqlite.go       # SQLite persistence + migrations
│   ├── permissions/            # Permission types & validation
│   ├── config/config.go        # Application configuration
│   ├── api/handler.go          # HTTP handlers (CRUD + execution)
│   └── sandbox/podman.go       # Podman engine (create, run, logs, health)
├── runtime/Containerfile       # Sandbox image build (cagent + Alpine)
├── tests/                      # Integration tests (require Podman)
├── web/                        # Future web UI
├── go.mod / go.sum
├── project.md                  # Product spec & roadmap
├── run.md                      # Operations guide
├── explainer.md                # Technical documentation
└── .env                        # Environment template
```

## Capabilities (Phase 1)

- ✅ Agent lifecycle: create, list, get, update, delete via HTTP API
- ✅ Run execution: manual trigger via API, runs in Podman container
- ✅ Persistence: SQLite stores agents, run history, schedules across restarts
- ✅ Run status: `queued` → `running` → `succeeded`/`failed`/`cancelled`/`timed_out`
- ✅ Container management: auto-create, start, stream logs, wait, auto-remove
- ✅ Health checks: Podman socket reachability + image existence
- ✅ Cancel runs: in-progress runs cancellable via API

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