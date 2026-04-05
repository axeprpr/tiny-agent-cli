package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
)

const protocolVersion = "2025-03-26"

var nextRequestID uint64

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type ListResourcesResult struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
}

func ListResources(ctx context.Context, workDir string, server Server, cursor string) (ListResourcesResult, error) {
	client, err := startClient(ctx, workDir, server)
	if err != nil {
		return ListResourcesResult{}, err
	}
	defer client.Close()

	params := map[string]any{}
	if strings.TrimSpace(cursor) != "" {
		params["cursor"] = strings.TrimSpace(cursor)
	}
	var result ListResourcesResult
	if err := client.Call(ctx, "resources/list", params, &result); err != nil {
		return ListResourcesResult{}, err
	}
	return result, nil
}

func ReadResource(ctx context.Context, workDir string, server Server, uri string) (ReadResourceResult, error) {
	client, err := startClient(ctx, workDir, server)
	if err != nil {
		return ReadResourceResult{}, err
	}
	defer client.Close()

	var result ReadResourceResult
	if err := client.Call(ctx, "resources/read", map[string]any{"uri": strings.TrimSpace(uri)}, &result); err != nil {
		return ReadResourceResult{}, err
	}
	return result, nil
}

type client struct {
	server Server
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func startClient(ctx context.Context, workDir string, server Server) (*client, error) {
	server = normalizeServer(server)
	if server.Name == "" {
		return nil, fmt.Errorf("MCP server name is required")
	}
	if !strings.EqualFold(server.Transport, "stdio") {
		return nil, fmt.Errorf("MCP server %q uses unsupported transport %q", server.Name, server.Transport)
	}
	if strings.TrimSpace(server.Command) == "" {
		return nil, fmt.Errorf("MCP server %q has no command configured", server.Name)
	}

	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	cmd.Dir = workDir
	cmd.Stderr = &bytes.Buffer{}
	cmd.Env = os.Environ()
	for key, value := range server.Env {
		cmd.Env = append(cmd.Env, strings.TrimSpace(key)+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &client{
		server: server,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *client) initialize(ctx context.Context) error {
	var result InitializeResult
	if err := c.Call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "tiny-agent-cli",
			"version": "dev",
		},
	}, &result); err != nil {
		return err
	}
	return c.Notify("notifications/initialized", map[string]any{})
}

func (c *client) Notify(method string, params any) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return c.writeFrame(req)
}

func (c *client) Call(ctx context.Context, method string, params any, out any) error {
	id := atomic.AddUint64(&nextRequestID, 1)
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := c.writeFrame(req); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := c.readResponse()
		if err != nil {
			return err
		}
		if resp.ID == nil {
			continue
		}
		if !sameJSONRPCID(resp.ID, id) {
			continue
		}
		if resp.Error != nil {
			return fmt.Errorf("MCP %s on %s failed: %s (%d)", method, c.server.Name, resp.Error.Message, resp.Error.Code)
		}
		if out == nil || len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode MCP %s response: %w", method, err)
		}
		return nil
	}
}

func (c *client) Close() error {
	if c == nil || c.cmd == nil {
		return nil
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	return nil
}

func (c *client) writeFrame(message any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	_, err = c.stdin.Write(body)
	return err
}

func (c *client) readResponse() (jsonRPCResponse, error) {
	payload, err := readFrame(c.stdout)
	if err != nil {
		return jsonRPCResponse{}, err
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return jsonRPCResponse{}, err
	}
	return resp, nil
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			raw := strings.TrimSpace(line[len("content-length:"):])
			length, err := strconv.Atoi(raw)
			if err != nil {
				return nil, err
			}
			contentLength = length
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func sameJSONRPCID(raw any, want uint64) bool {
	switch v := raw.(type) {
	case float64:
		return uint64(v) == want
	case string:
		return strings.TrimSpace(v) == strconv.FormatUint(want, 10)
	default:
		return false
	}
}
