package agent

import (
	"sort"
	"strings"
)

type PromptContext struct {
	MemoryText   string
	WorkDir      string
	Shell        string
	ApprovalMode string
	Model        string
	ToolNames    []string
	Skills       []PromptSkill
	Instructions []PromptInstructionFile
	GitBranch    string
	GitStatus    string
	SessionMode  string
	AgentRole    string
}

type PromptSkill struct {
	Name        string
	Description string
	Path        string
}

type PromptInstructionFile struct {
	Path    string
	Content string
}

const baseSystemPrompt = `You are an interactive agent that helps users with software engineering tasks.

System:
- Use tools when they reduce uncertainty. Prefer evidence over assumptions.
- Keep reasoning private. Never expose chain-of-thought or hidden analysis.
- Never emit <think>, </think>, <thinking>, analysis tags, or hidden-reasoning markers.
- Stay inside the workspace boundary. Do not access files outside the workspace.
- Prefer concise, actionable responses. The visible reply must be tool calls or the user-facing answer.
- Keep multi-step work tracked with update_todo; refresh it when state changes.
- Keep work in the foreground unless a subtask is clearly long-running. Inspect background jobs before relying on them.

Doing tasks:
- Read relevant code before changing it.
- Do not add speculative abstractions or broad refactors unless they are required.
- For workspace tasks: inspect first, edit second, verify third.
- Run the smallest useful command or edit that moves the task forward.
- When a user asks for a review, prioritize findings, regressions, and missing tests over summaries.
- Stop as soon as the task is complete.

Actions with care:
- Carefully consider reversibility and blast radius before running commands or editing files.
- Avoid destructive or risky commands unless explicitly required.
- Check whether the worktree already contains user changes before modifying related files.
- Prefer targeted diffs and preserve unrelated user edits.

Output style:
- Plain text only.
- Short paragraphs are allowed; lists use "- " or "1. ".
- No Markdown headings, bold markers, tables, block quotes, or fenced code blocks.
- Do not narrate intent ("let me", "I will", "first I will").
- Do not repeat the user's request unless necessary.
- Keep final responses concise, actionable, and terminal-friendly.
- When files changed, report what changed and any remaining risk.`

func BuildSystemPrompt(ctx PromptContext) string {
	sections := []string{
		baseSystemPrompt,
		renderRuntimeSection(ctx),
		renderGitSection(ctx),
		renderInstructionSection(ctx.Instructions),
		renderRoleSection(ctx),
		renderToolSection(ctx.ToolNames),
		renderSkillSection(ctx.Skills),
		renderMemorySection(ctx.MemoryText),
	}
	var out []string
	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section != "" {
			out = append(out, section)
		}
	}
	return strings.Join(out, "\n\n")
}

func SystemPromptWithMemory(memoryText string) string {
	return BuildSystemPrompt(PromptContext{MemoryText: memoryText})
}

func renderRuntimeSection(ctx PromptContext) string {
	var lines []string
	if strings.TrimSpace(ctx.SessionMode) != "" {
		lines = append(lines, "session_mode="+strings.TrimSpace(ctx.SessionMode))
	}
	if strings.TrimSpace(ctx.WorkDir) != "" {
		lines = append(lines, "workdir="+strings.TrimSpace(ctx.WorkDir))
	}
	if strings.TrimSpace(ctx.Shell) != "" {
		lines = append(lines, "shell="+strings.TrimSpace(ctx.Shell))
	}
	if strings.TrimSpace(ctx.Model) != "" {
		lines = append(lines, "model="+strings.TrimSpace(ctx.Model))
	}
	if strings.TrimSpace(ctx.ApprovalMode) != "" {
		lines = append(lines, "approval_mode="+strings.TrimSpace(ctx.ApprovalMode))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Runtime context:\n- " + strings.Join(lines, "\n- ")
}

func renderRoleSection(ctx PromptContext) string {
	role := strings.TrimSpace(strings.ToLower(ctx.AgentRole))
	if role == "" {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ctx.SessionMode)), "background:") {
			role = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ctx.SessionMode)), "background:")
		}
	}
	switch role {
	case "explore":
		return "Role guidance (explore):\n- Primary objective: inspect and map the codebase quickly.\n- Prefer read-only tools and commands.\n- Do not edit files unless explicitly requested."
	case "plan":
		return "Role guidance (plan):\n- Primary objective: produce a concrete execution plan.\n- Break work into testable steps with clear ordering.\n- Minimize implementation details unless they are blockers."
	case "implement":
		return "Role guidance (implement):\n- Primary objective: make targeted code changes.\n- Prefer minimal diffs and verify affected behavior.\n- Report changed files and remaining risks."
	case "verify":
		return "Role guidance (verify):\n- Primary objective: validate implementation quality.\n- Run build/tests/type-check/commands when possible.\n- Return verdict with evidence, failures, and confidence."
	default:
		return ""
	}
}

func renderGitSection(ctx PromptContext) string {
	branch := strings.TrimSpace(ctx.GitBranch)
	status := strings.TrimSpace(ctx.GitStatus)
	if branch == "" && status == "" {
		return ""
	}
	lines := []string{"Project context:"}
	if branch != "" {
		lines = append(lines, "- git_branch="+branch)
	}
	if status != "" {
		lines = append(lines, "- git_status:")
		for _, line := range strings.Split(status, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines = append(lines, "  "+line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderInstructionSection(files []PromptInstructionFile) string {
	if len(files) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "Instruction files:")
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		content := strings.TrimSpace(file.Content)
		if path == "" || content == "" {
			continue
		}
		lines = append(lines, "- "+path+":")
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines = append(lines, "  "+line)
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func renderToolSection(names []string) string {
	if len(names) == 0 {
		return ""
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	if len(sorted) > 32 {
		sorted = sorted[:32]
	}
	return "Available tools:\n- " + strings.Join(sorted, "\n- ")
}

func renderSkillSection(skills []PromptSkill) string {
	if len(skills) == 0 {
		return ""
	}
	items := append([]PromptSkill(nil), skills...)
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	if len(items) > 16 {
		items = items[:16]
	}

	var lines []string
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		line := name
		desc := strings.TrimSpace(item.Description)
		if desc != "" {
			line += ": " + desc
		}
		path := strings.TrimSpace(item.Path)
		if path != "" {
			line += " (" + path + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return "Available skills:\n- " + strings.Join(lines, "\n- ")
}

func renderMemorySection(memoryText string) string {
	memoryText = strings.TrimSpace(memoryText)
	if memoryText == "" {
		return ""
	}
	return memoryText
}
