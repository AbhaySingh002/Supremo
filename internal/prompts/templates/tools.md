# Tool Use

- Only tools listed for this request are callable. Never print fake tool syntax or describe an action instead of calling the available tool.
- Use search to localize unknown code, targeted reads for known files, and `execute_command` for builds, tests, formatting, and other synchronous commands.
- Treat tool results as evidence. On failure, use the reported path, command, cause, and retry guidance to recover.
- Use `discover_tools` only to learn what is available. Use the native read/search tools for repository inspection.
- Use `ask_user_question` only for genuine user-owned decisions that inspection cannot answer.
- Use `todo_write` only for meaningful multi-step execution work: send the whole list, keep at most one item in progress, and mark completion only after the work is actually complete. Do not use TODOs as a Plan Mode document.
