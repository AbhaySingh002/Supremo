# Agent Core

internal/agent owns Supremo's one-turn-at-a-time state machine. Providers,
tools, state, hooks, the backend, and UI clients supply inputs or services; none
of them can advance a Turn, finish a Step, or bypass the durable log.

## Canonical turn

~~~text
queued or direct user request
→ sync and repair the session surface
→ turn/start
→ user/message and objective update
→ repeat Steps
  → step/start
  → prepare, freeze, and measure the provider request
  → reduce context pressure if necessary
  → persist request diagnostics and call the provider
  → persist assistant/message
  → schedule and commit tool results
  → step/end
→ turn/end
~~~

A Step with tool calls continues the Turn. A Step with a final assistant answer
completes it. A blocked approval, recoverable tool failure, cancellation, or
provider failure is represented in the normal lifecycle rather than as a second
completion protocol.

## Durable ordering

The Agent fails closed at lifecycle boundaries. It does not call a provider
after a required request/lifecycle write fails, and it does not run a tool
before its tool/call event is durable. Message rows, projection events, and
model-surface events commit atomically.

Terminal step/end, turn/end, tool-result, and run-end writes use a fresh,
two-second context so caller cancellation does not erase the durable outcome.
If both work and its terminal write fail, the returned error retains both causes.
On restart, session-log repair closes unfinished turns and steps, supplies one
causal result for every unresolved tool call, and resolves interrupted
compactions.

## Model-visible surface

The immutable session event log is the source of truth. The model sees only the
active Surface: ordered user/message, assistant/message, and tool/result
events. Stream chunks, retries, request diagnostics, approvals, todos, plan
state, and lifecycle events are durable but are not prompt history.

Surface replacements never delete history. Pruning and compaction append a
replacement event that names the range it supersedes; replay derives the same
visible conversation after a restart. See [Session Context and Request
Construction](session-context.md).

## Tool boundary

Every assistant tool call receives exactly one model-visible tool result,
including cancellation or restart-repair results. Tool execution is split into
ordered preparation, bounded safe dispatch, and ordered commit. Hooks can reuse
or reject calls and can stage the next user-visible Step input, but they cannot
direct the loop themselves. See [Dynamic Tooling and the Tool
Scheduler](tooling-and-scheduler.md).

## Session isolation and delegation

RuntimeManager gives each session its own inbox, driver, cancellation state,
tool manager, pending approval, and progress routing. Workspace-wide services
such as the provider manager, registry, transcript store, compiler, repository
index, and state store are shared.

Subagents are child sessions that use the same core loop but do not share their
parent's conversation or mutable runtime. Their queue and lifecycle are durable.
See [Multi-Agent Architecture](multi-agent.md).

## Core ownership rules

- Providers normalize their wire protocol into canonical completions and stream
  events. They do not build durable messages or run tools.
- The registry decides which tool schemas are visible for a request; the Tool
  Manager still validates and enforces execution policy.
- sessionlog defines event formats and replays surfaces. It does not choose
  context, tool policy, or completion.
- Runtime hooks are advisory or policy checks around a tool. They cannot mutate
  the model surface or finish a Turn.
- Plan Mode is evaluated at every Step. An approved exit_plan_mode only affects
  the following Step.

## Related

- [Runtime Composition](runtime-composition.md)
- [Session Context and Request Construction](session-context.md)
- [Dynamic Tooling and the Tool Scheduler](tooling-and-scheduler.md)
- [Multi-Agent Architecture](multi-agent.md)
