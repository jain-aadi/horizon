# Horizon Execution Foundation Implementation Plan

> **For Hermes:** Implement this plan task-by-task; do not commit or discard the user's existing working-tree changes unless explicitly asked.

**Goal:** Make one saved, explicitly configured Horizon agent execute reliably in Podman and produce an inspectable, durable result before beginning the web UI or scheduler.

**Architecture:** Keep the local Go HTTP API, SQLite, Rootless Podman, and cagent design established in `project.md`. Move execution orchestration out of the HTTP handler into a concrete coordinator that uses the existing agent service/repositories. Each run must materialize a validated cagent configuration in a per-run workspace, capture output to local durable storage, and make state transitions—including cancellation and timeout—authoritative in SQLite.

**Tech stack:** Go 1.26, `net/http`, `modernc.org/sqlite`, Podman REST API over Unix socket, cagent, `github.com/google/uuid`. Add a YAML encoder only after verifying the cagent configuration contract that will be pinned in the runtime image.

---

## Current context and scope

- Product priority is the complete **create → configure → run → inspect → schedule** workflow (`project.md:823-835`).
- The immediate blocker is execution correctness, not UI construction: `internal/api/handler.go:462-471` hardcodes `agent.yaml`, does not generate it from the saved agent, and discards logs.
- Preserve and complete the current uncommitted environment-loading work in `cmd/horizond/main.go`, `internal/config/config.go`, and `internal/api/handler.go`; do not overwrite it.
- Do not build scheduling, a plugin system, or a web frontend in this implementation cycle. Those depend on a reliable run lifecycle.
- Do not expose credentials in API responses, persisted agent definitions, run logs, or test output.

## Immediate delivery boundary

A developer can create a valid agent through the local API, run it, poll its run record, receive a terminal status that accurately reflects success/failure/cancel/timeout, and retrieve captured output. A real Podman/cagent test is optional until its prerequisites are installed, but the behavior must be covered against a controlled fake Podman API.

---

### Task 1: Freeze the baseline and protect existing changes

**Objective:** Establish a reproducible baseline without modifying source behavior.

**Files:**
- Inspect: `cmd/horizond/main.go`, `internal/api/handler.go`, `internal/config/config.go`
- Inspect: `README.md`, `run.md`, `explainer.md`, `project.md`
- Create: `internal/config/config_test.go`

**Steps:**
1. Record `git status --short`, `git diff --check`, `CGO_ENABLED=0 go vet ./...`, and `CGO_ENABLED=0 go test ./...` in the implementation notes.
2. Add a focused config test proving default configuration values and valid environment overrides.
3. Run the test before changing configuration behavior; it should expose the flag-precedence issue once that behavior is asserted.
4. Keep `horizond` and `horizond.exe` untouched during functional work. Treat their removal from version control as a separate, explicit cleanup decision.

**Verification:**
```bash
CGO_ENABLED=0 go test ./internal/config
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go build ./...
```

---

### Task 2: Complete configuration precedence and validation

**Objective:** Make `.env`, process environment, and explicit CLI flags deterministic and documented.

**Files:**
- Modify: `cmd/horizond/main.go:21-71`
- Modify: `internal/config/config.go:43-85`
- Extend: `internal/config/config_test.go`
- Modify: `README.md:70-78`, `run.md:43-51`

**Steps:**
1. Define precedence explicitly: defaults < `.env` < inherited process environment < explicitly supplied CLI flags.
2. Use `flag.Visit` or equivalent to apply `--db` and `--port` only when passed, so their defaults do not overwrite `HORIZON_DB_PATH` or `HORIZON_PORT`.
3. Reject invalid environment values such as nonnumeric/out-of-range ports rather than silently retaining a default.
4. Either implement all documented timeout environment variables or remove their documentation. Include a container timeout override because it is required for operational control.
5. Keep `.env` optional and never print loaded values. Do not attempt to support a general shell parser; clearly document the supported `KEY=VALUE` format.
6. Make the currently unused `--config` flag return a clear error or remove it until file configuration exists; it must not silently ignore user input.

**Verification:**
```bash
CGO_ENABLED=0 go test ./internal/config ./cmd/horizond
CGO_ENABLED=0 go vet ./...
```

---

### Task 3: Consolidate agent validation and serialization

**Objective:** Ensure API-created agents meet the minimum persisted-definition contract and use one conversion path.

**Files:**
- Modify: `internal/agent/model.go:11-96`
- Modify: `internal/agent/service.go:29-153`
- Modify: `internal/api/handler.go:94-211`
- Create: `internal/agent/service_test.go`
- Create or extend: `internal/api/handler_test.go`

