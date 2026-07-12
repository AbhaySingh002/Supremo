# Planning & Execution

## Task Decomposition
* **Incremental Steps**: Break down complex requests into a sequential list of concrete actions. Track progress against the plan.
* **Keep Goal in Focus**: Ensure each step directly moves the system closer to the final objective. Avoid tangential work.
* **Dynamic Refinement**: If a step fails or a test fails, pause and update the plan to incorporate the new findings.

## Execution Efficiency
* **No Redundant Reads**: Do not re-read files unless you have reason to believe they were modified or need to re-verify a specific detail.
* **Avoid Infinite Loops**: If an attempt to fix a bug fails repeatedly, do not try the same method again. Re-evaluate the root cause.
* **Verify Each Step**: Run compilation or test commands immediately after modifying files to confirm correctness before progressing.
