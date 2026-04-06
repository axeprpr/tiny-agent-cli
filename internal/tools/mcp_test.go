package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"tiny-agent-cli/internal/mcp"
)

func TestListMCPResourcesToolUsesConfiguredServer(t *testing.T) {
	dir := t.TempDir()
	state := mcp.State{
		Servers: []mcp.Server{{
			Name:      "demo",
			Command:   "demo-server",
			Transport: "stdio",
		}},
	}
	if err := mcp.Save(mcp.Path(filepath.Join(dir, ".tacli")), state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	tool := &listMCPResourcesTool{
		workDir:  dir,
		stateDir: filepath.Join(dir, ".tacli"),
		list: func(_ context.Context, workDir string, server mcp.Server, cursor string) (mcp.ListResourcesResult, error) {
			if workDir != dir {
				t.Fatalf("unexpected workdir: %q", workDir)
			}
			if server.Name != "demo" {
				t.Fatalf("unexpected server: %#v", server)
			}
			if cursor != "next" {
				t.Fatalf("unexpected cursor: %q", cursor)
			}
			return mcp.ListResourcesResult{
				Resources: []mcp.Resource{{
					URI:      "file:///tmp/readme",
					Name:     "README",
					MIMEType: "text/plain",
				}},
			}, nil
		},
	}

	out, err := tool.Call(context.Background(), json.RawMessage(`{"server":"demo","cursor":"next"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(out, "server=demo resources=1") {
		t.Fatalf("unexpected output: %q", out)
	}
	if !strings.Contains(out, "file:///tmp/readme") {
		t.Fatalf("missing resource uri: %q", out)
	}
}

func TestReadMCPResourceToolFormatsTextContent(t *testing.T) {
	dir := t.TempDir()
	state := mcp.State{
		Servers: []mcp.Server{{
			Name:      "demo",
			Command:   "demo-server",
			Transport: "stdio",
		}},
	}
	if err := mcp.Save(mcp.Path(filepath.Join(dir, ".tacli")), state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	tool := &readMCPResourceTool{
		workDir:  dir,
		stateDir: filepath.Join(dir, ".tacli"),
		read: func(_ context.Context, workDir string, server mcp.Server, uri string) (mcp.ReadResourceResult, error) {
			if workDir != dir {
				t.Fatalf("unexpected workdir: %q", workDir)
			}
			if server.Name != "demo" {
				t.Fatalf("unexpected server: %#v", server)
			}
			if uri != "memo://rules" {
				t.Fatalf("unexpected uri: %q", uri)
			}
			return mcp.ReadResourceResult{
				Contents: []mcp.ResourceContents{{
					URI:  uri,
					Text: "hello from mcp",
				}},
			}, nil
		},
	}

	out, err := tool.Call(context.Background(), json.RawMessage(`{"server":"demo","uri":"memo://rules"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(out, "contents=1") {
		t.Fatalf("unexpected output: %q", out)
	}
	if !strings.Contains(out, "hello from mcp") {
		t.Fatalf("missing content text: %q", out)
	}
}
