# Horizon

## Product Specification and Technical Roadmap

**Product:** Horizon  
**Category:** Local-first, no-code AI agent platform  
**Primary audience:** Nontechnical users  
**Primary interface:** Local web application  
**Core language:** Go  
**Initial agent engine:** cagent  
**Initial execution backend:** Rootless Podman  
**Initial persistent storage:** SQLite  
**Specification status:** Product direction and proposed MVP defined

---

# 1. Product Overview

## 1.1 What is Horizon?

Horizon is a local-first platform that lets people create, configure, run, and manage independent AI agents using natural language, without needing to write code.

A user describes a task. Horizon helps turn that description into a structured agent definition, explains the capabilities the agent needs, and lets the user review the configuration before running it. Users can then inspect results, review execution history, and control recurring work from a straightforward interface.

Horizon is the product users interact with. Go provides the application core, cagent is the initial agent reasoning engine, and Rootless Podman provides the initial container execution environment.

## 1.2 Product vision

Give people more freedom over how they spend their time by making it practical to delegate repeatable digital work to AI agents.

## 1.3 Product promise

**Your work. Beyond the ordinary.**

Users describe the job, configure the agent, let it work, and remain in control.

## 1.4 Product principles

1. **No-code by default:** Natural language is the primary creation interface.
2. **Local-first:** The Horizon application and management interface run on the user's computer.
3. **User control:** Users can inspect, edit, run, pause, resume, and delete their agents.
4. **Durability:** Agent definitions, schedules, and run history survive application restarts.
5. **Understandability:** Status, permissions, results, and errors are explained in plain language.
6. **Least privilege:** Agents receive only the capabilities required for their tasks.
7. **Modularity:** Components have clear responsibilities and can evolve independently.
8. **Incremental development:** Deliver a reliable end-to-end workflow before building a large extension ecosystem.
9. **Honest capability claims:** Horizon only presents capabilities that the configured runtime and integrations actually support.

Local-first does not necessarily mean local inference. If a user chooses a hosted model provider, relevant prompts and data may be sent to that provider. Horizon must explain this distinction and make data flows understandable.

---

# 2. Target Users and Initial Use Cases

## 2.1 Primary audience

Horizon is initially designed for nontechnical individuals who want to delegate repeatable or recurring digital tasks to AI.

Ordinary users should not need to understand:

- Go or another programming language.
- YAML or JSON configuration.
- Container commands.
- Model API implementation details.
- Runtime sockets or networking.
- Agent framework internals.

Advanced diagnostics can be available when necessary, but the primary workflow must remain approachable.

## 2.2 Initial use cases

The first release should focus on tasks that have clear instructions and inspectable outputs, such as:

- Summarizing user-provided documents.
- Preparing a daily task summary from explicitly configured inputs.
- Researching a topic and producing a report.
- Reviewing files in a permitted directory.
- Transforming structured information into a requested output.
- Running a tested task on demand or on a recurring schedule.

These are examples, not a claim that every integration is included in the MVP. Any task requiring an external service, filesystem access, or other tool must wait until that capability is implemented and configured.

---

# 3. Product Experience

## 3.1 Application form

The first product interface is a local web application served by the Go backend. A user opens a local address such as `http://localhost:<configured-port>`.

The web UI is a client of Horizon Core. It must not independently implement agent lifecycle logic or communicate directly with Podman.

A desktop shell may later package the same application, but desktop packaging is not a prerequisite for the initial developer MVP.

## 3.2 Main screens

### A. Dashboard

Show:

- Saved agents.
- Agent status.
- Recent runs.
- Latest results.
- Failed runs.
- Actions requiring the user's attention.

Avoid exposing raw container IDs, runtime configuration, or terminal output as the default experience.

### B. Create agent

The user describes the task in natural language.

Horizon proposes a structured definition containing, as supported:

- Agent name and purpose.
- Instructions and expected output.
- Model/provider configuration.
- Required capabilities.
- Permissions.
- Optional schedule.

