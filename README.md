<div align="center">

# Supremo

**Deterministic, local-first agentic coding in your terminal.**

[![Latest Release](https://img.shields.io/github/v/release/AbhaySingh002/Supremo?style=flat-square&color=E5A93C)](https://github.com/AbhaySingh002/Supremo/releases)
[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey?style=flat-square)](https://github.com/AbhaySingh002/Supremo/releases)
[![Zero Telemetry](https://img.shields.io/badge/Telemetry-Zero%20%2F%20Local--First-10B981?style=flat-square)](PROJECT.md)

<br/>

<p align="center">
  <a href="#quickstart">Quickstart</a> &nbsp;•&nbsp;
  <a href="#visual-overview">Visual Tour</a> &nbsp;•&nbsp;
  <a href="#key-pillars">Core Pillars</a> &nbsp;•&nbsp;
  <a href="#safety--approvals">Safety & Approvals</a> &nbsp;•&nbsp;
  <a href="#runtime-architecture">Architecture</a> &nbsp;•&nbsp;
  <a href="#command-reference">Reference</a>
</p>

---

<p align="center">
  <a href="docs/architecture/init.png">
    <img src="docs/architecture/init.png" alt="Supremo Welcome Screen" width="100%" />
  </a>
</p>
<p align="center">
  <sub><strong>Launch Canvas:</strong> Real-time token budget meter (<code>0k/1048k</code>), active model indicator, approval state badge, and keystroke-driven controls.</sub>
</p>

<br/>

<p align="center">
  <a href="docs/architecture/demo.png">
    <img src="docs/architecture/demo.png" alt="Supremo Live Tool Execution" width="100%" />
  </a>
</p>
<p align="center">
  <sub><strong>Autonomous Execution:</strong> Real-time streamed reasoning, bounded shell execution, precise answer generation, and deterministic token accounting.</sub>
</p>

</div>

---

## Why Supremo?

Most AI coding tools wrap language models in opaque cloud infrastructure with hidden prompt injections and unpredictable context retention. **Supremo** is engineered from first principles as an auditable, local-first engineering partner:

<table>
  <tr>
    <td width="50%">
      <h3>🔒 Local-First & Zero Telemetry</h3>
      <p>Workspace state, session transcripts, and credentials remain exclusively on your machine. Network calls only occur between your machine and your chosen model provider API.</p>
    </td>
    <td width="50%">
      <h3>📊 Deterministic Token Envelope</h3>
      <p>Every request compiles an exact, frozen provider envelope with real-time token metering. Pruning and compaction maintain tool-call/result pairs without silent context loss.</p>
    </td>
  </tr>
  <tr>
    <td width="50%">
      <h3>⚡ Charm v2 Terminal Interface</h3>
      <p>Powered by the latest <code>bubbletea/v2</code>, <code>lipgloss/v2</code>, and <code>bubbles/v2</code>. Features native terminal window titles, OS task progress bars, and zero-latency feed rendering.</p>
    </td>
    <td width="50%">
      <h3>🛡️ Safe by Construction</h3>
      <p>Multi-tier approval gates (<code>strict</code>, <code>batman</code>, <code>superman</code>, <code>dry-run</code>). Filesystem tools use atomic writes, path locking, and Compare-and-Swap (CAS) hash validation.</p>
    </td>
  </tr>
  <tr>
    <td width="50%">
      <h3>🧭 Plan Mode & Subagents</h3>
      <p>Draft architectural plans and clarify tradeoffs before modifying code. Delegate isolated subagents with bounded authority and durable child session lifecycles.</p>
    </td>
    <td width="50%">
      <h3>🔌 Dual-Mode Ergonomics</h3>
      <p>Use the full interactive TUI for exploratory pair-programming, or run headless one-shot CLI commands and an authenticated loopback HTTP/SSE server for automation.</p>
    </td>
  </tr>
</table>

---

## Quickstart

### 1. Install Supremo

#### Unix (macOS & Linux)
```sh
curl -fsSL https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.sh | sh
```

#### Windows (PowerShell)
```powershell
irm https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.ps1 | iex
```

#### Via Go (1.24+)
```sh
go install github.com/AbhaySingh002/supremo/cmd/supremo@latest
```

<details>
<summary><strong>Build from Source</strong></summary>

```sh
git clone https://github.com/AbhaySingh002/Supremo.git
cd Supremo
make build
./supremo --version
```
</details>

---

### 2. Launch an Interactive Session

Navigate to any repository and start Supremo:

```sh
cd /path/to/your/project
supremo
```

Within the TUI, configure your provider and model in seconds:

1. **Configure Provider**: Type `/provider` to select Anthropic, OpenAI, Mistral, OpenRouter, or a custom OpenAI-compatible endpoint (e.g. Ollama, vLLM). Enter your API key in the masked credential prompt.
2. **Select Model**: Type `/model` to search and switch models with real-time capability checks.
3. **Index Workspace**: Type `/init` to snapshot repository files and load existing workspace guidelines (`AGENTS.md`, `README.md`).
4. **Prompt**: Ask Supremo to inspect your codebase, plan changes, or run tests:
   ```text
   explain the authentication flow and run the unit tests
   ```

---

## Visual Overview

### Top Bar & Telemetry Meter
```text
SUPREMO  ~/Desktop/Projects/supermo  openrouter · google/gemini-3.6-flash  ask risky  [▓░░░░░░░░░] 4k/1048k
```
- **Workspace Path**: Active working directory and git root.
- **Provider & Model Chip**: Currently active model routing and provider status.
- **Approval Badge**: Active safety mode (`ask risky` in default `batman` mode).
- **Token Budget Meter**: Real-time context consumption against model window limits.

### Transcript & Tool Pipeline
- **Live Streamed Reasoning**: Model thoughts and status steps stream directly into the feed.
- **Tool Invocations**: Shell commands, filesystem edits, and search operations are recorded with exit statuses (`✓ Ran`, `✓ Wrote`, `✓ Read`).
- **Collapsible Batches**: Press <kbd>Space</kbd> to collapse or expand tool execution groups.

### Bottom Composer
- **Prompt Input**: Multiline text input with history search (<kbd>Ctrl</kbd>+<kbd>R</kbd>).
- **Workspace Mentions**: Type `@` to fuzzy-search and attach files or directories into context.
- **Command Palette**: Type `/` to open the searchable slash-command menu.

---

## Safety & Approvals

Supremo never executes arbitrary mutations behind your back. Every session runs under an explicit approval policy:

| Mode | Trigger Command | Read Actions | Mutating Actions | Best For |
| :--- | :--- | :--- | :--- | :--- |
| **`batman`** *(Default)* | `/batman` | Auto-approved | Prompts for confirmation (`y`/`n`/`e`) | Everyday development |
| **`strict`** | `/strict` | Prompts | Prompts for confirmation | Audited or production environments |
| **`superman`** | `/superman` | Auto-approved | Auto-approved | Hands-free CI runs and trusted scripts |
| **`dry-run`** | `/dry-run` | Auto-approved | Simulated & logged without modifying disk | Pre-execution verification |
| **Plan Mode** | `/plan` | Allowed | Blocked; research & planning only | Multi-step architecture reviews |

> **Filesystem Integrity Guarantee:** File writes use read-before-write hashes and Compare-and-Swap (CAS) locking. If a file is concurrently modified on disk, Supremo rejects stale writes and prompts for resolution.

---

## Headless & Automation

Supremo integrates cleanly into shell scripts, CI/CD pipelines, and editor extensions.

### One-Shot Prompts
```sh
# Run a dry-run summary
supremo --prompt "summarize recent changes in this repository"

# Run tests and permit mutations
supremo --approve --prompt "run the test suite and fix failing cases"

# Pipe prompt via standard input
git diff | supremo --prompt "review this diff for memory leaks"

# Resume an existing session
supremo --resume <session_id> --prompt "continue the benchmark analysis"
```

### Loopback API Server
Run Supremo as a local daemon exposing loopback JSON-RPC and Server-Sent Events (SSE):

```sh
supremo serve --listen 127.0.0.1:0
```
The daemon outputs its loopback endpoint and bearer token as JSON on startup, allowing external applications to subscribe to event streams and drive sessions.

---

## Command & Keyboard Reference

### Essential Slash Commands
| Command | Action |
| :--- | :--- |
| `/provider` | Switch provider or configure credentials |
| `/model` | Search and switch models on active provider |
| `/plan [objective]` | Toggle Plan Mode or research an architectural objective |
| `/diff` | Open the full interactive workspace diff viewer |
| `/tasks` | Inspect durable tasks, checklists, and plan execution state |
| `/session` | List, switch, rename, or create isolated sessions |
| `/rewind` | Revert workspace files to earlier checkpoints |
| `/doctor` | Verify workspace, tool, and provider connectivity |
| `/init` | Record workspace snapshot and load repository guidelines |

### Keyboard Shortcuts
| Key | Context | Action |
| :--- | :--- | :--- |
| <kbd>Enter</kbd> | Composer | Send prompt or confirm modal |
| <kbd>Shift</kbd>+<kbd>Enter</kbd> | Composer | Insert newline |
| <kbd>@</kbd> | Composer | Mention files or directories |
| <kbd>/</kbd> | Composer | Open slash-command menu |
| <kbd>Ctrl</kbd>+<kbd>M</kbd> | Composer | Cycle approval modes (`batman` ➔ `strict` ➔ `superman`) |
| <kbd>Ctrl</kbd>+<kbd>P</kbd> | Composer | Toggle Plan Mode |
| <kbd>Ctrl</kbd>+<kbd>R</kbd> | Composer | Search command and prompt history |
| <kbd>Space</kbd> | Transcript | Expand or collapse latest tool batch |
| <kbd>Up</kbd> / <kbd>Down</kbd> | Transcript | Scroll conversation history |
| <kbd>Esc</kbd> | Global | Dismiss modal, close menu, or restore focus |
| <kbd>Ctrl</kbd>+<kbd>C</kbd> | Active Run | Cancel current model run or tool execution |

---

## Architecture

Supremo enforces clean package boundaries with complete separation between frontend interaction, orchestration, and provider adapters:

```text
┌────────────────────────────────────────────────────────┐
│                      Frontends                         │
│   Interactive TUI (Charm v2)  │  Headless CLI  │  HTTP/SSE API  │
└───────────────────────────┬────────────────────────────┘
                            │ api.Client (Version 1 Contract)
                            ▼
┌────────────────────────────────────────────────────────┐
│                   backend.Service                      │
│   Durable Runs │ Idempotency │ Snapshots │ Event Streams│
└───────────────────────────┬────────────────────────────┘
                            ▼
┌────────────────────────────────────────────────────────┐
│                   RuntimeManager                       │
│      Session Agent A  │  Session Agent B  │  Subagents  │
│  ────────────────────────────────────────────────────  │
│   Context Compiler   │  Tool Scheduler  │  Providers   │
│   (Frozen Envelope)  │  (CAS Locks)     │  (Adapters)  │
└───────────────────────────┬────────────────────────────┘
                            ▼
┌────────────────────────────────────────────────────────┐
│            Local Storage & Temporary Artifacts         │
│   Config & Credentials  │  Transcripts  │  Checkpoints │
└────────────────────────────────────────────────────────┘
```

For deeper architectural details, request lifecycles, and subagent orchestration, see [PROJECT.md](PROJECT.md).

---

## Contributing & Development

We welcome contributions! Please review our core guidelines before submitting pull requests:

- [AGENTS.md](AGENTS.md) — Technical instructions and architectural rules.
- [PROJECT.md](PROJECT.md) — Package boundaries and lifecycle maps.
- [COMMANDS.md](COMMANDS.md) — Comprehensive command and shortcut reference.
- [DEVELOPMENT.md](DEVELOPMENT.md) — Debug builds, logging system, and release workflow.

To validate your changes locally:
```sh
make precommit
```

---

<div align="center">
<sub>Built with Go and <a href="https://charm.sh">Charm</a> • Maintained by <a href="https://github.com/AbhaySingh002">AbhaySingh002</a></sub>
</div>
