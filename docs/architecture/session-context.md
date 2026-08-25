# Session Context and Request Construction

This document explains the difference between durable history, the
model-visible session Surface, compact working state, and one exact provider
request.

## Four layers of memory

~~~text
SQLite event log            immutable source of truth
        ↓ replay
Session Surface             ordered visible event sequence IDs
        ↓ derive
Conversation messages       user, assistant, and tool-result messages only
        +
Context compiler output     control, selected workspace evidence, tool schemas
        ↓ freeze
Provider envelope           system + ordered messages + ordered tool definitions
~~~

The event log retains more than the model sees. Lifecycle events, stream
chunks, retries, approval interactions, plans, todos, manifests, and
compaction metrics are durable audit data. They are deliberately excluded from
the conversation history.

WorkingMemory is separate structured task state. It records current focus,
evidence, goals, failures, and optional model directives. It helps the compiler
choose context; it never replaces the event log as ground truth.

## Building a request

For every Step, RealContextBuilder calls context.Compiler.Prepare.
Preparation only reads state. It loads the working set, session Surface,
repository evidence, active profile, provider limit, and tool catalog; selects
bounded candidates; and produces a provider-neutral prompt plus an uncommitted
manifest.

The Agent then makes the request deterministic:

1. Deep-copy the system text, ordered messages, and tool definitions.
2. Compact each tool JSON schema and sort definitions by tool name.
3. Remove non-provider metadata from copied messages.
4. Serialize the envelope as canonical JSON and calculate SHA-256 request,
   header, system, and tool-schema digests.
5. Measure that exact frozen envelope.

Only after the request passes context-pressure checks does Commit save its
manifest and development trace. Immediately before the provider call, the
stream recorder persists the frozen envelope as an artifact and writes
request/context; it writes request/header when the header is initial, resumed,
or changed. Trace IDs and timestamps are outside the provider-visible digest.

## Pressure recovery and compaction

The Agent uses this sequence, not a rough pre-build estimate:

~~~text
prepare → freeze → measure exact envelope
   │
   ├─ below threshold → commit → provider
   │
   └─ pressured → prune or compact durable Surface → rebuild
~~~

The token meter counts the frozen system text, tool definitions, and messages
once. At 80% of the context limit, recovery first deterministically reduces an
oversized visible tool result. It preserves the call ID and keeps a full-output
artifact. If pruning cannot help, the compaction engine summarizes the oldest
safe prefix while retaining a recent tail.

Compaction preserves causality. It moves the cutoff back whenever it would
separate an assistant tool call from any of its tool results. The provider sees
the same frozen system and tool definitions plus one final instruction that
identifies the old prefix to summarize and the retained tail to exclude.

A summary is rejected if it is empty, ended because of max_tokens, is not
smaller than the replaced range, or was generated after that range changed.
Successful compaction appends a replacement Surface event; it does not rewrite
or discard the original messages.

Each Step permits at most three pressure-recovery passes. Every successful pass
must advance Surface generation and reduce estimated tokens, otherwise the Step
stops with a context-not-converging error. A provider context-overflow error
uses the same rebuild path and is retried only after durable reduction.

## Recovery

On session load, sessionlog.RepairTail closes unmatched Turns and Steps,
synthesizes results for unresolved calls, and examines unmatched
compaction/start records. If the matching replacement exists it marks the
compaction recovered-completed; otherwise it appends an interrupted failed end
record. This keeps session replay and provider history causal after a crash.

## Related

- [Agent Core](agent-core.md)
- [Dynamic Tooling and the Tool Scheduler](tooling-and-scheduler.md)
- [Multi-Agent Architecture](multi-agent.md)
