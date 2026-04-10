# tacli Roadmap

Reference baseline: `ultraworkers/claw-code`

## Ordered plan

1. [x] Add an `init` workflow
   - Add `tacli init` and `/init`.
   - Scaffold `CLAW.md`, `.claw/`, and local ignore rules with idempotent behavior.
   - Tailor starter guidance from repo detection instead of writing a generic template.

2. [x] Strengthen the command/control plane
   - Split high-level command handlers from chat-runtime state plumbing.
   - Add clear top-level parity between direct commands and slash commands where it improves usability.
   - Make plan/status/help surfaces reflect the real repo files and runtime state.

3. [x] Add command-pattern permissions
   - Extend tool-level policy into command-level rules for `run_command`.
   - Support explicit allow/deny patterns for common risky commands.
   - Persist and inspect those rules from chat and CLI entrypoints.

4. [x] Build a parity harness inspired by claw-code
   - Add deterministic runtime scenarios for tool loops, retries, sessions, and command failures.
   - Keep live-model E2E tests separate from fast deterministic coverage.
   - Use the harness to pin down provider-compatibility regressions.

5. [ ] Introduce a capability layer
   - Group higher-level workflows into capability packs instead of adding more loose tools.
   - Promote agent roles and task modes into explicit runtime objects.
   - Use that layer to stage future LSP integration and project init extensions.

## Execution notes

- Finish each item with tests before moving to the next one.
- Keep claw-code as a reference for control-plane shape, init ergonomics, and parity coverage, not as a line-by-line port target.
