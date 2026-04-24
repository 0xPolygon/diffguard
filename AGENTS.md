# AGENTS.md

Instructions for AI coding agents working in this repository.

## Task Completion Checklist

Before considering any task complete, the agent MUST:

- [ ] Build the project (`make build`) and confirm it compiles without errors.
- [ ] Run the full test suite (`make test`) and confirm all tests pass.
- [ ] **Run `diffguard` on this code and confirm it exits 0.** A task is NOT complete until diffguard passes on the changes. Use `./diffguard .` from the repo root (or `diffguard --paths <changed-paths> .` to scope to specific files).
- [ ] Resolve any diffguard violations before reporting the task as done. Do not suppress, skip, or work around violations — fix the underlying code.
- [ ] Verify the changes address the original request (no partial implementations, no TODOs left behind).

If diffguard has not been run, the task is not complete — regardless of whether tests pass or the code compiles.
