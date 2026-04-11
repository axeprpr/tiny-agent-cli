package i18n

var messages = map[string]map[string]string{
	LangEN: messagesEN,
	LangCN: messagesCN,
}

var messagesEN = map[string]string{
	// Language selection
	"lang.prompt": "Select language / 选择语言:",
	"lang.en":     "1) English",
	"lang.cn":     "2) 中文",
	"lang.ask":    "Enter 1 or 2: ",
	"lang.saved":  "Language set to English.",

	// TUI placeholders & hints
	"tui.placeholder.default":  "Type a message or /command...",
	"tui.placeholder.approval": "Approval: y approve / n reject / a always allow",
	"tui.placeholder.busy":     "Running, you can keep typing...",
	"tui.hint.approval":        "Waiting for your choice",
	"tui.hint.busy":            "Task running",
	"tui.hint.send":            "",

	// TUI status
	"tui.status.ready":          "ready",
	"tui.status.running":        "running",
	"tui.status.error":          "error",
	"tui.status.approval":       "approval required",
	"tui.status.drawer.shown":   "activity drawer shown",
	"tui.status.drawer.hidden":  "activity drawer hidden",
	"tui.status.filter.updated": "activity filter updated",
	"tui.status.bg.update":      "background job update",

	// TUI labels
	"tui.label.messages":    "%d messages",
	"tui.label.plan":        "plan",
	"tui.label.activity":    "activity  %s  %d",
	"tui.label.no.activity": "No tool activity yet.",
	"tui.label.no.match":    "No matching activity.",

	// Approval inline
	"tui.approval.command": "Command approval needed",
	"tui.approval.write":   "File write approval needed",
	"tui.approval.general": "Your approval is needed",
	"tui.approval.hint":    "Type y to approve, n to reject, a to always allow",
	"tui.approval.waiting": "Approval still waiting, type y to approve, n to reject, a to always allow",
	"tui.approval.granted": "approval granted",
	"tui.approval.denied":  "approval denied",
	"tui.approval.always":  "approval granted and mode switched to dangerously",
	"tui.approval.mode":    "approval mode switched to dangerously",
	"tui.approval.bar":     "[y] yes   [n] no   [a] always dangerously",

	// Terminal approval
	"approval.cmd.header":     "Command approval required:",
	"approval.cmd.prompt":     "Run? [y]es / [n]o / [a]lways dangerously for this session: ",
	"approval.write.header":   "File write approval required:",
	"approval.write.prompt":   "Write file? [y]es / [n]o / [a]lways dangerously for this session: ",
	"approval.switched":       "approval mode switched to dangerously for this session",
	"approval.invalid":        "please answer y, n, or a",
	"approval.cmd.need.tty":   "command approval requires an interactive terminal; rerun with --dangerously to skip prompts",
	"approval.write.need.tty": "file write approval requires an interactive terminal; rerun with --dangerously to skip prompts",

	// /help
	"help": `Commands:
  /help                 Show this help
  /interrupt            Interrupt the current foreground task
  /init                 Scaffold CLAW.md and local repo artifacts
  /exit, /quit          Exit the chat
  /reset                Clear conversation context
  /session [name|new]   Switch or create a session
  /session save         Save current session state
  /session restore      Restore current session state
  /status               Show session and config status
  /plan                 Show plan.md
  /compact              Compact conversation context now
  /agents               Inspect or manage background subagents
  /hooks                Show or change hook configuration
  /mcp                  Manage configured MCP servers and resources
  /plugin               List or load plugins
  /reload-plugins       Reload already loaded plugins
  /skills               List discovered skills
  /capabilities         List bundled capability packs
  /scope                Show current project scope key
  /model <name>         Switch model for this session
  /policy ...           Show or change persisted tool and command policy
  /tasks ...            Manage persistent project tasks
  /review [base] [target] [--staged] [--path <path>]
                        Review the current git diff with optional scope
  /approval <mode>      Set approval mode (prompt|read-only|workspace-write|danger-full-access|allow)
  /memory               Show or manage saved memory notes
  /memory team ...      Show or manage team-scoped memory notes
  /remember <text>      Save a project memory note
  /remember-global <t>  Save a global memory note
  /forget <query>       Remove matching project memory
  /forget-global <q>    Remove matching global memory
  /memorize             Extract memory from conversation
  /bg <task>            Start a background job
  /bg-role <r> <task>   Start a role-based background job
  /jobs                 List background jobs
  /job <id>             Inspect a background job
  /job-send <id> <msg>  Send follow-up to a background job
  /job-cancel <id>      Cancel a background job
  /job-apply <id>       Apply job result to chat context
  /audit stats          Show recent tool-audit summary
  /audit tail [n]       Show latest tool-audit events
  /audit errors [n]     Show latest failed tool-audit events
  /debug-tool-call      Show details for the latest tool call
  /debug-tool-call tail [n]
                        Show detailed history for recent tool calls
  /debug-tool-call errors [n]
                        Show detailed history for recent failed tool calls
  /debug-tool-call replay
                        Replay the latest audited tool call
  /debug-turn           Show the latest conversation turn summary
  /debug-turn tail [n]
                        Show recent conversation turn summaries

Or just type naturally -- no command needed for most tasks.`,

	// Commands
	"cmd.reset":              "context reset",
	"cmd.interrupt.ok":       "interrupt requested",
	"cmd.interrupt.idle":     "no foreground task is running",
	"cmd.interrupt.pending":  "interrupting current task",
	"cmd.interrupt.done":     "foreground task interrupted",
	"cmd.queue.added.one":    "queued next prompt",
	"cmd.queue.added.many":   "queued prompt (%d pending)",
	"cmd.queue.starting":     "starting queued prompt: %s",
	"cmd.queue.remaining":    "%d still queued",
	"cmd.approval.usage":     "usage: /approval prompt|read-only|workspace-write|danger-full-access|allow",
	"cmd.approval.set":       "approval mode set to %s",
	"cmd.output.deprecated":  "output mode command is deprecated in chat; terminal rendering is now the default",
	"cmd.model.usage":        "usage: /model <name>",
	"cmd.model.set":          "model set to %s for this session",
	"cmd.bg.started":         "started background job %s",
	"cmd.bgrole.usage":       "usage: /bg-role <general|explore|plan|implement|verify> <task>",
	"cmd.job.usage":          "usage: /job <id>",
	"cmd.job.unknown":        "unknown job %q",
	"cmd.jobsend.usage":      "usage: /job-send <id> <text>",
	"cmd.jobsend.ok":         "queued follow-up for %s",
	"cmd.jobcancel.usage":    "usage: /job-cancel <id>",
	"cmd.jobcancel.ok":       "canceled %s",
	"cmd.jobapply.usage":     "usage: /job-apply <id>",
	"cmd.jobapply.ok":        "applied %s into current chat context",
	"cmd.audit.usage.stats":  "usage: /audit stats",
	"cmd.audit.usage.tail":   "usage: /audit tail [n]",
	"cmd.audit.usage.errors": "usage: /audit errors [n]",
	"cmd.debugtool.usage":    "usage: /debug-tool-call [tail|errors [n]|replay]",
	"cmd.debugturn.usage":    "usage: /debug-turn [tail [n]]",
	"cmd.audit.empty":        "no audit events yet",
	"cmd.audit.no_errors":    "no failed audit events in the recent window",
	"cmd.remember.usage":     "usage: /remember <text>",
	"cmd.remember.ok":        "project memory saved",
	"cmd.rememberg.usage":    "usage: /remember-global <text>",
	"cmd.rememberg.ok":       "global memory saved",
	"cmd.forget.usage":       "usage: /forget <query>",
	"cmd.forget.ok":          "removed %d project memory note(s)",
	"cmd.forgetg.usage":      "usage: /forget-global <query>",
	"cmd.forgetg.ok":         "removed %d global memory note(s)",
	"cmd.memorize.err":       "memorize error: %v",
	"cmd.memorize.ok":        "added %d memory note(s)",
	"cmd.session.already":    "already on session %s",
	"cmd.session.switched":   "switched to session %s",
	"cmd.session.started":    "started session %s",

	// Memory responses
	"mem.reject":               "This is not suitable for long-term memory; state a stable preference or project fact directly.",
	"mem.saved.global":         "Saved as global preference.",
	"mem.saved.project":        "Saved as project memory.",
	"mem.no.global.delete":     "No global memory to delete.",
	"mem.no.project.delete":    "No project memory to delete.",
	"mem.no.delete":            "No memory to delete.",
	"mem.deleted.last.global":  "Deleted the most recent global memory.",
	"mem.deleted.last.project": "Deleted the most recent project memory.",
	"mem.forget.what":          "Please specify what to forget.",
	"mem.no.match":             "No matching memory found.",
	"mem.deleted.mixed":        "Deleted %d memory note(s).",
	"mem.deleted.global":       "Deleted %d global memory note(s).",
	"mem.deleted.project":      "Deleted %d project memory note(s).",

	// Usage
	"usage.header":       "Usage:",
	"usage.examples":     "Examples:",
	"usage.error.config": "invalid config: %v\n",
	"usage.error.task":   "missing task",
	"usage.error.agent":  "agent error: %v\n",
	"usage.error.chat":   "chat setup error: %v\n",
	"usage.error.ui":     "chat ui error: %v\n",
	"auto.memory.err":    "auto-memory error: %v\n",
	"auto.memory.ok":     "auto-memorized %d note(s)\n",
}
