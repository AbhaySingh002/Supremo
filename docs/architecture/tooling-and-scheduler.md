# Dynamic Tooling and the Tool Scheduler

Supremo has a static in-process tool registry, but each provider request gets a
dynamic, bounded view of that registry. Visibility and permission are separate:
a visible schema may be called by the model, but the Tool Manager still enforces
the runtime policy before side effects occur.

## Tool routing

At startup, internal/app registers each built-in tool with a ToolMetadata
record. Metadata declares its family, capabilities, access level, side effect,
supported modes, approval requirement, inspection behavior, and ParallelSafe
status. The registry validates this into a ToolDescriptor and builds a
deterministic catalog.

For a request, the context compiler routes descriptors using:

- the active mode and plan/read-only restrictions;
- explicitly required capabilities;
- the task objective, plan step, and working set;
- earlier tool observations and failures; and
- bootstrap and planning-core flags.

Execution requests deliberately expose every eligible schema so work can
continue without a separate activation turn. Planning, exploration, audit, and
side-answer requests expose a narrower selection. discover_tools is a small
bootstrap tool that returns matching names and descriptions without sending all
argument schemas to the provider. Automatic task-match activation is capped at
six schemas; candidates and rejections are recorded in the context manifest.

The chosen ActiveTools list is both the provider schema list and the
prompt-scoped execution allowlist. A fabricated or no-longer-visible name is
denied before argument validation, approval, or execution.

## Execution policy

Before a tool body runs, the Manager checks the active allowlist, schema,
research/read-only restrictions, dry-run mode, and approval policy. Mutating
filesystem tools also enforce trusted read-before-mutate observations, path
locking, on-disk hash comparison, atomic writes, and optional checkpoints.
This is what protects shared workspace writes made by independent agents.

The three approval modes are session-scoped:

| Mode | Effect |
| --- | --- |
| strict | Every tool marked RequiresApproval waits for a decision. |
| batman | Most marked mutations wait; ordinary file changes run, while dependency-manifest changes still wait. |
| superman | Marked tools run without an approval pause. |

An approved call can use the original input, an edited and revalidated input,
or a durable denial. A session has its own pending approval, so an approval for
one session cannot release another session's mutation.

## Bounded parallel scheduling

The scheduler receives tool calls in model order and uses three stages:

~~~text
prepare in model order
  persist tool/call → run BeforeTool hooks → resolve cached or blocked calls

dispatch
  overlap only consecutive ParallelSafe bodies, at most four at once

commit in model order
  persist tool result → run AfterTool hooks → observe context → apply control
~~~

ParallelSafe defaults to false. Supremo marks only proven reentrant work safe,
such as file inspection/listing, repository and text searches, web fetch, and
selected delegation calls. Workspace mutations, commands, approvals,
interaction, plan/todo transitions, git inspection, and unknown tools remain
exclusive.

An exclusive call is a barrier: it waits for the earlier safe pool to drain and
finishes before later calls begin. A later fast read may finish physically
first, but its result, hooks, and context observation wait until earlier calls
commit. The next provider Step therefore always sees tool results in the
model's order.

On cancellation, the scheduler stops replenishing its pool, drains work that
already started, and writes causal cancelled results for unstarted calls. A
lifecycle persistence failure stops new dispatch. A recoverable tool failure
stops later groups without abandoning already-running safe calls.

## Related

- [Agent Core](agent-core.md)
- [Session Context and Request Construction](session-context.md)
- [Multi-Agent Architecture](multi-agent.md)
