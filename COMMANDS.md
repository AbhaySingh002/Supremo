# Use Supremo commands

This reference covers the command-line interface (CLI), interactive slash commands, local TUI actions, and keyboard controls. Run `/help` inside the TUI for the command catalog built into the current binary.

## Start Supremo from the shell

| Command | Action |
| --- | --- |
| `supremo` | Open the interactive TUI in the current directory |
| `supremo "prompt"` | Run one prompt and print the response |
| `supremo --prompt "prompt"` or `supremo -p "prompt"` | Run one explicit prompt |
| `echo "prompt" \| supremo` | Read a prompt from standard input |
| `supremo --session session_id --prompt "prompt"` | Load or create a named session |
| `supremo --resume session_id --prompt "prompt"` | Resume an existing session |
| `supremo --plan --prompt "objective"` | Research and save a plan without executing it |
| `supremo --approve --prompt "task"` | Permit mutating tools during a one-shot run |
| `supremo serve --listen 127.0.0.1:0` | Start the authenticated loopback API server |
| `supremo --version` or `supremo version` | Print the version |
| `supremo --help` | Print CLI usage |

Runtime flags are `--provider`, `--model`, `--endpoint`, and `--api-key`. They apply only to the current process. `serve` also accepts these flags plus `--listen` and `--debug`.

## Manage conversations

| Command | Action |
| --- | --- |
| `/help` | Open command and keyboard help |
| `/clear` | Clear the current conversation |
| `/reset` | Reset conversation and agent state |
| `/init` | Record a workspace snapshot and load existing repository instructions |
| `/krypton` | Purge Supremo state for this workspace after typed confirmation |
| `/session [list\|current\|new [id]\|switch id]` | Open or manage sessions |
| `/new` | Start a blank session |
| `/delete-session` | Open the session deletion selector |
| `/rename-session name` | Rename the current session |
| `/copy` | Copy the last assistant response |
| `/export [path]` | Export the current session as Markdown |
| `/diff` | Open the workspace diff viewer |
| `/exit` | Exit Supremo |

## Control runs and plans

| Command | Action |
| --- | --- |
| `/plan` | Toggle Plan Mode |
| `/plan objective` | Start a planning turn for an objective |
| `/plan status\|show\|execute\|resume\|cancel` | Inspect or control the active plan |
| `/tasks` | Show durable tasks and plan status |
| `/ux [status]` | Show checklist, rewind, and retry preferences |
| `/ux checklist on\|off` | Toggle checklist projection |
| `/ux rewind on\|off` | Toggle file checkpoints |
| `/ux retry on\|off` | Toggle provider retry preference |
| `/side` | Open a tool-free side-question surface |
| `/rewind` | Select and restore a file checkpoint |
| `/approve` | Approve the pending tool call |
| `/deny [reason]` | Deny the pending tool call |
| `/dry-run` | Toggle dry-run behavior for mutating tools |
| `/mode [strict\|batman\|superman]` | Inspect, cycle, or set approval mode |
| `/strict`, `/batman`, `/superman` | Set an approval mode directly |
| `/cancel` | Cancel the active run or Plan Mode |

## Inspect tools and context

| Command | Action |
| --- | --- |
| `/tools` | List registered tools and effective policies |
| `/activity` | Show recent tool activity |
| `/doctor` | Check workspace, provider, and tool health without a model request |
| `/context status` | Show current context totals and state |
| `/context show` | Open the latest context manifest |
| `/index semantic status` | Inspect semantic-index configuration |
| `/index semantic on\|off` | Enable or disable semantic lookup |

## Configure providers and models

| Command | Action |
| --- | --- |
| `/provider` | Open the provider selector |
| `/provider provider_id [endpoint]` | Activate a provider or named compatible route |
| `/providers` | List registered providers and configuration state |
| `/auth` | Enter the active provider credential in a masked surface |
| `/endpoint url` | Set the active provider endpoint |
| `/model` | Refresh configured providers and open the unified model picker |
| `/model model_id` | Select a model on the active provider |
| `/models` or `/models refresh` | Open the same model picker |
| `/usage [refresh]` | Show completion usage and available provider account metadata |
| `/config` | Show configuration status |
| `/config reload` | Reload configuration from disk |
| `/config embeddings credential_provider endpoint model` | Configure an OpenAI-compatible embedding route |

Credentials entered through `/auth` are masked and excluded from transcript history. Selecting an unconfigured provider opens endpoint and credential setup before model selection.

## Use keyboard controls

| Context | Keys | Action |
| --- | --- | --- |
| Composer | `Enter` | Send text or accept the current surface |
| Composer | `Shift+Enter`, `Alt+Enter`, `Ctrl+J` | Insert a newline |
| Composer | `Tab` | Complete a slash command or workspace mention |
| Composer | `@` | Open file and directory mentions |
| Composer | `/` | Open slash-command completion |
| Composer | `Ctrl+R` | Search prompt history |
| Composer | `Ctrl+M` | Cycle approval mode |
| Composer | `Ctrl+P` | Open saved plans |
| Composer | `Ctrl+B` | Toggle the activity rail or inspector |
| Composer | `Ctrl+L` | Clear terminal presentation scrollback |
| Composer | `?` | Open help |
| Transcript | `Up`, `Down`, `Page Up`, `Page Down` | Scroll |
| Transcript | `Home`, `End` | Jump to the first or latest entry |
| Transcript | `Enter` or `e` | Expand the latest tool details when available |
| Transcript | `Space` | Expand or collapse the latest tool group |
| Transcript | `Ctrl+N` | Return focus to the composer |
| Plan ready | `Ctrl+X` | Execute the ready plan |
| Plan question | `1` to `9`, arrows, `Enter`, `Space` | Select an option |
| Approval | `y` or `Enter` | Allow the pending action |
| Approval | `n` or `Escape` | Deny the pending action |
| Approval | `e` | Edit and revalidate tool arguments |
| Approval | `a` | Switch the session to auto-approve |
| Selectors and viewers | Arrows, `Page Up`, `Page Down`, `Home`, `End` | Navigate or scroll |
| Any overlay | `Escape` | Close it and restore the prior focus |
| Active run | `Ctrl+C` | Request cancellation |

Enter `! command` in the composer to run a local host-shell command in the workspace. This is a frontend-local action and is not an agent tool call.
