# Runtime Composition

internal/app.App is Supremo's composition root. It builds shared workspace
services once, then gives RuntimeManager a factory that creates the
session-local mutable pieces on demand.

~~~text
cmd/supremo
→ app.NewWithRuntimeOverrides
  → SQLite state store and repository index
  → provider registry and provider manager
  → static tool registry, question service, interaction broker
  → durable transcript and context compiler
  → RuntimeManager (one Agent per session)
  → SubagentManager (durable child sessions)
  → backend.Service (transport-neutral API)
  → optional loopback HTTP RPC and SSE transport
~~~

## Shared and session-local services

| Shared per workspace | Isolated per live session |
| --- | --- |
| State store, transcript storage, compiler, repository index, provider manager, tool registry, interaction broker | Agent inbox and driver, cancellation, phase, tool manager, pending approval, hooks, observation/repeat state, and progress route |

RuntimeManager.For(sessionID) returns the same in-process Agent for repeat
calls to that session and a different Agent for another session. This lets two
sessions run independently while ensuring each session serializes its own
turns. Deleting a session releases its runtime. Shutdown cancels active
runtimes, waits up to five seconds for their terminal persistence, then closes
state.

## Client seams

There are two layers above the Agent:

- app.AgentAPI keeps the CLI and existing TUI on a stable backend seam. Both a
  plain Agent in focused tests and RuntimeManager in production satisfy it.
- backend.API is the versioned, transport-neutral contract for clients that
  need sessions, durable asynchronous runs, interactions, subagents, and
  subscriptions. internal/transport/http exposes it over loopback-only JSON
  RPC and Server-Sent Events (SSE), protected by a bearer token.

The backend admits prompts durably before running them. Each session gets a
FIFO worker that records run/message.queued → run/start → run/end; an
idempotency key prevents duplicate submission. Event subscribers first receive
durable events from a cursor, then live durable events and ephemeral progress.
If the cursor is too old, the client must resynchronize from a session snapshot.

## Capability registration

internal/app registers built-in capabilities explicitly:

| Capability | Contract | Owner |
| --- | --- | --- |
| Provider adapter | providers.Factory and providers.Provider | provider registry |
| Tool | tools.Tool and tools.ToolMetadata | tool registry |
| Tool hooks | before/after/user-input observer contracts | runtime hook set |
| Human interaction | question service and interaction broker | app/backend |
| Client updates | durable events plus agent.ProgressEvent | backend subscription |

There is deliberately no dynamic plugin loader. Adding an in-tree capability
means implementing an existing contract and registering it in the composition
root. A distributable plugin API needs its own versioning, isolation, and
security model before it is introduced.

## Policy placement

- Providers adapt wire formats; they do not own the loop.
- Tool descriptors declare routing, side effects, approval, and parallel-safety
  metadata; the Tool Manager enforces runtime policy.
- Filesystem tools own read-before-mutate CAS checks and checkpoints.
- The Agent owns prompt preparation, pressure recovery, tool-result ordering,
  and Turn/Step state.
- The backend owns admission, idempotency, API errors, client snapshots, and
  subscriptions.
- The UI renders state and sends intents. It does not mutate session state
  directly.

## Related

- [Agent Core](agent-core.md)
- [Session Context and Request Construction](session-context.md)
- [Dynamic Tooling and the Tool Scheduler](tooling-and-scheduler.md)
- [Multi-Agent Architecture](multi-agent.md)