Users can review and edit the proposal before saving or enabling it. If an important requirement is ambiguous, Horizon should ask a clarifying question rather than silently inventing one.

### C. Agent details

Provide controls to:

- Inspect and edit the definition.
- Run now.
- Pause or resume scheduled activity.
- View previous runs.
- Inspect results and errors.
- Delete the agent after confirmation.

### D. Run details

Display:

- Run ID.
- Start and completion times.
- Current or final status.
- Output and result.
- Failure details.
- Relevant execution events.
- Cancellation or timeout information where available.

### E. Settings and diagnostics

Provide approachable controls for:

- Model/provider configuration.
- Available capabilities.
- Permissions.
- Local runtime health.
- Application settings.
- Setup and recovery guidance.

---

# 4. Scope Definitions

This roadmap separates required product capabilities from later opportunities.

- **MVP:** Necessary to deliver and validate the first complete user workflow.
- **Post-MVP:** Improvements to reliability, usability, integrations, and flexibility after the core workflow works.
- **Long-term:** Larger capabilities that depend on demonstrated user demand and additional architectural work.

Features listed after the MVP are not hidden prerequisites for the first release.

---

# 5. MVP Scope

## 5.1 Agent creation and lifecycle

The MVP must allow users to:

1. Create an agent from a natural-language description.
2. Review and edit its structured definition.
3. Save it persistently.
4. List and inspect saved agents.
5. Update an agent.
6. Run an agent on demand.
7. Pause and resume its scheduled activity.
8. Delete an agent with appropriate confirmation.

An **agent** is a persistent entity with a purpose and configuration. A **run** is one execution instance of that agent.

Editing an agent must not erase its historical runs. Horizon must define how configuration changes affect future runs and what happens to executions already in progress.

## 5.2 Structured agent definitions

Horizon must maintain a structured representation of every agent.

The initial definition should support:

- Stable agent ID.
- Name and user-facing description.
- Goal and instructions.
- Runtime configuration.
- Supported model/provider settings.
- Requested capabilities.
- Permission settings.
- Optional schedule.
- Creation and update timestamps.
- Definition version.

The schema should not be tied unnecessarily to the web interface's presentation format.

Horizon should generate or validate cagent-compatible configuration through a dedicated integration layer. Ordinary users should not have to hand-edit cagent YAML.

## 5.3 Execution lifecycle

Horizon Core coordinates execution through the existing Go integration with Rootless Podman.

The lifecycle must cover:

1. Validate the agent definition.
2. Check required runtime dependencies.
3. Create and persist a run record.
4. Prepare the execution environment.
5. Start the configured agent engine.
6. Capture output and errors.
7. Determine the execution outcome.
8. Persist final status and results.
9. Attempt execution-environment cleanup.
10. Make results available through the UI.

Handle failures during startup, execution, output collection, persistence, and cleanup.

A successful container start is not the same as a successful task. Horizon must report success based on the actual execution outcome.

## 5.4 Run history

Every execution receives its own stable run ID.

Where applicable, store:

- Run ID and agent ID.
- Agent definition version or execution snapshot.
- Trigger type, such as manual or scheduled.
- Start and completion timestamps.
- Status.
- Captured output or a reference to persisted output.
- Error information.
- Exit status.
- Cancellation or timeout reason.

The initial status model may include:

- `queued`
- `running`
- `succeeded`
- `failed`
- `cancelled`
- `timed_out`

Only use statuses that correspond to actual lifecycle states. Introduce additional states when implementation needs them.

## 5.5 Local persistence

Use SQLite for initial durable storage.

Persist at least:

- Agent definitions.
- Definition versions or execution snapshots.
- Run records.
- Schedules.
- Relevant application settings.

Saved agents and historical run information must survive application restarts.

Use schema migrations and a defined persistence layer. Do not scatter database queries across HTTP handlers or UI components.

## 5.6 Basic scheduling

Users should be able to configure simple recurring schedules after testing an agent.

The scheduler must:

- Persist schedules.
- Preserve schedules across application restarts.
- Record scheduled executions in run history.
- Respect paused agents.
- Avoid unintended overlapping executions by default.
- Record failures to start scheduled runs.

Scheduling is a Horizon Core responsibility, not an incidental property of a container.

The MVP does not need a general-purpose workflow engine.

## 5.7 Basic permissions and user control

The MVP must make the capabilities available to an agent understandable.

At minimum:

- Only configured capabilities may be exposed to the agent.
- Destructive or externally consequential actions must not be silently enabled.
- Sensitive actions require confirmation or another appropriate authorization mechanism.
- Permission checks must be enforced at the execution or capability boundary, not merely by hiding UI controls.
- Secrets must not be written to ordinary logs or unnecessarily exposed in the interface.

A model requesting an action is not the same as Horizon authorizing it.

The MVP can support a small number of capabilities. A universal permission framework is not required.

## 5.8 Setup and diagnostics

Horizon must detect common setup failures and explain them in understandable language.

Examples:

- Podman is unavailable.
- The Podman socket cannot be reached.
- The required image is missing.
- The agent configuration is invalid.
- Model credentials are missing.
- The selected model is unavailable.
- Execution timed out.

The developer MVP may rely on existing development tools. A user-ready release must provide guided setup appropriate for nontechnical users.

## 5.9 MVP acceptance journey

A nontechnical user must be able to:

1. Launch Horizon.
2. Create an agent using natural language.
3. Review and save the proposed definition.
4. Run the agent.
5. View and understand the result.
6. Restart Horizon without losing the agent or run history.
7. Configure a recurring schedule.
8. Pause scheduled activity.
9. Understand common failures without reading source code or editing configuration files.

---

# 6. MVP Technical Architecture

## 6.1 Architecture overview

The initial architecture comprises:

1. Local web UI.
2. Go application and service layer.
3. Agent definition and lifecycle management.
4. Scheduler and run coordinator.
5. SQLite persistence.
6. Internal Podman execution adapter.
7. cagent running inside the execution environment.

The web UI and any CLI must use the same application services. Business logic must not be duplicated across interfaces.

## 6.2 Horizon Core

Horizon Core owns:

- Agent identity and definitions.
- Agent lifecycle.
- Run creation and status.
- Scheduling.
- Persistence.
- Application-level authorization and permissions.
- Result and history data.
- Runtime health checks.
- Coordination of the execution lifecycle.

Core functionality must remain independent of the web interface.

## 6.3 cagent

cagent is the initial agent reasoning and orchestration engine.

It is responsible for the functionality supported by its implementation, including interpreting its agent configuration and carrying out model-driven work.

Horizon should integrate with cagent rather than accidentally implement a second reasoning loop around it.

Before expanding provider support, verify the selected cagent version's actual support for models, tools, streaming, cancellation, and other required capabilities.

Do not claim arbitrary providers or tools are supported merely because Horizon's own data model can represent them.

## 6.4 Rootless Podman

Rootless Podman is the initial execution backend.

It creates and manages the container in which cagent runs. Horizon communicates with Podman through the existing Go integration and Podman API. The MVP does not require the Docker SDK.

Podman is an internal implementation detail, not a separate user-facing runtime plugin.

Retain and incrementally improve the existing sandbox integration rather than rewriting it without a concrete need.

Verify cleanup, timeouts, resource constraints, output collection, mounts, networking, and failure handling through tests.

Rootless containers reduce some risks but are not a complete security boundary. Filesystem access, network access, secrets, capabilities, and shared-kernel risks still require deliberate configuration.

## 6.5 SQLite

SQLite is the initial database.

Use migrations and a defined storage layer. Keep durable definitions and run records separate from transient in-memory execution state.

## 6.6 Local web interface

The web interface communicates with Go through a documented local HTTP API.

The UI must not call Podman directly or manage container lifecycles independently.

By default, bind to a local interface and protect against unauthorized local requests, cross-origin abuse, and unintended remote exposure.

## 6.7 CLI