**Steps:**
1. Add domain validation for required name, goal, instructions, a valid runtime image/default image, and supported model fields needed by the first cagent configuration.
2. Make creation assign ID, creation/update timestamps, and version `1` in the service layer; make update preserve creation time and increment the existing version.
3. Have HTTP handlers call the service instead of duplicating row-to-domain conversion and lifecycle rules.
4. Keep storage types restricted to `internal/storage`; remove duplicated conversion helpers only after all call sites use the domain-service path.
5. Return stable JSON validation errors with appropriate 400 status codes; do not return raw database errors to clients.

**Verification:**
```bash
CGO_ENABLED=0 go test ./internal/agent ./internal/api
```

---

### Task 4: Make migrations and SQLite startup reliable

**Objective:** Ensure a fresh launch creates required local storage and applies schema changes deterministically.

**Files:**
- Modify: `internal/storage/sqlite.go:23-55,363-404`
- Modify or remove: `internal/storage/migrations/001_create_agents.sql`
- Modify or remove: `internal/storage/migrations/002_create_runs.sql`
- Create: `internal/storage/sqlite_test.go`

**Steps:**
1. Create the parent directory for the configured database path before opening SQLite; handle an in-memory SQLite path correctly in tests.
2. Choose one canonical migration source: embedded SQL files or Go constants. Do not retain unused duplicate migration definitions.
3. Add a schema-version mechanism before adding any new schema changes, so upgrades are forward-only and idempotent.
4. Keep `PRAGMA foreign_keys` enabled and add tests for foreign-key cascade, agent persistence, run persistence, list ordering, and `ErrNotFound` mapping.
5. Do not change the existing schema destructively or erase databases.

**Verification:**
```bash
CGO_ENABLED=0 go test ./internal/storage -count=1
```

---

### Task 5: Verify and pin the cagent contract

**Objective:** Replace the placeholder adapter with a configuration boundary based on a known cagent version.

**Files:**
- Modify: `runtime/Containerfile:1-22`
- Modify: `internal/cagent/adapter.go:8-110`
- Modify: `workspace/test-agent.yaml`
- Create: `internal/cagent/adapter_test.go`
- Update: `go.mod`, `go.sum` only if a YAML library is required

**Steps:**
1. Inspect the selected cagent release/documentation and record the exact accepted agent YAML syntax and execution command semantics. Do not infer the format from the existing sample.
2. Pin cagent to a specific tag or immutable commit in the Containerfile; retain a non-root runtime user.
3. Change `BuildConfig` to accept a concrete `agent.AgentDefinition`, validate it, and map supported model, instruction, capability, and permission fields deliberately.
4. Serialize configuration using a tested YAML encoder rather than hand-written YAML. Add only the dependency needed for this task.
5. Make `workspace/test-agent.yaml` a valid, minimal fixture for the pinned contract, not a disconnected manual sample.
6. Keep unsupported fields rejected or explicitly documented; do not advertise arbitrary provider/tool support.

**Verification:**
```bash
CGO_ENABLED=0 go test ./internal/cagent
# When Podman is available:
podman build -t localhost/horizon-sandbox:dev ./runtime
```

---

### Task 6: Build a real run coordinator and durable output path

**Objective:** Replace execution logic in the HTTP handler with a concrete lifecycle owner.

**Files:**
- Modify: `internal/run/coordinator.go`
- Modify: `internal/run/model.go`
- Create: `internal/run/service.go` or `internal/run/coordinator_service.go`
- Modify: `internal/run/repository.go`
- Modify: `internal/api/handler.go:226-487`
- Modify: `cmd/horizond/main.go:112-124`
- Add tests: `internal/run/*_test.go`, `internal/api/handler_test.go`

**Steps:**
1. Define a concrete coordinator dependency set: agent service/repository, run repository, cagent adapter/config materializer, sandbox engine, and configured data/workspace root.
2. On run creation, snapshot agent version and create a `queued` row before launching work.
3. Materialize a per-run workspace below the application data directory. Write the generated YAML there with restricted file permissions; never write API keys into it.
4. Capture stdout/stderr to a bounded local file. Persist its safe local reference in `output_ref`; expose a dedicated read endpoint only after enforcing that the resolved path stays under the output root.
5. Implement guarded state transitions: `queued → running → succeeded|failed|cancelled|timed_out`. Persist timestamps, container ID, exit status, error, and output reference exactly once.
6. Use a managed context/cancel-function registry keyed by run ID. Cancellation must cancel the active context and request container removal, not only alter the database row.
7. Ensure a late worker cannot overwrite a cancelled terminal state. Define behavior for a cancellation race and test it.
8. Map a timeout to `timed_out`; inspect both the Go error and `RunResult.Error` from the sandbox.

**Verification:**
```bash
CGO_ENABLED=0 go test ./internal/run ./internal/api -race
```

