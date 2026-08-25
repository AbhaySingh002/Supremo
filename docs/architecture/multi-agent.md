# Multi-Agent Architecture

Supremo delegates work by creating durable child sessions. A child has the same
agent loop as a root session, but a distinct runtime and a standalone prompt.
It does not receive its parent's conversation history.

~~~text
parent Agent
  └─ subagent tool
       → SubagentManager
         → child session + descriptor + first queued message (one transaction)
         → one FIFO worker for that child
         → child RuntimeManager runtime
         → subagent/run.start → child Turn → subagent/run.end
~~~

## Child identity and inheritance

The child session records its durable parent ID, origin: subagent, label,
depth, and immutable scope. It inherits provider, model, approval mode, and
dry-run policy from its parent. The creation transaction also appends:

- subagent/descriptor; and
- subagent/message.queued for the initial prompt.

That atomic admission means a crash cannot leave a usable child session without
its identity or first task.

## Scope and authority

| Scope | Allowed work |
| --- | --- |
| local_read | Local side-effect-free inspection only. It is both read-only and research-only. |
| execution | May use the parent-inherited execution policy; it cannot widen approval or dry-run policy. |

Background execution is the default. A foreground request waits for the same
durable child run result. Delegation has a maximum depth of three. A
local_read child cannot create an execution descendant, and Plan Mode forces
new children to local_read. Delegated agents cannot ask the human directly;
they return unresolved choices to their parent.

The model-facing controls are subagent, list_agents, send_message, wait_agent,
and interrupt_agent:

- Only the direct durable parent can send a message to or wait on a child.
- A parent can list direct children or all descendants.
- Any ancestor may interrupt a descendant's active work.
- Self, sibling, stale, and unknown lineage requests are rejected.

## Queueing, execution, and recovery

Each child has one worker and one active turn. Messages append to the child log
and execute FIFO. The worker writes subagent/run.start, drives the normal child
Agent loop, then writes subagent/run.end with structured status, output, and
error. Idle child runtimes are released and can be cold-resumed from their
durable session later.

On startup, a queued message without a start record resumes. An unmatched run
start is completed only if durable child history proves that its turn completed;
otherwise it is marked interrupted and is never replayed, avoiding duplicate
side effects.

Sibling children may run concurrently because they have different runtimes and
session logs. They still share the workspace. Filesystem CAS enforces the
conflict boundary: a child records a read hash, another actor changes the file,
and the stale child write fails after path locking and an on-disk hash check.
Concurrent creates and renames use the filesystem tool's existing exclusive or
ordered path locking.

## Client API

The transport-neutral backend exposes agent.start, agent.list, agent.send,
agent.wait, and agent.interrupt alongside root session/run operations. Agent
start and message send require idempotency keys, so a client retry cannot enqueue
duplicate delegated work.

## Related

- [Agent Core](agent-core.md)
- [Runtime Composition](runtime-composition.md)
- [Dynamic Tooling and the Tool Scheduler](tooling-and-scheduler.md)
