# Repository instructions for coding agents

This file is the single source of technical instructions for AI coding agents modifying Supremo. It applies to the entire repository. Read [PROJECT.md](PROJECT.md) before changing package boundaries and [DEVELOPMENT.md](DEVELOPMENT.md) before changing build or release behavior.

## Work from evidence

1. Read the request and inspect the full code path before editing
2. Check `git status --short` and preserve unrelated or pre-existing changes
3. Search with `rg` or `rg --files`; inspect every caller of shared code you change
4. Reuse an existing helper, standard-library feature, platform feature, or installed dependency before adding code
5. Fix the root cause at the narrowest shared boundary
6. Implement the smallest complete change, then run the smallest relevant check

Do not add speculative abstractions, plugin systems, dependencies, schema migrations, compatibility layers, or configuration switches. Add one only when the task requires it and the existing contracts cannot express the behavior.

## Preserve architecture boundaries

- `internal/app` is the composition root. Register providers, tools, hooks, runtimes, subagents, and the backend there
- `internal/api` owns the transport-neutral frontend contract and event data transfer objects (DTOs)
- `internal/backend` owns durable run admission, idempotency, snapshots, interactions, recovery, and subscriptions
- `internal/ui` is a frontend. It may depend on `internal/api` and UI packages, but must not import agent, state, provider, tool, repository, context, or backend implementation packages
- `internal/agent` owns turn and step lifecycles, request pressure recovery, tool-result ordering, session runtimes, and subagent orchestration
- `internal/context` prepares provider-neutral requests without writes; commit manifests only for requests that will be sent
- `internal/state` and `internal/sessionlog` own durable storage, typed events, replay, projections, and artifacts
- `internal/providers` adapts provider protocols. It must not own agent-loop policy or durable messages
- `internal/tools` owns tool metadata, policy, approvals, checkpoints, and execution. Filesystem compare-and-swap (CAS) checks remain in filesystem tools

Interactive frontends must use `api.Client`. The legacy `app.AgentAPI` exists only for one-shot CLI behavior and focused tests. Do not add new frontend calls to concrete agents.

## Protect durable behavior

- Persist lifecycle gates before calling a provider or tool; fail closed when required persistence fails
- Keep message rows, projections, and model-surface events atomic
- Preserve provider-visible tool-call and tool-result pairs during pruning, compaction, cancellation, and recovery
- Build request digests from the frozen provider-visible envelope, excluding trace identifiers and timestamps
- Advance context state only at durable objective and tool-observation boundaries
- Keep one mutable runtime per session; share only workspace-wide services
- Treat `ParallelSafe` as false unless a tool is proven reentrant; dispatch safe bodies concurrently but commit results in model order
- Keep subagent identity, queueing, scope, lineage, and terminal results durable; never widen inherited authority
- Preserve existing sessions and API version 1 wire behavior unless the task explicitly changes compatibility

## Make safe changes

- Never expose credentials through DTOs, events, logs, errors, command history, or rendered output
- Validate inputs at trust boundaries and preserve cancellation with `context.Context`
- Wrap errors with operation context; retain both causes with `errors.Join` when terminal persistence also fails
- Preserve filesystem path locking, atomic writes, read hashes, and stale-write diagnostics
- Do not use destructive Git commands, overwrite unrelated work, or reformat untouched files
- Do not add a database migration or dependency without explaining why the current schema or modules cannot support the change
- Keep production builds free of debug logging and keep log redaction intact

## Test in proportion to risk

Run focused tests while editing, then validate the affected boundary:

```sh
go test ./internal/package_name
go test -race ./internal/concurrent_package
```

Before handing off a repository-wide or cross-package change, run:

```sh
go test ./...
go vet ./...
go build ./cmd/supremo
```

Run `make precommit` for concurrency, storage, provider, release, or broad UI changes. Apply `gofmt` to changed Go files. Add one focused regression test for non-trivial logic; avoid duplicate fixtures and tests that assert implementation details. Update terminal render fixtures only when the requested visual behavior changes.

## Finish cleanly

Confirm that documentation and examples match the implemented behavior, no stale filename references remain, and `git diff --check` passes. Report changed files, validation commands, and any unresolved risk. Do not commit unless the user asks.
