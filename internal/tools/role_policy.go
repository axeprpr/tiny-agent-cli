package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	AgentRoleGeneral   = "general"
	AgentRoleExplore   = "explore"
	AgentRolePlan      = "plan"
	AgentRoleImplement = "implement"
	AgentRoleVerify    = "verify"
)

func NormalizeAgentRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", AgentRoleGeneral:
		return AgentRoleGeneral
	case AgentRoleExplore:
		return AgentRoleExplore
	case AgentRolePlan:
		return AgentRolePlan
	case AgentRoleImplement:
		return AgentRoleImplement
	case AgentRoleVerify:
		return AgentRoleVerify
	default:
		return ""
	}
}

func EvaluateRolePermission(role string, inv ToolInvocation) PermissionDecision {
	role = NormalizeAgentRole(role)
	if role == "" || role == AgentRoleGeneral || role == AgentRoleImplement {
		return PermissionDecision{Allowed: true, Mode: "role:" + role}
	}

	name := strings.TrimSpace(inv.Name)
	switch name {
	case "write_file", "edit_file":
		return PermissionDecision{
			Allowed: false,
			Mode:    "role:" + role,
			Reason:  fmt.Sprintf("%s role is read-only and cannot modify workspace files with %s", role, name),
		}
	case "run_command":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(inv.Raw, &args); err != nil {
			return PermissionDecision{Allowed: false, Mode: "role:" + role, Reason: fmt.Sprintf("decode args: %v", err)}
		}
		command := strings.TrimSpace(args.Command)
		if command == "" {
			return PermissionDecision{Allowed: false, Mode: "role:" + role, Reason: "command is required"}
		}
		if !commandLooksReadOnly(command) {
			return PermissionDecision{
				Allowed: false,
				Mode:    "role:" + role,
				Reason:  fmt.Sprintf("%s role only allows read-only shell commands; denied: %s", role, command),
			}
		}
	}
	return PermissionDecision{Allowed: true, Mode: "role:" + role}
}

func commandLooksReadOnly(command string) bool {
	for _, part := range splitShellCommands(command) {
		if !shellPartLooksReadOnly(part) {
			return false
		}
	}
	return true
}

func shellPartLooksReadOnly(part string) bool {
	trimmed := strings.TrimSpace(part)
	if trimmed == "" {
		return true
	}
	if strings.Contains(trimmed, ">") {
		return false
	}
	normalized := normalizeShellCommand(trimmed)
	if normalized == "" {
		return true
	}
	lower := " " + normalized + " "
	for _, token := range []string{
		" rm ", " mv ", " cp ", " chmod ", " chown ", " mkdir ", " rmdir ", " touch ",
		" tee ", " install ", " sed -i", " perl -pi", " apply_patch ", " git apply ",
		" git am ", " git commit ", " git push ", " git add ", " git reset ", " git clean ",
		" git checkout ", " git switch ", " git restore ", " go generate ", " go install ",
		" cargo install ", " cargo add ", " npm install ", " npm ci ", " npm add ",
		" pnpm install ", " pnpm add ", " yarn add ", " yarn install ", " pip install ",
		" uv pip install ", " docker build ", " docker run ", " docker compose up ",
	} {
		if strings.Contains(lower, token) {
			return false
		}
	}

	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return true
	}
	head := fields[0]
	if head == "git" {
		return gitSubcommandLooksReadOnly(fields[1:])
	}
	switch head {
	case "ls", "pwd", "cat", "head", "tail", "grep", "rg", "find", "stat", "file", "wc", "sort", "uniq", "cut", "tr", "env", "printenv", "which", "whereis", "readlink", "dirname", "basename":
		return !strings.Contains(normalized, "tail -f")
	case "sed":
		return len(fields) > 1 && fields[1] == "-n"
	case "curl":
		return true
	case "go":
		return len(fields) > 1 && (fields[1] == "test" || fields[1] == "build" || fields[1] == "vet" || fields[1] == "list" || fields[1] == "env")
	case "cargo":
		return len(fields) > 1 && (fields[1] == "test" || fields[1] == "build" || fields[1] == "check" || fields[1] == "clippy")
	case "npm", "pnpm", "yarn":
		return packageManagerCommandLooksReadOnly(fields)
	case "pytest", "ruff", "mypy", "golangci-lint", "tsc":
		return true
	case "python", "python3", "uv":
		return pythonToolingCommandLooksReadOnly(fields)
	case "make":
		return len(fields) > 1 && (fields[1] == "test" || fields[1] == "check" || fields[1] == "lint" || fields[1] == "build")
	default:
		return false
	}
}

func gitSubcommandLooksReadOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "status", "diff", "show", "log", "rev-parse", "branch", "remote", "tag", "ls-files", "grep":
		return true
	default:
		return false
	}
}

func packageManagerCommandLooksReadOnly(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	switch fields[1] {
	case "test", "run":
		if len(fields) == 2 {
			return fields[1] == "test"
		}
		if fields[1] == "run" && len(fields) >= 3 {
			switch fields[2] {
			case "test", "build", "lint", "check", "typecheck":
				return true
			}
		}
		return false
	case "exec":
		return false
	default:
		return fields[1] == "build" || fields[1] == "lint"
	}
}

func pythonToolingCommandLooksReadOnly(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	joined := strings.Join(fields, " ")
	switch {
	case strings.Contains(joined, "-m pytest"),
		strings.Contains(joined, "-m unittest"),
		strings.Contains(joined, " -m mypy"),
		strings.Contains(joined, " -m ruff"):
		return true
	case fields[0] == "uv" && len(fields) >= 3 && fields[1] == "run":
		return pythonToolingCommandLooksReadOnly(fields[2:])
	default:
		return false
	}
}