---

### Task 7: Harden the Podman execution boundary

**Objective:** Make the adapter faithfully implement the run contract and least-privilege configuration.

**Files:**
- Modify: `internal/sandbox/podman.go:123-356`
- Create: `internal/sandbox/podman_test.go`
- Extend: `internal/run/*_test.go`

**Steps:**
1. Replace generic `Env map[string]string` pass-through with an allowlisted execution environment. Credentials are supplied from the configured secure environment path only; request bodies cannot override them.
2. Make workspace path, image, timeout, networking, and resource settings derive from the validated agent definition and global policy, not hardcoded handler values.
3. Enforce the requested/approved permission set at container creation: network access, mounts, shell/tool availability, and capabilities must match actual settings.
4. Return errors for invalid HTTP status codes from log streaming and container removal; include bounded diagnostic content only.
5. Make cleanup observable in logs and test it on create/start/log/wait failures.
6. Preserve rootless operation, read-only mounts by default, dropped Linux capabilities, and a local-only server bind.

**Verification:**
```bash
CGO_ENABLED=0 go test ./internal/sandbox -race
CGO_ENABLED=0 go test ./... -race
```

---

### Task 8: Add end-to-end API tests without external runtime dependencies

**Objective:** Prove the core journey at HTTP level before real Podman integration.

**Files:**
- Create: `internal/api/handler_test.go` or `internal/api/integration_test.go`
- Create: fake-engine support under `internal/sandbox` only if it is reusable; otherwise keep the fake test-local
- Create: `tests/integration/` only for tagged real-runtime tests

**Steps:**
1. Use `httptest` with a temporary SQLite database to test agent creation, retrieval, update/versioning, and deletion.
2. Test run creation returns `202` and a stable run ID, followed by successful status/output persistence from a controlled engine result.
3. Test container failure, nonzero exit, timeout, cancellation, malformed JSON, missing agent, invalid agent definition, and unknown run ID.
4. Add a tagged real Podman/cagent smoke test that is skipped unless socket, image, and required credential environment are explicitly present.
5. Do not make normal unit tests dependent on Podman, cagent, network access, or a model API key.

**Verification:**
```bash
CGO_ENABLED=0 go test ./... -race -count=1
CGO_ENABLED=0 go test -tags integration ./... -count=1
```

---

### Task 9: Align documentation with verified behavior

**Objective:** Remove unsupported claims and document the verified developer workflow.

**Files:**
- Modify: `README.md`
- Modify: `run.md`
- Modify: `explainer.md`
- Modify: `project.md` only if the product decision changes; do not rewrite its established direction

**Steps:**
1. Update API examples to show the actual required agent configuration and run-result retrieval endpoint.
2. State clearly which capabilities are implemented, tested with a fake runtime, and real-runtime verified.
3. Remove claims that cancellation stops containers, output is captured, network is blocked, or scheduling exists until tests demonstrate them.
4. Document precise configuration precedence and required Podman/cagent setup.
5. Keep `project.md` as roadmap/product authority; implementation docs must follow it, not redefine it.

**Verification:**
```bash
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./... -race -count=1
CGO_ENABLED=0 go build ./...
git diff --check
```

---

## Deferred until the execution foundation is proven

1. Build the local web UI and natural-language creation UX (Phase 3).
2. Persistent scheduling, pause/resume, non-overlap, and restart recovery (Phase 4).
3. External integrations, broad provider support, plugins, cloud/team features.
4. Desktop packaging.

## Risks and decisions to make before Task 5

- **cagent compatibility:** The project must choose and pin a version before generating YAML or promising providers/tools.
- **Secret handling:** `GOOGLE_API_KEY` can remain process-provided for the developer MVP, but agent records and API payloads must hold only references—not secret values.
- **Cancellation semantics:** Podman removal can be the initial cancellation mechanism; a later cagent-native cancellation channel can be added once its support is verified.
- **Output retention:** Use an application-owned output directory and define size/retention limits before allowing arbitrary agent workloads.
- **Database migrations:** Adopt versioned, additive migrations before the first released user database; do not retroactively mutate existing schema in place.

## Completion criteria

The execution foundation is complete when all of the following are demonstrated:

- A validated saved agent produces a generated, pinned-cagent configuration.
- A run does not depend on a hardcoded `agent.yaml`.
- Output is persisted and safely retrievable.
- State transitions distinguish success, failure, cancellation, and timeout correctly.
- Cancellation affects active execution and cannot be overwritten by a late goroutine.
- Unit/API tests exercise normal and failure paths without external services.
- A tagged real Podman/cagent smoke test exists and passes in a prepared environment.
- `go vet`, tests, race detector, build, and `git diff --check` pass.