A CLI may remain available for development, diagnostics, and advanced users. It is not the primary interface for the target audience.

The CLI and web UI should share the same application service layer.

---

# 7. Proposed Go Project Structure

This is a proposed direction, not a requirement to create every package immediately.

```text
horizon/
├── cmd/
│   └── horizond/
│       └── main.go
├── internal/
│   ├── agent/
│   │   ├── model.go
│   │   ├── service.go
│   │   └── repository.go
│   ├── run/
│   │   ├── model.go
│   │   ├── coordinator.go
│   │   └── repository.go
│   ├── scheduler/
│   ├── sandbox/
│   │   └── podman.go
│   ├── cagent/
│   │   └── adapter.go
│   ├── storage/
│   │   ├── sqlite.go
│   │   └── migrations/
│   ├── permissions/
│   ├── config/
│   └── api/
├── web/
├── runtime/
│   └── Containerfile
├── tests/
├── go.mod
└── project.md
```

Create packages only when there is a clear responsibility to implement. Do not add empty abstractions merely to match this tree.

Keep the current working sandbox code intact while extracting responsibilities gradually. Rename Go module paths, executable names, image names, and documentation references only when the corresponding change is intentional and tested.

---

# 8. Plugin and Provider Strategy

## 8.1 MVP decision

The MVP does not require a public external plugin system.

- Podman remains an internal execution adapter.
- cagent remains the initial reasoning engine.
- Core lifecycle management remains part of Horizon.
- Only implement integrations required by the first supported user workflows.

Do not build a plugin marketplace or third-party SDK before the core product is usable.

## 8.2 Provider integrations

Provider flexibility is a product goal, but its implementation depends on who owns model communication.

Initially, use the provider configuration supported by cagent.

Before introducing a separate provider plugin protocol, decide whether Horizon will:

1. Delegate model calls to cagent, or
2. Own model calls through a Horizon-defined provider interface.

Do not implement both approaches without a concrete requirement.

If Horizon later owns provider calls, define interfaces for supported operations such as model selection, generation, streaming, cancellation, errors, and usage metadata. Expose provider-specific differences instead of claiming all providers behave identically.

## 8.3 Capability integrations

Future capabilities may include file access, email, calendars, research, and external APIs.

For each capability, define:

- Stable identity.
- Available operations.
- Input and output schemas.
- Required permissions.
- Side effects.
- Timeout and failure behavior.
- Credential handling.

MCP may be used where appropriate. It is an integration mechanism, not a replacement for Horizon Core or its lifecycle model.

## 8.4 Future external plugin protocol

If third-party plugins become necessary, evaluate a process-based protocol with versioned manifests and structured messages. JSON-RPC over newline-delimited JSON is one candidate.

The eventual protocol must address:

- Protocol versioning.
- Request IDs.
- Input and output schemas.
- Timeouts and cancellation.
- Process supervision.
- Bounded messages.
- Error handling.
- Permission boundaries.
- Compatibility.

This protocol is explicitly outside the MVP.

---

# 9. Post-MVP Scope

These improvements should follow a reliable, user-tested MVP.

## 9.1 Better agent creation

- Better natural-language interpretation.
- More useful clarifying questions.
- Reusable agent templates.
- Richer validation before saving.
- Test runs and configuration comparisons.
- Clearer explanations of permissions and external data sharing.

## 9.2 Better execution and recovery

- More detailed progress events.
- Retry policies for appropriate transient failures.
- Checkpoints for tasks that can safely resume.
- Recovery of interrupted runs after restarts.
- Improved resource limits and diagnostics.
- Better concurrency controls.

Retries must account for side effects. If an action may already have sent an email or changed an external system, it must not be repeated blindly.

## 9.3 Improved scheduling

- More flexible recurring schedules.
- Time-zone support.
- Missed-run policies.
- Better schedule visibility and controls.
- Optional event-based triggers.

## 9.4 Initial integrations

Add a small number of useful integrations based on user demand. Potential examples:

