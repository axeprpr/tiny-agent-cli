package mcp

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestReadFrameParsesContentLength(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("Content-Length: 17\r\n\r\n{\"ok\":true,\"n\":1}"))
	payload, err := readFrame(reader)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if string(payload) != "{\"ok\":true,\"n\":1}" {
		t.Fatalf("unexpected payload: %q", payload)
	}
}

func TestSameJSONRPCID(t *testing.T) {
	if !sameJSONRPCID(float64(3), 3) {
		t.Fatalf("expected numeric id to match")
	}
	if !sameJSONRPCID("3", 3) {
		t.Fatalf("expected string id to match")
	}
	if sameJSONRPCID("4", 3) {
		t.Fatalf("unexpected id match")
	}
}

func TestWriteFrameIncludesContentLengthHeader(t *testing.T) {
	var buf bytes.Buffer
	c := &client{stdin: nopWriteCloser{Writer: &buf}}
	if err := c.writeFrame(map[string]any{"jsonrpc": "2.0", "method": "ping"}); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	text := buf.String()
	if !strings.HasPrefix(text, "Content-Length: ") {
		t.Fatalf("missing content-length header: %q", text)
	}
	if !strings.Contains(text, "\r\n\r\n{\"jsonrpc\":\"2.0\",\"method\":\"ping\"}") {
		t.Fatalf("missing JSON payload: %q", text)
	}
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }
