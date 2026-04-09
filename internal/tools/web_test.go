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

func TestWebSearchPrefersGitHubRepositorySearchForRepoQueries(t *testing.T) {
	tool := &webSearchTool{
		client: &http.Client{
			Timeout: 2 * time.Second,
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body := `{"items":[{"full_name":"axeprpr/tiny-agent-cli","html_url":"https://github.com/axeprpr/tiny-agent-cli","description":"official repository"}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		},
	}

	out, err := tool.Call(context.Background(), json.RawMessage(`{"query":"tiny-agent-cli github repository"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "https://github.com/axeprpr/tiny-agent-cli") {
		t.Fatalf("expected GitHub repository result in output, got: %q", out)
	}
}

func TestRerankSearchResultsPrefersGitHubForGitHubQuery(t *testing.T) {
	results := []ddgResult{
		{title: "Blog Post", url: "https://example.com/tiny-agent-cli", snippet: "third-party summary"},
		{title: "GitHub - tinyagents/cli: Load connectors, invoke LLM with function calling", url: "https://github.com/tinyagents/cli", snippet: "similar but wrong repository"},
		{title: "GitHub - axeprpr/tiny-agent-cli: Minimal Go single-task agent CLI", url: "https://github.com/axeprpr/tiny-agent-cli", snippet: "official repository"},
	}

	ranked := rerankSearchResults("tiny-agent-cli github repository", results)
	if ranked[0].url != "https://github.com/axeprpr/tiny-agent-cli" {
		t.Fatalf("expected GitHub result first, got %#v", ranked)
	}
}

func TestSearchQueryVariantsPreferExactGitHubRepoSearch(t *testing.T) {
	variants := searchQueryVariants("tiny-agent-cli github repository")
	if len(variants) < 2 {
		t.Fatalf("expected multiple query variants, got %#v", variants)
	}
	if variants[0] != `site:github.com "tiny-agent-cli"` {
		t.Fatalf("expected exact GitHub repo query first, got %#v", variants)
	}
	if variants[len(variants)-1] != "tiny-agent-cli github repository" {
		t.Fatalf("expected original query to remain as fallback, got %#v", variants)
	}
}

func TestFetchURLRejectsUnsupportedScheme(t *testing.T) {
	tool := newFetchURLTool()
	_, err := tool.Call(context.Background(), json.RawMessage(`{"url":"file:///etc/passwd"}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported URL scheme") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFetchURLReturnsErrorForHTTPFailure(t *testing.T) {
	tool := &fetchURLTool{
		client: &http.Client{
			Timeout: 2 * time.Second,
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Status:     "404 Not Found",
					Body:       io.NopCloser(strings.NewReader("missing")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		},
	}

	_, err := tool.Call(context.Background(), json.RawMessage(`{"url":"https://example.com/missing"}`))
	if err == nil || !strings.Contains(err.Error(), "404 Not Found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
