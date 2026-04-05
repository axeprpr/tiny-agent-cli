package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiny-agent-cli/internal/mcp"
	"tiny-agent-cli/internal/model"
)

type listMCPServersTool struct {
	stateDir string
}

func newListMCPServersTool(stateDir string) Tool {
	return &listMCPServersTool{stateDir: stateDir}
}

func (t *listMCPServersTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "list_mcp_servers",
			Description: "List configured MCP servers available to this workspace.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *listMCPServersTool) Call(_ context.Context, _ json.RawMessage) (string, error) {
	state, err := mcp.Load(mcp.Path(t.stateDir))
	if err != nil {
		return "", err
	}
	if len(state.Servers) == 0 {
		return "no MCP servers configured", nil
	}
	lines := make([]string, 0, len(state.Servers))
	for _, server := range state.Servers {
		line := fmt.Sprintf("%s transport=%s command=%s", server.Name, firstMCPValue(server.Transport, "stdio"), server.Command)
		if len(server.Args) > 0 {
			line += " args=" + strings.Join(server.Args, " ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func firstMCPValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
