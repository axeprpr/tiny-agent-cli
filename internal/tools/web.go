package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

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
					"max_bytes": map[string]any{
						"type":        "integer",
						"description": "Optional max response bytes to read, default 65536.",
					},
				},
			},
		},
	}
}

func (t *fetchURLTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		URL      string `json:"url"`
		MaxBytes int64  `json:"max_bytes"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}

	target := strings.TrimSpace(args.URL)
	if target == "" {
		return "", fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
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

	if args.MaxBytes <= 0 {
		args.MaxBytes = 64 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, args.MaxBytes))
	if err != nil {
		return "", err
	}

	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	text := compactHTML(string(body))
	if !strings.Contains(strings.ToLower(contentType), "html") && strings.TrimSpace(string(body)) != "" {
		text = strings.TrimSpace(string(body))
	}
	if text == "" {
		text = "(empty body)"
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fetch failed with %s: %s", resp.Status, compactWebText(text, 240))
	}
	return fmt.Sprintf("status: %s\nurl: %s\ncontent_type: %s\nfetched_at: %s\ncontent_bytes: %d\n\n%s",
		resp.Status,
		target,
		firstNonEmptyContentType(contentType),
		time.Now().UTC().Format(time.RFC3339),
		len(body),
		text,
	), nil
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
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum results to return, default 5.",
					},
				},
			},
		},
	}
}

func (t *webSearchTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}

	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}
	hints := buildSearchQueryHints(query)

	results := []ddgResult{}
	source := ""
	var lastErr error
	if hints.wantGitHub && len(hints.repoTerms) > 0 {
		results, lastErr = t.searchGitHubRepositories(ctx, hints, args.Limit)
		if len(results) > 0 {
			source = "github_api"
		}
	}

	endpoints := defaultSearchEndpoints(query)
	if t.searchEndpoints != nil {
		endpoints = t.searchEndpoints(query)
	}
	if len(results) == 0 {
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
					source = endpoint
					break
				}
			}
		}
	if len(results) == 0 && lastErr != nil {
		return "", lastErr
	}
	results = rerankSearchResults(query, results)
	results = dedupeSearchResults(results)

	var lines []string
	lines = append(lines, "query: "+query)
	if strings.TrimSpace(source) != "" {
		lines = append(lines, "source: "+source)
	}

	if len(results) == 0 {
		lines = append(lines, "(no search results returned)")
		return strings.Join(lines, "\n"), nil
	}

	if len(results) > args.Limit {
		results = results[:args.Limit]
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

func dedupeSearchResults(results []ddgResult) []ddgResult {
	if len(results) <= 1 {
		return results
	}
	seen := map[string]bool{}
	out := make([]ddgResult, 0, len(results))
	for _, item := range results {
		key := strings.ToLower(strings.TrimSpace(item.url))
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(item.title))
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func (t *webSearchTool) searchGitHubRepositories(ctx context.Context, hints searchQueryHints, limit int) ([]ddgResult, error) {
	repoTerm := hints.repoTerms[0]
	if repoTerm == "" {
		return nil, nil
	}
	perPage := limit
	if perPage <= 0 {
		perPage = 1
	}
	if perPage > 10 {
		perPage = 10
	}
	endpoint := "https://api.github.com/search/repositories?q=" + url.QueryEscape(repoTerm+" in:name") + "&per_page=" + fmt.Sprintf("%d", perPage)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tiny-agent-cli/0.1")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github search: %s", resp.Status)
	}

	var payload struct {
		Items []struct {
			FullName    string `json:"full_name"`
			HTMLURL     string `json:"html_url"`
			Description string `json:"description"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode github search: %w", err)
	}
	results := make([]ddgResult, 0, len(payload.Items))
	for _, item := range payload.Items {
		if strings.TrimSpace(item.HTMLURL) == "" {
			continue
		}
		title := strings.TrimSpace(item.FullName)
		if title == "" {
			title = item.HTMLURL
		}
		results = append(results, ddgResult{
			title:   "GitHub - " + title,
			url:     strings.TrimSpace(item.HTMLURL),
			snippet: strings.TrimSpace(item.Description),
		})
	}
	return results, nil
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
	endpoints := make([]string, 0, 8)
	for _, variant := range searchQueryVariants(query) {
		escaped := url.QueryEscape(variant)
		endpoints = append(endpoints,
			"https://html.duckduckgo.com/html/?q="+escaped,
			"https://lite.duckduckgo.com/lite/?q="+escaped,
		)
	}
	return endpoints
}

func rerankSearchResults(query string, results []ddgResult) []ddgResult {
	if len(results) < 2 {
		return results
	}
	type scoredResult struct {
		result ddgResult
		score  int
		index  int
	}
	hints := buildSearchQueryHints(query)
	scored := make([]scoredResult, 0, len(results))
	for i, item := range results {
		score := scoreSearchResult(hints, item)
		scored = append(scored, scoredResult{result: item, score: score, index: i})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].index < scored[j].index
		}
		return scored[i].score > scored[j].score
	})
	out := make([]ddgResult, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.result)
	}
	return out
}

