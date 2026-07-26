# Privacy and local state

Supremo does not include analytics or telemetry.

## What stays on your machine

- `~/.supremo/config.yaml` stores the selected provider and model.
- `~/.supremo/credentials.json` stores provider API keys with private file permissions.
- `.memory/MEMORY.md` and `.memory/progress.md` store repository knowledge and recent work.
- `.session/` stores conversation checkpoints and active plans.
- `.scratchpad/` stores full oversized tool output; prompt history contains only a bounded snippet.

`/krypton` removes `.memory/`, `.session/`, and `.scratchpad/` from the current workspace. It intentionally does not remove global credentials.

## What leaves your machine

When a task calls the configured Gemini provider, Supremo sends the system prompt, applicable project instructions (`SUPREMO.md` or `AGENTS.md`), selected workspace memory, conversation context, and tool observations needed for that task. The provider's privacy terms apply to that request.

`web_fetch` makes a direct request to the URL you supply. `run_build` and `run_tests` execute repository code. `execute_command` runs only after approval and is not sandboxed.

Use `/dry-run`, `/tools`, `/activity`, and `/doctor` to inspect behavior before running a task.
