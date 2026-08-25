# Supremo

Supremo is a local Go coding agent with a terminal user interface (TUI), durable sessions, deterministic context construction, bounded parallel tools, and isolated subagents. It keeps workspace history in SQLite, streams durable events through one backend contract, and applies session-scoped approval rules before side effects.

## Install Supremo

Download a checksum-verified build from [GitHub Releases](https://github.com/AbhaySingh002/Supremo/releases), or install the latest Unix release:

```sh
curl -fsSL https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.sh | sh
```

On Windows, run:

```powershell
irm https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.ps1 | iex
```

If Go is installed, you can also run:

```sh
go install github.com/AbhaySingh002/supremo/cmd/supremo@latest
```

Build the repository with:

```sh
git clone https://github.com/AbhaySingh002/Supremo.git
cd Supremo
make build
./supremo --version
```

## Start an interactive session

Run Supremo inside the repository you want it to inspect or change:

```sh
supremo
```

Configure the provider, choose a model, and initialize workspace memory from the TUI:

```text
/provider
/model
/init
explain this repository and run its focused tests
```

`/provider` opens a masked credential flow when the selected provider is not configured. `/model` refreshes and searches text-generation models across configured providers. The TUI keeps chat, tool activity, approvals, plan questions, and subagents on the same durable session stream.

## Run without the TUI

Use one-shot mode for scripts and continuous integration (CI):

```sh
supremo --prompt "summarize this repository"
echo "run the focused tests" | supremo
supremo --resume session_id --prompt "continue the task"
```

One-shot runs are dry-run by default. Add `--approve` to allow mutating tools. Add `--plan` to research and save a plan without executing it.

Process-only provider settings use these flags:

```sh
supremo --provider openai --model model_id --api-key your_api_key_here --prompt "inspect this project"
supremo --provider openai-compatible --endpoint http://localhost:11434/v1 --model model_id --prompt "inspect this project"
```

Flags override `SUPREMO_PROVIDER`, `SUPREMO_MODEL`, `SUPREMO_ENDPOINT`, and `SUPREMO_API_KEY`. Environment variables override saved configuration.

Start the loopback application programming interface (API) server with:

```sh
supremo serve --listen 127.0.0.1:0
```

The server prints its URL and bearer token as JSON. See [COMMANDS.md](COMMANDS.md) for the complete CLI, slash-command, and keyboard reference.

## Control tool safety

Approval mode belongs to the active session:

- `strict` asks before tools marked as approval-required
- `batman` is the default; it runs routine work and asks before higher-risk actions
- `superman` auto-approves tool actions
- `dry-run` reports mutating actions without applying them
- Plan Mode permits research and planning, not workspace mutation

`execute_command` is not sandboxed. It is the command path for builds, tests, formatters, and repository scripts. Filesystem tools use path locks, read-before-write hashes, atomic writes, and optional rewind checkpoints. A stale write fails when another agent changes the observed file first.

## Understand the runtime

```text
TUI -> api.Client -> backend.Service ---------┐
HTTP/SSE -> transport/http -> backend.Service ┤
one-shot CLI -> app.AgentAPI -----------------┤
                                             v
                                      RuntimeManager
                           session Agent A | session Agent B | child Agent
                                             |
                         context compiler | providers | tool scheduler
                                             |
                                SQLite state + artifact objects
```

The backend admits runs, enforces idempotency, serves snapshots, and streams ordered events. Each live session receives an isolated agent runtime. A request compiler rebuilds and measures the exact provider-visible envelope before every model call. Safe read operations may overlap, but tool results commit in model order. Delegated agents run as durable child sessions with bounded authority.

Read [PROJECT.md](PROJECT.md) for package ownership, request flow, recovery behavior, and architecture scenarios.

## Locate local data

Supremo stores configuration under the operating system's user configuration directory in `supremo/`. Set `SUPREMO_DATA_DIR` to choose another location. The directory contains:

- `global.db`: workspace identity registry
- `config.yaml` and `credentials.json`: provider settings and credentials
- `workspaces/workspace_id/state.db`: sessions, messages, events, plans, interactions, and repository index
- `workspaces/workspace_id/objects/`: content-addressed prompt and tool artifacts
- `workspaces/workspace_id/checkpoints/`: rewind data

Supremo migrates legacy workspace-local `.supremo/` state when it first opens that workspace. It sends prompts, selected workspace evidence, tool observations, and configured web requests to the chosen provider. It has no built-in telemetry. Semantic repository indexing is opt-in because it sends selected source chunks to the configured embedding endpoint.

## Contribute changes

Read these documents before editing:

- [AGENTS.md](AGENTS.md): binding instructions for AI coding agents
- [PROJECT.md](PROJECT.md): architecture, ownership, and runtime scenarios
- [COMMANDS.md](COMMANDS.md): user commands and keyboard actions
- [DEVELOPMENT.md](DEVELOPMENT.md): debug builds, logging, release process, and full validation

Run the repository checks with:

```sh
make precommit
```

Continuous integration runs race tests, vet, release builds, and cross-compilation. A `v*` tag creates release archives and checksums.
