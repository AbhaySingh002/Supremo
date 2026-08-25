# Development and Releases

## Build Targets

Supremo supports distinct development and production build configurations via opt-in Go build tags:

```sh
# Development build with local diagnostic logging enabled:
make dev

# Production/release build (clean, stripped, zero logging overhead):
make release

# Run unit tests and race checks:
make test
make precommit
```

---

## Development Logging System

Development builds (`-tags debug`) automatically create structured, timestamped logs inside the local project root:

```text
<project-root>/.supremo-dev/
└── logs/
    └── supremo-debug.log
```

### Logged Events
- **Lifecycle**: Startup flags, binary version, session IDs, clean shutdowns.
- **Panics**: Uncaught panics, panic values, and full runtime stack traces.
- **Plan Workflow**: State transitions (`PLAN_RESEARCH` → `PLAN_BUILDING` → `PLAN_READY` → `EXECUTING_PLAN`), step starts/completions, and blocked states.
- **Tool Invocations**: Tool call counts, tool execution warnings, non-success statuses, and errors.
- **Model/Provider Protocols**: Validation failures, protocol repair retries, and dropped completions.
- **Agent turn lifecycle**: Request IDs, L0–L8 context selection (with provenance and rejections), canonical messages, pre-dispatch provider payloads, parsed turn progress, tool cache vs physical execution, and Request N+1 state transitions.
- **Checkpoints & Storage**: Preflight errors and snapshot validation issues.
- **TUI / Bubble Tea**: Bubble Tea internal events and TUI termination errors.

### Secret Redaction
All log writes pass through automatic credential sanitizer that redacts:
- Bearer tokens (`Bearer [REDACTED]`)
- Provider API keys (`sk-[REDACTED]`, `api_key=[REDACTED]`)
- Password fields and authorization headers (`[REDACTED]`)

### Production Cleanliness Guarantee
Production releases (`make release` or `make build`) compile out all logging implementations using `//go:build !debug`. Release builds will never create `.supremo-dev/` or write log files to disk.

SWE request assembly is slot-based (`selectForDecision`). Debug logs include per-slot token totals (control, constraints, focus, verified_fact, exact_source, feedback, conversation, tools). Conversational chat still fills a 12-turn window.

### meal_tracker Phase 2 ACI

After `make dev`, rerun the meal_tracker SWE task and compare against Phase 1. There is no round-limit kill switch.

Log in the debug turn traces (do not add a product kill switch):

| Metric | Phase 1 | Phase 2 |
| requests | | |
| tokens | | |
| ranged vs whole-file `read_file` | | |
| searches | | |
| edits (`replace_in_file` / `write_file`) | | |
| verify | | |

Fill the table from `.supremo-dev/logs/supremo-debug.log` after a real run.


---

## Release

The release tag and `VERSION` must match. Prepare both on `main` before pushing the tag:

```sh
git pull --ff-only origin main
printf 'vX.Y.Z\n' > VERSION
git add .
git commit -m "what Fixed.  Not the version release name ."
git tag -a vX.Y.Z
git push origin main --follow-tags
```

Pushing the tag starts the GitHub release workflow. It builds the archives and publishes the release; it does not write a follow-up commit.

Installers read the latest tag from the raw `VERSION` file in `main`, then download the matching release asset and `checksums.txt`. The release workflow publishes Linux and macOS archives for amd64 and arm64, plus Windows ZIPs for amd64 and arm64.

After the first tagged release, smoke test installers:

```sh
curl -fsSL https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.sh | sh
# Open a new terminal if this was the first installation.
supremo --version
```

```powershell
irm https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.ps1 | iex
supremo --version
```
