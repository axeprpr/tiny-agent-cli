# tiny-agent-cli: Remaining Work to Match claw-code

Based on PARITY.md analysis. Work in /root/.openclaw/workspace/tiny-agent-cli.

## Already Done (Phases 1-10)
- Hooks pipeline ✅
- Plugin system ✅
- Bundled skills ✅
- CLI commands (/plan, /compact, /hooks, /plugin, /skills, /memory, /session) ✅
- Session & Memory persistence ✅
- glob_search tool ✅
- MCP client ✅
- Permission store ✅

## Remaining Gaps (Priority Order)

### P1 - Agent Orchestration (`/agents` command)
1. Add `internal/agent/orchestration.go` with subagent management
2. Add `/agents` command to main.go (list, delegate, cancel subagents)
3. Wire background agent spawning into registry
4. Add agent session lifecycle management
5. Commit: "feat(agents): add agent orchestration and /agents command"

### P2 - Structured IO / Remote Transport
1. Add `internal/transport/structured.go` for JSON streaming
2. Improve JSON mode output cleanliness (no extra text before JSON)
3. Add structured output mode for programmatic use
4. Commit: "feat(transport): add structured IO for clean JSON output"

### P3 - Task Management (`/tasks` command)
1. Add `internal/tasks/tasks.go` with task registry
2. Add `/tasks` command (list, create, update, delete tasks)
3. Wire task tool into registry
4. Commit: "feat(tasks): add task management system and /tasks command"

### P4 - Code Review Tool (`/review` command)
1. Add `internal/tools/review.go` for diff-based code review
2. Add `/review` command to main.go
3. Commit: "feat(review): add /review command for diff-based code review"

### P5 - Team Memory Integration
1. Improve memory store with team-level memory
2. Add /memory team commands
3. Commit: "feat(memory): add team memory support"

### P6 - Settings Sync & Policy
1. Add config sync to remote settings endpoint
2. Improve policy enforcement
3. Commit: "feat(config): add settings sync and policy improvements"

### P7 - Integration & Polish
1. Run full test suite
2. Fix any remaining issues
3. Commit: "feat: integration polish and testing"

## Rules
- After each priority section, run: go build ./... && go test ./... (if tests exist)
- Commit with clear message and push
- Commit at LEAST once per section
- Keep changes focused and small

Start with P1 (Agent Orchestration). After each commit, push.
