# Code Generation Guidelines

## Principles
* **Simplicity Over Cleverness**: Write simple, readable, and boring code. Avoid complex patterns or over-engineering unless explicitly required.
* **Minimal Diff**: Make the smallest change that completely solves the problem. Do not refactor unrelated code unless requested or necessary to fix a systemic bug.
* **Idiomatic Go**: Write standard, idiomatic Go code. Follow established naming conventions, leverage standard libraries where possible, and avoid introducing unneeded dependencies.
* **Preserve Formatting**: Match the existing code formatting, indentation (tabs vs spaces), bracket style, and comment styles exactly.

## Code Quality
* **Abstractions**: Do not introduce interfaces, packages, or wrappers unless requested. Reuse existing helpers, utilities, or types already defined in the codebase.
* **Error Handling**: Never ignore errors. Return errors with clear context, handle resource cleanup using `defer`, and fail early at boundaries.
* **Documentation & Comments**: Write comments only where the code logic is non-obvious or contains important business decisions. Do not write verbose comments that merely restate the code.
* **Performance & Safety**: Write thread-safe code. Be aware of complexity bounds, avoid goroutine leaks, and ensure locks are released cleanly.