- Read-only file and document access.
- Research and web retrieval.
- Selected productivity services.
- Explicitly authorized email or calendar actions.

Each integration must have clear permissions, scoped access, and tests.

## 9.5 User experience improvements

- Guided onboarding.
- Better error explanations.
- Search and filtering across agents.
- Agent duplication and templates.
- Improved run timelines.
- Export and backup of local agent definitions.
- Improved provider and credential settings.

## 9.6 Provider flexibility

Expand model support according to validated user needs and the capabilities of the chosen reasoning engine.

If cagent becomes a meaningful limitation, evaluate a separate provider interface or Horizon-owned reasoning loop as a deliberate architectural decision.

Do not replace cagent solely to achieve theoretical provider independence.

## 9.7 Desktop packaging

Package the local web UI and Go backend as a desktop application if it materially improves installation and everyday use.

The desktop shell should reuse the same application services and persistence layer.

For Windows users, validate the installation and Rootless Podman/WSL experience early. Do not assume that a working developer setup automatically translates into a straightforward nontechnical installation.

---

# 10. Long-Term Possibilities

These are strategic options, not commitments.

## 10.1 Public plugin ecosystem

A documented extension protocol, developer tools, plugin distribution, compatibility checks, and user-facing capability management.

Only pursue a marketplace when the extension model and security boundaries are mature.

## 10.2 Multiple execution backends

Alternative container engines or remote execution services may be considered if users need them.

An internal execution interface can evolve over time, but an external runtime plugin ecosystem is not a prerequisite for Horizon.

## 10.3 Advanced automation

Potential future capabilities include:

- Event-driven automation.
- More sophisticated workflows.
- Optional coordination between agents.
- Durable task checkpoints.
- Advanced memory and knowledge retrieval.
- More complex triggers and integrations.

Multi-agent coordination and distributed execution introduce additional reliability, permission, and observability requirements. They must not delay the initial product.

## 10.4 Cloud and team features

Horizon may eventually support hosted execution, remote management, shared agents, team permissions, and centralized administration.

These would be separate product and security milestones. They are not implied by local-first MVP support.

---

# 11. State and Persistence Model

Horizon must distinguish three concepts.

## 11.1 Agent definition

The durable description of what an agent should do. It includes instructions, configuration, capabilities, permissions, and schedule settings.

## 11.2 Run state

The execution-specific state of one run, including status, timestamps, output, errors, and execution records.

## 11.3 Agent memory

Information intentionally retained between runs when a concrete use case requires it.

Memory must not be implemented as an uncontrolled dump of every previous conversation.

For the MVP, prioritize agent definitions and run history. Add cross-run memory only after defining its purpose, retention policy, and user controls.

Switching model providers must not erase Horizon-owned definitions or historical records. Provider-specific conversation state may not be portable and must be handled explicitly.

---

# 12. Reliability and Security

These requirements support the product; they are not its primary marketing identity.

## 12.1 Reliability

- Persist important state before reporting success.
- Handle runtime errors explicitly.
- Record failures and timeouts.
- Attempt cleanup even when execution fails.
- Prevent unintended duplicate scheduled executions.
- Do not silently discard output or errors.
- Recover persistent application state after restart.
- Test failure paths as well as successful execution.

## 12.2 Security

- Use Rootless Podman with deliberately restricted container configuration.
- Avoid mounting unnecessary host paths.
- Restrict network access where feasible.
- Avoid exposing secrets in logs or command output.
- Validate agent configuration before execution.
- Enforce permissions at the actual capability boundary.
- Protect the local HTTP interface against unintended remote access.
- Treat model-generated instructions and tool arguments as untrusted input.
- Require appropriate confirmation for sensitive side effects.

A container boundary does not eliminate all host risks. Security claims must match controls that are actually implemented and tested.

---

# 13. Testing and Acceptance Criteria

## 13.1 Core tests

The MVP should include tests for:

- Agent creation, validation, updates, and deletion.
- Run creation and status transitions.
- SQLite persistence and migrations.
- Podman communication failures.
- Container startup and cleanup.
- cagent execution failures.
- Missing credentials and invalid configurations.
- Cancellation and timeout behavior.
- Scheduling and restart behavior.
- Permission enforcement.

Use unit tests for application logic and integration tests for the Podman/cagent boundary.

## 13.2 Product acceptance

The MVP is ready for a small pilot when:

1. A nontechnical user can create an agent without writing code.
2. The user can review and edit the proposed definition.
3. The agent can execute a supported task successfully.
4. The result and run status are understandable.
5. Agent definitions and run history survive a restart.
6. A recurring schedule can be configured and paused.
7. Common failures produce actionable explanations.
8. The user can understand the agent's permissions.
9. The user does not need to manage containers manually.
10. The end-to-end workflow works reliably in the supported pilot environment.

---

# 14. Development Roadmap

## Phase 1: Stabilize execution

**Goal:** Make the existing execution foundation reliable.

- Preserve the current Go-to-Podman integration.
- Stabilize cagent invocation and configuration.
- Capture output, errors, and exit status.
- Handle timeouts and cleanup.
- Add focused integration tests.

**Exit condition:** Horizon can execute a supported agent task and reliably report its outcome.

## Phase 2: Build durable agent management

**Goal:** Turn individual executions into manageable agents.

- Add structured agent definitions.
- Add SQLite persistence.
- Add run records.
- Separate agent identity from individual runs.
- Implement application services for agent and run management.

**Exit condition:** Agents and run histories survive application restarts.

## Phase 3: Build the local web app

**Goal:** Make Horizon usable without a terminal.

- Add the local HTTP API.
- Build the dashboard and agent library.
- Add the natural-language creation flow.
- Add agent details and run history.
- Show execution status and errors clearly.

**Exit condition:** A user can create, run, and inspect an agent through the interface.

## Phase 4: Add scheduling and controls

**Goal:** Enable dependable recurring tasks.

- Add persistent schedules.
- Add pause and resume.
- Prevent unintended overlapping runs.
- Add basic permission controls.
- Add restart recovery and diagnostics.

**Exit condition:** A configured recurring agent can run predictably and be controlled by the user.

## Phase 5: Pilot and refine

**Goal:** Validate Horizon with nontechnical users.

- Test installation and onboarding.
- Observe whether users understand agent configuration.
- Identify confusing failures.
- Refine permissions and explanations.
- Prioritize integrations based on actual usage.

**Exit condition:** Users can complete the core workflow without developer assistance.

## Phase 6: Post-MVP expansion

Only after the earlier phases are stable, evaluate:

- More integrations.
- Advanced scheduling.
- Better checkpoints and retries.
- Expanded model support.
- Desktop packaging.
- External capability plugins.
- Alternative execution backends.

---

# 15. Explicit MVP Non-Goals

The following are not required for the first release:

- Public plugin marketplace.
- External runtime plugins.
- Horizon-owned model reasoning loop.
- Multi-agent swarms.
- Cloud execution.
- Enterprise team management.
- Universal visual workflow editor.
- Support for every model provider.
- Support for every integration.
- Automatic recovery from every possible failure.

These exclusions prevent the MVP from becoming an infrastructure project that never reaches users.

---

# 16. Final Product Direction

Horizon is a local-first, no-code AI agent management product.

Its initial architecture uses Go for the application core, cagent for agent reasoning and orchestration, Rootless Podman for container execution, and SQLite for persistent local data.

The first release must make a small set of supported agent workflows easy to create, execute, monitor, and schedule.

The product's value is not simply starting a container. Its value is enabling a user to delegate work to an agent, understand what happened, and remain in control without needing to become a developer.

**MVP priority:** Deliver the complete create → configure → run → inspect → schedule workflow.

**Post-MVP priority:** Improve recovery, integrations, provider flexibility, and user experience based on real usage.

**Long-term direction:** Evolve into an extensible platform for personal AI agents without sacrificing usability, local control, or reliability.