type searchQueryHints struct {
	tokens         []string
	wantGitHub     bool
	repoTerms      []string
	ownerRepoTerms []string
}

func buildSearchQueryHints(query string) searchQueryHints {
	lower := strings.ToLower(strings.TrimSpace(query))
	hints := searchQueryHints{
		tokens:     searchTokens(lower),
		wantGitHub: strings.Contains(lower, "github"),
	}
	var b strings.Builder
	for _, r := range lower {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == '/':
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	seenRepo := map[string]bool{}
	seenOwnerRepo := map[string]bool{}
	for _, part := range strings.Fields(b.String()) {
		if len(part) < 3 {
			continue
		}
		if strings.Contains(part, "/") {
			if !seenOwnerRepo[part] {
				seenOwnerRepo[part] = true
				hints.ownerRepoTerms = append(hints.ownerRepoTerms, part)
			}
			if repo := repoNameFromCandidate(part); len(repo) >= 3 && !seenRepo[repo] {
				seenRepo[repo] = true
				hints.repoTerms = append(hints.repoTerms, repo)
			}
			continue
		}
		if strings.ContainsAny(part, "-_") && !seenRepo[part] {
			seenRepo[part] = true
			hints.repoTerms = append(hints.repoTerms, part)
		}
	}
	return hints
}

func searchQueryVariants(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	hints := buildSearchQueryHints(query)
	var variants []string
	if hints.wantGitHub {
		for _, ownerRepo := range hints.ownerRepoTerms {
			variants = append(variants, `site:github.com "`+ownerRepo+`"`)
		}
		for _, repoTerm := range hints.repoTerms {
			variants = append(variants,
				`site:github.com "`+repoTerm+`"`,
				`site:github.com `+repoTerm+` repository`,
			)
		}
	}
	variants = append(variants, query)
	seen := make(map[string]bool, len(variants))
	out := make([]string, 0, len(variants))
	for _, variant := range variants {
		variant = strings.TrimSpace(variant)
		if variant == "" || seen[variant] {
			continue
		}
		seen[variant] = true
		out = append(out, variant)
	}
	return out
}

func scoreSearchResult(hints searchQueryHints, item ddgResult) int {
	combined := strings.ToLower(item.title + " " + item.url + " " + item.snippet)
	normalizedCombined := normalizeSearchComparable(combined)
	score := 0
	if strings.Contains(combined, "github.com") {
		score += 15
		if hints.wantGitHub {
			score += 25
		}
	}
	repoOwner, repoName := githubRepoSegments(item.url)
	for _, ownerRepo := range hints.ownerRepoTerms {
		if strings.Contains(combined, "github.com/"+ownerRepo) {
			score += 60
		}
	}
	for _, repoTerm := range hints.repoTerms {
		if strings.Contains(combined, repoTerm) {
			score += 18
		}
		if normalized := normalizeSearchComparable(repoTerm); normalized != "" && strings.Contains(normalizedCombined, normalized) {
			score += 10
		}
		if repoName == repoTerm {
			score += 50
		}
		if repoOwner != "" && strings.Contains(combined, "github.com/"+repoOwner+"/"+repoTerm) {
			score += 25
		}
	}
	if strings.Contains(combined, "readme") {
		score += 10
	}
	if strings.Contains(combined, "/blob/main/") || strings.Contains(combined, "/tree/main/") {
		score += 6
	}
	matchedTokens := 0
	for _, token := range hints.tokens {
		if strings.Contains(combined, token) {
			score += 4
			matchedTokens++
		}
	}
	if matchedTokens >= 2 {
		score += 8
	}
	if matchedTokens >= 3 {
		score += 8
	}
	return score
}

func repoNameFromCandidate(value string) string {
	value = strings.Trim(strings.ToLower(value), "/")
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}

func githubRepoSegments(rawURL string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", ""
	}
	if !strings.EqualFold(parsed.Host, "github.com") {
		return "", ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return "", ""
	}
	return strings.ToLower(parts[0]), strings.ToLower(parts[1])
}

func normalizeSearchComparable(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func searchTokens(query string) []string {
	query = strings.ToLower(query)
	var b strings.Builder
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	parts := strings.Fields(b.String())
	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 3 || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func stripTags(s string) string {
	s = tagPattern.ReplaceAllString(s, " ")
	s = decodeHTMLEntities(s)
	s = spacePattern.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func firstNonEmptyContentType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
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

func compactWebText(text string, limit int) string {
	text = strings.TrimSpace(spacePattern.ReplaceAllString(text, " "))
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
