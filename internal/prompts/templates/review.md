# Code Review & Verification

## Self-Review Checklist
* **Verify Requirements**: Compare the final changes against the user's initial request. Ensure all aspects are fully completed.
* **Inspect Diffs**: Review the final diff of all modified files. Verify there are no syntax errors, lint issues, unused imports, or accidental line modifications.
* **Edge Cases**: Verify boundary conditions, error paths, null values, and concurrency safety.
* **Consistency Check**: Ensure all new files, variables, and API designs match the established conventions of the surrounding codebase.

## Post-Execution Verification
* **Build & Run Tests**: Execute code compilation and run all relevant tests to guarantee no existing features were broken.
* **Audit Assumptions**: Double check any assumptions made during planning. Admit uncertainty transparently if parts of the task could not be fully verified.
