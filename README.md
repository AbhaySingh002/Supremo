# Supremo

Supremo is a local Go coding agent. It uses Gemini, keeps its working state in the current repository, and asks before tools modify files or run arbitrary commands.

## Install

Download a checksum-verified release for macOS, Linux, or Windows from [GitHub Releases](https://github.com/AbhaySingh002/Supremo/releases), or install the latest Unix release:

```sh
curl -fsSL https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.ps1 | iex
```

The installers verify the downloaded release checksum. Set `SUPREMO_INSTALL_DIR` to choose a destination; the default is `~/.local/bin`.

If you already have Go, use:

```sh
go install github.com/AbhaySingh002/supremo/cmd/supremo@latest
```

To build from source:

```sh
git clone https://github.com/AbhaySingh002/Supremo.git
cd Supremo
make build
./supremo --version
```

## Quick start

Run Supremo inside the repository you want it to work on:

```text
supremo
> /auth YOUR_GEMINI_API_KEY
> /init
> explain this codebase and run its tests
```

`/init` writes a small repository snapshot to `.memory/MEMORY.md`. Add project rules to `SUPREMO.md` (or `AGENTS.md`); Supremo injects them into each task's context.

## Commands

| Command | Purpose |
| --- | --- |
| `/help` | List commands. |
| `/init` | Create workspace memory. |
| `/doctor` | Check local setup without making a provider request. |
| `/tools` | Show tools and which require approval. |
| `/activity` | Show recent local tool executions. |
| `/plan` | Toggle planned, checkpointed tool execution. |
| `/plan status`, `/plan show`, `/plan resume` | Inspect or resume the active plan. |
| `/approve`, `/deny`, `/dry-run`, `/cancel` | Control a running task. |
| `/auth`, `/model`, `/config` | Configure Gemini. |
| `/clear`, `/reset`, `/krypton` | Clear conversation, state, or workspace traces. |

`run_build` and `run_tests` execute repository code automatically. File-changing tools, formatters, and `execute_command` require approval. `execute_command` is an approved escape hatch and is not sandboxed.

## Local state and privacy

- Credentials and provider configuration stay in `~/.supremo/`.
- Workspace memory, plans, checkpoints, and large tool-output scratch files stay in `.memory/`, `.session/`, and `.scratchpad/` in the current repository.
- `/krypton` removes only those workspace files; it keeps `~/.supremo/`.
- Prompts, selected workspace memory, tool observations, and configured web fetches are sent to the configured Gemini provider when a task runs. Supremo has no built-in telemetry.

See [the privacy and state guide](docs/privacy.md) for details.

## Development

```sh
make precommit
git config core.hooksPath .githooks
```

CI runs race tests, vet, builds, and cross-compilation on every push and pull request. Pushing a `v*` tag creates release archives and checksums.
