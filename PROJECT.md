# How Supremo is wired

Supremo separates frontend concerns, durable orchestration, session-local execution, and workspace-wide services. This document maps those layers and shows which path runs for each user scenario.

## Runtime map

```text
interactive TUI -> api.Client -> backend.Service ---------┐
HTTP/SSE client -> transport/http.Client -> backend.Service ┤
backend.Service -> SubagentManager -------------------------┤
one-shot CLI -> app.AgentAPI -------------------------------┤
                                                            v
                                                     RuntimeManager
                                      session A Agent | session B Agent | child Agent
                                                            |
                             context.Compiler | providers.Manager | tools.Manager
                                                            |
                                          state.Store + sessionlog + artifacts
```

`internal/app.App` creates the workspace-wide services and connects them. The TUI receives the in-process backend as an `api.Client`. `supremo serve` exposes the same version 1 contract through loopback JSON remote procedure calls (RPC) and server-sent events (SSE). One-shot CLI runs retain `app.AgentAPI` for compatibility.

## Package ownership

| Package | Owns |
| --- | --- |
| `cmd/supremo` | CLI parsing, process lifecycle, TUI startup, one-shot mode, and loopback server startup |
| `internal/app` | Dependency construction and built-in capability registration |
| `internal/api` | Versioned client interface, requests, responses, errors, and event DTOs |
| `internal/backend` | Sessions, durable run queues, idempotency, snapshots, interactions, recovery, and event subscriptions |
| `internal/transport/http` | Authenticated loopback RPC and SSE adapters for `api.Client` |
| `internal/agent` | Turn and step loop, session surface, pressure recovery, scheduler, runtime isolation, and subagents |
| `internal/context` | Read-only request preparation, working-set selection, manifests, traces, and prompt compilation |
| `internal/sessionlog` | Typed event encoding, replay, lifecycle repair, and model-visible surface reconstruction |
| `internal/state` | SQLite storage, projections, artifacts, documents, checkpoints, and subscriptions |
| `internal/providers` | Provider registry, protocol adapters, streaming, retries, usage, and model catalogs |
| `internal/tools` | Tool registry, descriptors, policy, approvals, execution, and activity |
| `internal/tools/filesystem` | Path locks, read hashes, CAS mutations, atomic writes, and rewind checkpoints |
| `internal/repository` | Workspace indexing and evidence retrieval |
| `internal/capabilities` | Plan guards, observation reuse, and repeat-call feedback |
| `internal/ui` | Bubble Tea frontend state, API intents, rendering, selectors, and local terminal actions |

## Scenario paths

| Scenario | Path and durable outcome |
| --- | --- |
| Start the TUI | `cmd/supremo` starts `backend.Service`, opens a session, then passes the service to `ui.New` as `api.Client` |
| Submit a prompt | The backend stores an idempotent queued run; the session worker records run start/end around `RuntimeManager.Run` |
| Build a provider request | The agent asks `context.Compiler` to prepare a request, freezes and hashes the exact envelope, resolves pressure, commits the manifest, records the prompt artifact, then calls the provider |
| Reach a context limit | The agent prunes oversized tool output or compacts the oldest safe prefix, appends a surface replacement, and rebuilds from durable state |
| Execute tool calls | Calls prepare in model order, only `ParallelSafe` bodies overlap, exclusive calls create barriers, and every result commits in model order |
| Mutate a file | Policy and approval checks run before filesystem path locking, hash comparison, checkpointing, and atomic replacement |
| Delegate work | `SubagentManager` creates a child session with immutable scope and a durable FIFO queue; the child receives its own runtime |
| Switch provider or model | The backend verifies configuration before activation; `/model` can refresh configured providers concurrently without exposing credentials |
| Reconnect a client | The client loads a snapshot, subscribes after its cursor, rejects duplicate events, and reloads when the backend returns `RESYNC_REQUIRED` |
| Restart Supremo | Run, turn, step, tool, compaction, interaction, and subagent recovery close incomplete durable boundaries without replaying ambiguous side effects |
| Shut down | The backend closes admission, subagents stop, session runtimes cancel and drain, then state closes |

## Core invariants

- The immutable event log is the source of truth; the model-visible session surface is a replayed projection
- Required lifecycle persistence happens before provider and tool side effects
- Every assistant tool call receives one model-visible result, including cancellation and recovery outcomes
- Determinism covers the provider-visible envelope, not trace metadata
- A session serializes its turns; separate sessions and child agents may run concurrently
- Tool parallelism is opt-in and bounded; durable observations remain model-ordered
- Shared-workspace writes rely on filesystem locks and CAS, not agent ownership claims
- Frontends send API intents and render state; they do not mutate core runtime state directly
- Credentials never appear in API responses, durable events, logs, or transcript history

## Detailed architecture

- [Agent core](docs/architecture/agent-core.md)
- [Runtime composition](docs/architecture/runtime-composition.md)
- [Session context and request construction](docs/architecture/session-context.md)
- [Dynamic tooling and the tool scheduler](docs/architecture/tooling-and-scheduler.md)
- [Multi-agent architecture](docs/architecture/multi-agent.md)
