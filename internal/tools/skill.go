package tools

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const skillFileName = "SKILL.md"

type Skill struct {
	Name            string
	Description     string
	Instructions    string
	Path            string
	Source          string
	ToolDefinitions []string
	Enabled         bool
	DisabledReason  string
}

type skillProbe func(workDir string) (bool, string)

var (
	skillLookupPath = exec.LookPath
	skillEnv        = os.Getenv
)

func DiscoverSkills(workDir string) ([]Skill, error) {
	roots, err := skillRoots(workDir)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	out := append([]Skill(nil), BundledSkills()...)
	for _, item := range out {
		key := strings.ToLower(strings.TrimSpace(item.Path))
		if key == "" {
			key = "bundled:" + strings.ToLower(strings.TrimSpace(item.Name))
		}
		seen[key] = true
	}
	for _, root := range roots {
		items, err := discoverSkillsInRoot(root)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key := strings.ToLower(strings.TrimSpace(item.Path))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, item)
		}
	}

	for i := range out {
		out[i] = evaluateSkillAvailability(out[i], workDir)
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name+"\x00"+out[i].Path) < strings.ToLower(out[j].Name+"\x00"+out[j].Path)
	})
	return out, nil
}

func skillRoots(workDir string) ([]string, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workdir: %w", err)
	}
	codeHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codeHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			codeHome = filepath.Join(home, ".codex")
		}
	}

	roots := []string{
		filepath.Join(absWorkDir, ".codex", "skills"),
		filepath.Join(absWorkDir, ".agents", "skills"),
	}
	if strings.TrimSpace(codeHome) != "" {
		roots = append(roots, filepath.Join(codeHome, "skills"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".config", "tiny-agent-cli", "skills"))
	}

	uniq := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		uniq = append(uniq, abs)
	}
	return uniq, nil
}

func discoverSkillsInRoot(root string) ([]Skill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skill root %s: %w", root, err)
	}

	var out []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(root, entry.Name())
		skillFile := filepath.Join(skillDir, skillFileName)
		data, err := os.ReadFile(skillFile)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read skill file %s: %w", skillFile, err)
		}
		skill := parseSkillMarkdown(entry.Name(), skillFile, string(data))
		out = append(out, skill)
	}
	return out, nil
}

func parseSkillMarkdown(fallbackName, path, text string) Skill {
	name := strings.TrimSpace(fallbackName)
	desc := ""
	var toolDefs []string
	meta, body := splitSkillFrontmatter(text)
	if metaName := strings.TrimSpace(meta["name"]); metaName != "" {
		name = metaName
	}
	if metaDesc := strings.TrimSpace(meta["description"]); metaDesc != "" {
		desc = metaDesc
	}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			header := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if header != "" && name == fallbackName {
				name = header
			}
			continue
		}
		if strings.HasPrefix(line, "Tool:") || strings.HasPrefix(line, "Tools:") {
			label := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "Tools:"), "Tool:"))
			for _, item := range strings.Split(label, ",") {
				item = strings.TrimSpace(strings.TrimPrefix(item, "-"))
				if item != "" {
					toolDefs = append(toolDefs, item)
				}
			}
			if desc == "" {
				continue
			}
		}
		if desc == "" {
			desc = line
		}
	}

	if name == "" {
		name = fallbackName
	}
	if desc == "" {
		desc = "No description"
	}

	return Skill{
		Name:            strings.TrimSpace(name),
		Description:     strings.TrimSpace(desc),
		Instructions:    firstNonEmptySkillValue(strings.TrimSpace(body), strings.TrimSpace(text)),
		Path:            strings.TrimSpace(path),
		Source:          "local",
		ToolDefinitions: uniqueSkillToolNames(toolDefs),
		Enabled:         true,
	}
}

var frontmatterKeyPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9_-]*)\s*:`)

func splitSkillFrontmatter(text string) (map[string]string, string) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "---") {
		return nil, text
	}
	if strings.HasPrefix(trimmed, "---\n") || strings.HasPrefix(trimmed, "---\r\n") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) < 3 {
			return nil, text
		}
		end := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
		if end == -1 {
			return nil, text
		}
		meta := parseFrontmatterBlock(strings.Join(lines[1:end], "\n"))
		body := strings.Join(lines[end+1:], "\n")
		return meta, body
	}
	rest := trimmed[3:]
	end := strings.Index(rest, "---")
	if end == -1 {
		return nil, text
	}
	meta := parseFrontmatterBlock(rest[:end])
	body := rest[end+3:]
	return meta, body
}

func parseFrontmatterBlock(block string) map[string]string {
	block = strings.TrimSpace(block)
	if block == "" {
		return nil
	}
	if !strings.Contains(block, "\n") {
		return parseInlineFrontmatter(block)
	}
	out := make(map[string]string)
	currentKey := ""
	var currentValues []string
	flush := func() {
		if currentKey == "" || len(currentValues) == 0 {
			return
		}
		out[currentKey] = strings.TrimSpace(strings.Join(currentValues, " "))
	}
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.ToLower(strings.TrimSpace(line[:idx]))
			if isFrontmatterKey(key) {
				flush()
				currentKey = key
				currentValues = currentValues[:0]
				if value := strings.TrimSpace(line[idx+1:]); value != "" {
					currentValues = append(currentValues, value)
				}
				continue
			}
		}
		if currentKey == "" {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if value != "" {
			currentValues = append(currentValues, value)
		}
	}
	flush()
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseInlineFrontmatter(line string) map[string]string {
	matches := frontmatterKeyPattern.FindAllStringSubmatchIndex(line, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make(map[string]string)
	for i, match := range matches {
		key := strings.ToLower(strings.TrimSpace(line[match[2]:match[3]]))
		if !isFrontmatterKey(key) {
			continue
		}
		valueStart := match[1]
		valueEnd := len(line)
		if i+1 < len(matches) {
			valueEnd = matches[i+1][0]
		}
		value := strings.TrimSpace(line[valueStart:valueEnd])
		if value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isFrontmatterKey(key string) bool {
	if strings.TrimSpace(key) == "" {
		return false
	}
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func BundledSkills() []Skill {
	out := make([]Skill, len(bundledSkills))
	copy(out, bundledSkills)
	return out
}

func EnabledSkills(skills []Skill) []Skill {
	out := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		if skill.Enabled {
			out = append(out, skill)
		}
	}
	return out
}

func uniqueSkillToolNames(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func firstNonEmptySkillValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var bundledSkills = []Skill{
	{
		Name:            "memory",
		Description:     "Persistent memory workflow for project and global notes.",
		Instructions:    "Use /memory, /remember, /remember-global, /forget, and /memorize to manage durable context.",
		Path:            "bundled:memory",
		Source:          "bundled",
		ToolDefinitions: []string{"update_todo"},
		Enabled:         true,
	},
	{
		Name:            "planning",
		Description:     "Use plan.md as the source of truth for the current development plan.",
		Instructions:    "Read plan.md before implementation-heavy tasks and keep work aligned with the listed phases. If the path forward is still ambiguous, resolve missing constraints and open questions before locking the plan.",
		Path:            "bundled:planning",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "list_files"},
		Enabled:         true,
	},
	{
		Name:            "hooks",
		Description:     "Inspect configured tool hooks and validate tool execution policy before risky commands.",
		Instructions:    "Use /hooks to inspect or adjust hook configuration during interactive sessions.",
		Path:            "bundled:hooks",
		Source:          "bundled",
		ToolDefinitions: []string{"run_command"},
		Enabled:         true,
	},
	{
		Name:            "superpowers",
		Description:     "Escalate from basic execution to high-leverage workflows such as decomposition, verification, and constraint-aware planning.",
		Instructions:    "Use this skill for tasks that benefit from stronger structure. Break work into phases, surface assumptions early, keep a tight inspect-edit-verify loop, and explicitly choose the smallest high-leverage action that changes the outcome.",
		Path:            "bundled:superpowers",
		Source:          "bundled",
		ToolDefinitions: []string{"update_task_contract", "update_todo", "read_file", "run_command"},
		Enabled:         true,
	},
	{
		Name:            "pdf-processing",
		Description:     "Handle PDF-oriented tasks with a text-first, evidence-driven workflow.",
		Instructions:    "For PDF requests, first identify whether the user needs metadata, extracted text, page-level inspection, or conversion. Prefer deterministic extraction and summarize page boundaries, missing text, encoding problems, and verification limits explicitly.",
		Path:            "bundled:pdf-processing",
		Source:          "bundled",
		ToolDefinitions: []string{"inspect_pdf", "read_file"},
		Enabled:         true,
	},
	{
		Name:            "frontend-design",
		Description:     "Preserve or improve frontend quality with deliberate typography, hierarchy, and interaction decisions.",
		Instructions:    "For frontend work, avoid generic UI changes. State the intended visual direction, respect the existing design system when one exists, and verify layouts across mobile and desktop. Prefer specific hierarchy, spacing, color, and copy decisions over vague styling language.",
		Path:            "bundled:frontend-design",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "edit_file", "write_file"},
		Enabled:         true,
	},
	{
		Name:            "systematic-debugging",
		Description:     "Run debugging as a disciplined loop: reproduce, isolate, hypothesize, test, and confirm.",
		Instructions:    "For bugs, start by reproducing the issue or extracting the strongest available symptoms. Narrow the failing surface, test one hypothesis at a time, and preserve evidence after each step. Do not jump to code edits until the likely failure mechanism is clear enough to justify the change.",
		Path:            "bundled:systematic-debugging",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "run_command", "grep", "review_diff"},
		Enabled:         true,
	},
	{
		Name:            "marketing-skills",
		Description:     "Write positioning, messaging, and launch-oriented content with clear audience and outcome framing.",
		Instructions:    "When producing marketing-oriented material, identify the audience, value proposition, and CTA first. Prefer concrete messaging over hype, vary tone deliberately, and keep claims aligned with what the product or code actually does.",
		Path:            "bundled:marketing-skills",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "write_file", "edit_file"},
		Enabled:         true,
	},
	{
		Name:            "skill-creator",
		Description:     "Create or refine skills with crisp purpose, boundaries, and tool usage guidance.",
		Instructions:    "When defining a skill, make the trigger conditions explicit, keep instructions tight, and separate policy from implementation steps. The description must clearly state what the skill is for and when it should trigger, because that is what the agent uses for selection. Include expected tools, likely failure modes, when the skill should be skipped, and prefer reusable scripts or templates over long prose when the workflow is deterministic.",
		Path:            "bundled:skill-creator",
		Source:          "bundled",
		ToolDefinitions: []string{"write_file", "edit_file", "read_file"},
		Enabled:         true,
	},
	{
		Name:            "grill-me",
		Description:     "Pressure-test a plan, architecture, or requirement set by asking one high-leverage question at a time until the next step is unambiguous.",
		Instructions:    "Use this skill before implementation when the task still has major branching decisions, hidden assumptions, or unclear constraints. Ask exactly one focused question at a time. Prefer questions that expose missing interfaces, failure modes, ownership boundaries, acceptance criteria, or out-of-scope lines. Stop grilling once the next implementation step is specific enough to execute.",
		Path:            "bundled:grill-me",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "update_task_contract"},
		Enabled:         true,
	},
	{
		Name:            "tdd",
		Description:     "Use red-green-refactor with the smallest failing test at the public interface before broadening the implementation.",
		Instructions:    "For features and bug fixes that benefit from test-first development, start with the smallest failing test that demonstrates the intended behavior through a public interface. Make that test pass with the minimum code change, then refactor while keeping the tests green. Prefer behavior-focused coverage over tests that mirror internals, and say explicitly when strict TDD is not a good fit for the task.",
		Path:            "bundled:tdd",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "edit_file", "write_file", "run_command"},
		Enabled:         true,
	},
	{
		Name:            "to-prd",
		Description:     "Turn rough ideas into a buildable PRD or implementation brief with scope, constraints, open questions, and acceptance criteria.",
		Instructions:    "When the user wants a PRD, spec, or clearer implementation brief, capture the objective, target users, user-visible scope, constraints, non-goals, open questions, delivery slices, and acceptance criteria. Keep it concrete enough that another engineer could implement from the document without reopening basic scope questions.",
		Path:            "bundled:to-prd",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "write_file", "edit_file", "update_task_contract"},
		Enabled:         true,
	},
	{
		Name:            "git-guardrails",
		Description:     "Add or review git safety rails that block destructive commands and make risky repository operations explicit.",
		Instructions:    "Use this skill when the task involves protecting a repository from destructive git actions. Prefer hooks, policies, or wrappers that block resets, cleans, force-pushes, or protected-branch commits unless the user explicitly authorizes them. Keep the safe path obvious, the escape hatch deliberate, and the failure message actionable.",
		Path:            "bundled:git-guardrails",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "edit_file", "write_file", "run_command"},
		Enabled:         true,
	},
	{
		Name:            "webapp-testing",
		Description:     "Test web apps with a verification-first mindset: startup, routing, responses, assets, and user-visible outcomes.",
		Instructions:    "For webapp testing, define what must be true from a user perspective before running commands. Check the smallest useful surface first, such as build success, server reachability, route responses, and critical asset loading. Distinguish local verification from assumptions clearly.",
		Path:            "bundled:webapp-testing",
		Source:          "bundled",
		ToolDefinitions: []string{"check_webapp", "fetch_url", "run_command", "read_file"},
		Enabled:         true,
	},
	{
		Name:            "docx",
		Description:     "Handle DOCX-oriented requests with structure-aware extraction, conversion, and verification discipline.",
		Instructions:    "For DOCX tasks, separate container structure, document text, and formatting fidelity concerns. Make clear whether the goal is extraction, transformation, or validation, and report any unsupported formatting or media limitations explicitly.",
		Path:            "bundled:docx",
		Source:          "bundled",
		ToolDefinitions: []string{"inspect_docx", "read_file"},
		Enabled:         true,
	},
	{
		Name:            "changelog-maintenance",
		Description:     "Keep changelog entries accurate, scoped, and useful to downstream readers.",
		Instructions:    "When updating changelogs, group changes by user-facing impact rather than implementation trivia. Avoid speculative claims, call out breaking changes explicitly, and align entries with what was actually tested or shipped.",
		Path:            "bundled:changelog-maintenance",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "edit_file", "write_file"},
		Enabled:         true,
	},
	{
		Name:            "gpt-researcher",
		Description:     "Run structured research loops: clarify the question, gather evidence, compare findings, and synthesize limits.",
		Instructions:    "For research tasks, restate the target question precisely, gather multiple sources when the answer is unstable, and distinguish facts from inferences. Prefer a short synthesis with explicit unresolved questions over a long ungrounded summary.",
		Path:            "bundled:gpt-researcher",
		Source:          "bundled",
		ToolDefinitions: []string{"web_search", "fetch_url", "read_file"},
		Enabled:         true,
	},
	{
		Name:            "code-refactoring",
		Description:     "Approach refactors with behavior preservation, clear boundaries, and verification at each slice.",
		Instructions:    "For refactors, define the invariant first: what must not change. Prefer small mechanical moves, keep naming and dependency changes explicit, and verify behavior after each slice before broadening the refactor.",
		Path:            "bundled:code-refactoring",
		Source:          "bundled",
		ToolDefinitions: []string{"read_file", "edit_file", "write_file", "review_diff", "run_command"},
		Enabled:         true,
	},
}

func evaluateSkillAvailability(skill Skill, workDir string) Skill {
	if strings.TrimSpace(skill.Path) == "" {
		skill.Enabled = true
		skill.DisabledReason = ""
		return skill
	}
	if !strings.EqualFold(strings.TrimSpace(skill.Source), "bundled") {
		if !skill.Enabled {
			skill.Enabled = true
		}
		skill.DisabledReason = ""
		return skill
	}
	if probe, ok := bundledSkillProbes[strings.ToLower(strings.TrimSpace(skill.Name))]; ok && probe != nil {
		enabled, reason := probe(workDir)
		skill.Enabled = enabled
		skill.DisabledReason = strings.TrimSpace(reason)
		if enabled {
			skill.DisabledReason = ""
		}
		return skill
	}
	skill.Enabled = true
	skill.DisabledReason = ""
	return skill
}

func requireEnv(keys ...string) skillProbe {
	return func(_ string) (bool, string) {
		for _, key := range keys {
			if strings.TrimSpace(skillEnv(key)) != "" {
				return true, ""
			}
		}
		return false, "missing env: " + strings.Join(keys, " or ")
	}
}

func requireBinary(name string) skillProbe {
	return func(_ string) (bool, string) {
		if _, err := skillLookupPath(name); err == nil {
			return true, ""
		} else if errors.Is(err, exec.ErrNotFound) || err != nil {
			return false, "missing binary: " + strings.TrimSpace(name)
		}
		return false, "missing binary: " + strings.TrimSpace(name)
	}
}

func notYetAvailable(reason string) skillProbe {
	return func(_ string) (bool, string) {
		return false, strings.TrimSpace(reason)
	}
}

var bundledSkillProbes = map[string]skillProbe{}
