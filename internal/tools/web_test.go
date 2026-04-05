package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseDDGResultsFallbackAnchors(t *testing.T) {
	html := `
	<html><body>
	<a href="/l/?uddg=https%3A%2F%2Fexample.com%2Falpha">Example Alpha</a>
	<a href="https://example.org/beta">Example Beta</a>
	</body></html>`

	results := parseDDGResults(html)
	if len(results) < 2 {
		t.Fatalf("expected fallback results, got %#v", results)
	}
	if results[0].url != "https://example.com/alpha" {
		t.Fatalf("unexpected first url: %q", results[0].url)
	}
	if results[1].url != "https://example.org/beta" {
		t.Fatalf("unexpected second url: %q", results[1].url)
	}
}

func TestWebSearchUsesSecondaryEndpointFallback(t *testing.T) {
	tool := &webSearchTool{
		client: &http.Client{
			Timeout: 2 * time.Second,
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body := `<html><body><div>no result blocks here</div></body></html>`
				if req.URL.Path == "/lite" {
					body = `<html><body><a href="https://example.com/fallback">Fallback Result</a></body></html>`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		},
		searchEndpoints: func(_ string) []string {
			return []string{"https://search.test/html", "https://search.test/lite"}
		},
	}

	out, err := tool.Call(context.Background(), json.RawMessage(`{"query":"tiny agent"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Fallback Result") {
		t.Fatalf("expected fallback result in output, got: %q", out)
	}
	if !strings.Contains(out, "https://example.com/fallback") {
		t.Fatalf("expected fallback url in output, got: %q", out)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
