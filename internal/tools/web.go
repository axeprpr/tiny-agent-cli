package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
)

type fetchURLTool struct {
	client *http.Client
}

func newFetchURLTool() Tool {
	return &fetchURLTool{
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (t *fetchURLTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "fetch_url",
			Description: "Fetch a URL and return a compact text extract",
			Parameters: map[string]any{
				"type": "object",
				"required": []string{
					"url",
				},
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "HTTP or HTTPS URL",
					},
				},
			},
		},
	}
}

func (t *fetchURLTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}

	target := strings.TrimSpace(args.URL)
	if target == "" {
		return "", fmt.Errorf("url is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tiny-agent-cli/0.1")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}

	text := compactHTML(string(body))
	if text == "" {
		text = "(empty body)"
	}
	return fmt.Sprintf("status: %s\nurl: %s\n\n%s", resp.Status, target, text), nil
}

type webSearchTool struct {
	client          *http.Client
	searchEndpoints func(query string) []string
}

func newWebSearchTool() Tool {
	return &webSearchTool{
		client:          &http.Client{Timeout: 20 * time.Second},
		searchEndpoints: defaultSearchEndpoints,
	}
}

func (t *webSearchTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "web_search",
			Description: "Run a web search and return compact results",
			Parameters: map[string]any{
				"type": "object",
				"required": []string{
					"query",
				},
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query",
					},
				},
			},
		},
	}
}

func (t *webSearchTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}

	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	endpoints := defaultSearchEndpoints(query)
	if t.searchEndpoints != nil {
		endpoints = t.searchEndpoints(query)
	}
	results := []ddgResult{}
	var lastErr error
	for _, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; tiny-agent-cli/0.1)")

		resp, err := t.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		results = parseDDGResults(string(body))
		if len(results) > 0 {
			break
		}
	}
	if len(results) == 0 && lastErr != nil {
		return "", lastErr
	}

	var lines []string
	lines = append(lines, "query: "+query)

	if len(results) == 0 {
		lines = append(lines, "(no search results returned)")
		return strings.Join(lines, "\n"), nil
	}

	for i, r := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, r.title))
		if r.url != "" {
			lines = append(lines, "   "+r.url)
		}
		if r.snippet != "" {
			lines = append(lines, "   "+r.snippet)
		}
	}
	return strings.Join(lines, "\n"), nil
}

type ddgResult struct {
	title   string
	url     string
	snippet string
}

var (
	ddgResultBlockPattern = regexp.MustCompile(`(?si)<a[^>]+class="[^"]*result__a[^"]*"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	ddgSnippetPattern     = regexp.MustCompile(`(?si)<a[^>]+class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	ddgURLPattern         = regexp.MustCompile(`(?i)uddg=([^&]+)`)
	scriptStylePattern    = regexp.MustCompile(`(?si)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
	tagPattern            = regexp.MustCompile(`(?s)<[^>]*>`)
	spacePattern          = regexp.MustCompile(`\s+`)
)

func parseDDGResults(html string) []ddgResult {
	results := parseDDGPrimaryResults(html)
	if len(results) > 0 {
		return results
	}
	return parseDDGFallbackResults(html)
}

func parseDDGPrimaryResults(html string) []ddgResult {
	blocks := ddgResultBlockPattern.FindAllStringSubmatch(html, 24)
	snippets := ddgSnippetPattern.FindAllStringSubmatch(html, 24)

	var results []ddgResult
	for i, block := range blocks {
		if len(results) >= 8 {
			break
		}
		rawURL := block[1]
		title := stripTags(block[2])
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}

		resultURL := rawURL
		if m := ddgURLPattern.FindStringSubmatch(rawURL); len(m) > 1 {
			if decoded, err := url.QueryUnescape(m[1]); err == nil {
				resultURL = decoded
			}
		}

		snippet := ""
		if i < len(snippets) {
			snippet = stripTags(snippets[i][1])
			snippet = strings.TrimSpace(snippet)
		}

		results = append(results, ddgResult{
			title:   title,
			url:     resultURL,
			snippet: snippet,
		})
	}
	return results
}

var ddgFallbackAnchorPattern = regexp.MustCompile(`(?si)<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)

func parseDDGFallbackResults(html string) []ddgResult {
	anchors := ddgFallbackAnchorPattern.FindAllStringSubmatch(html, 64)
	if len(anchors) == 0 {
		return nil
	}

	seen := map[string]bool{}
	var out []ddgResult
	for _, m := range anchors {
		if len(out) >= 8 {
			break
		}
		rawURL := strings.TrimSpace(m[1])
		if rawURL == "" || strings.HasPrefix(rawURL, "#") || strings.HasPrefix(strings.ToLower(rawURL), "javascript:") {
			continue
		}
		targetURL := normalizeDDGURL(rawURL)
		if targetURL == "" || seen[targetURL] {
			continue
		}
		title := strings.TrimSpace(stripTags(m[2]))
		if title == "" || len(title) < 3 {
			continue
		}
		seen[targetURL] = true
		out = append(out, ddgResult{
			title: title,
			url:   targetURL,
		})
	}
	return out
}

func normalizeDDGURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "/l/?") || strings.Contains(raw, "uddg=") {
		if m := ddgURLPattern.FindStringSubmatch(raw); len(m) > 1 {
			if decoded, err := url.QueryUnescape(m[1]); err == nil {
				raw = decoded
			}
		}
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return ""
	}
	return raw
}

func defaultSearchEndpoints(query string) []string {
	escaped := url.QueryEscape(query)
	return []string{
		"https://html.duckduckgo.com/html/?q=" + escaped,
		"https://lite.duckduckgo.com/lite/?q=" + escaped,
	}
}

func stripTags(s string) string {
	s = tagPattern.ReplaceAllString(s, " ")
	s = decodeHTMLEntities(s)
	s = spacePattern.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func decodeHTMLEntities(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&apos;", "'",
		"&nbsp;", " ", "&#x27;", "'", "&#x2F;", "/",
	)
	return r.Replace(s)
}

func compactHTML(body string) string {
	body = scriptStylePattern.ReplaceAllString(body, " ")
	body = tagPattern.ReplaceAllString(body, " ")
	body = decodeHTMLEntities(body)
	body = spacePattern.ReplaceAllString(body, " ")
	body = strings.TrimSpace(body)
	if len(body) > 4000 {
		body = body[:4000] + " ...[truncated]"
	}
	return body
}
