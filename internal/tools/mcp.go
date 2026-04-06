package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"tiny-agent-cli/internal/mcp"
	"tiny-agent-cli/internal/model"
)

type listMCPServersTool struct {
	stateDir string
}

type mcpResourceLister func(context.Context, string, mcp.Server, string) (mcp.ListResourcesResult, error)
type mcpResourceReader func(context.Context, string, mcp.Server, string) (mcp.ReadResourceResult, error)

type listMCPResourcesTool struct {
	workDir  string
	stateDir string
	list     mcpResourceLister
}

type readMCPResourceTool struct {
	workDir  string
	stateDir string
	read     mcpResourceReader
}

func newListMCPServersTool(stateDir string) Tool {
	return &listMCPServersTool{stateDir: stateDir}
}

func newListMCPResourcesTool(workDir, stateDir string) Tool {
	return &listMCPResourcesTool{
		workDir:  workDir,
		stateDir: stateDir,
		list:     mcp.ListResources,
	}
}

func newReadMCPResourceTool(workDir, stateDir string) Tool {
	return &readMCPResourceTool{
		workDir:  workDir,
		stateDir: stateDir,
		read:     mcp.ReadResource,
	}
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

func (t *listMCPResourcesTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "list_mcp_resources",
			Description: "List resources exposed by a configured stdio MCP server.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server": map[string]any{
						"type":        "string",
						"description": "Configured MCP server name.",
					},
					"cursor": map[string]any{
						"type":        "string",
						"description": "Optional pagination cursor returned by a previous list_mcp_resources call.",
					},
				},
				"required": []string{"server"},
			},
		},
	}
}

func (t *readMCPResourceTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "read_mcp_resource",
			Description: "Read a resource exposed by a configured stdio MCP server.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server": map[string]any{
						"type":        "string",
						"description": "Configured MCP server name.",
					},
					"uri": map[string]any{
						"type":        "string",
						"description": "Resource URI returned by list_mcp_resources.",
					},
				},
				"required": []string{"server", "uri"},
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

func (t *listMCPResourcesTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var req struct {
		Server string `json:"server"`
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", err
	}
	server, err := loadMCPServer(t.stateDir, req.Server)
	if err != nil {
		return "", err
	}
	result, err := t.list(ctx, t.workDir, server, req.Cursor)
	if err != nil {
		return "", err
	}
	if len(result.Resources) == 0 {
		return fmt.Sprintf("server=%s resources=0", server.Name), nil
	}
	lines := []string{fmt.Sprintf("server=%s resources=%d", server.Name, len(result.Resources))}
	for _, resource := range result.Resources {
		line := resource.URI
		if strings.TrimSpace(resource.Name) != "" {
			line += " name=" + resource.Name
		}
		if strings.TrimSpace(resource.MIMEType) != "" {
			line += " mime=" + resource.MIMEType
		}
		if strings.TrimSpace(resource.Description) != "" {
			line += " desc=" + resource.Description
		}
		lines = append(lines, line)
	}
	if strings.TrimSpace(result.NextCursor) != "" {
		lines = append(lines, "next_cursor="+result.NextCursor)
	}
	return strings.Join(lines, "\n"), nil
}

func (t *readMCPResourceTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var req struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", err
	}
	server, err := loadMCPServer(t.stateDir, req.Server)
	if err != nil {
		return "", err
	}
	result, err := t.read(ctx, t.workDir, server, req.URI)
	if err != nil {
		return "", err
	}
	if len(result.Contents) == 0 {
		return fmt.Sprintf("server=%s uri=%s contents=0", server.Name, strings.TrimSpace(req.URI)), nil
	}
	lines := make([]string, 0, len(result.Contents)+1)
	lines = append(lines, fmt.Sprintf("server=%s uri=%s contents=%d", server.Name, strings.TrimSpace(req.URI), len(result.Contents)))
	for _, item := range result.Contents {
		lines = append(lines, formatMCPResourceContents(item))
	}
	return strings.Join(lines, "\n"), nil
}

func loadMCPServer(stateDir, name string) (mcp.Server, error) {
	state, err := mcp.Load(mcp.Path(stateDir))
	if err != nil {
		return mcp.Server{}, err
	}
	needle := strings.TrimSpace(name)
	for _, server := range state.Servers {
		if strings.EqualFold(server.Name, needle) {
			return server, nil
		}
	}
	names := make([]string, 0, len(state.Servers))
	for _, server := range state.Servers {
		names = append(names, server.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return mcp.Server{}, fmt.Errorf("no MCP servers configured")
	}
	return mcp.Server{}, fmt.Errorf("unknown MCP server %q (configured: %s)", needle, strings.Join(names, ", "))
}

func formatMCPResourceContents(item mcp.ResourceContents) string {
	if strings.TrimSpace(item.Text) != "" {
		return compactPreviewString(item.Text, 800)
	}
	if strings.TrimSpace(item.Blob) != "" {
		return fmt.Sprintf("[base64 blob] mime=%s bytes=%d", firstMCPValue(item.MIMEType, "application/octet-stream"), len(item.Blob))
	}
	return fmt.Sprintf("[empty content] uri=%s", item.URI)
}

func firstMCPValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
