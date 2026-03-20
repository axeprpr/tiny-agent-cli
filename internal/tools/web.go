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

	"onek-agent/internal/model"
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
	req.Header.Set("User-Agent", "onek-agent/0.1")

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
	client *http.Client
}

func newWebSearchTool() Tool {
	return &webSearchTool{
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (t *webSearchTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "web_search",
			Description: "Run a lightweight web search and return compact results",
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

	endpoint := "https://api.duckduckgo.com/?format=json&no_html=1&skip_disambig=0&q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "onek-agent/0.1")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload struct {
		Heading       string `json:"Heading"`
		AbstractText  string `json:"AbstractText"`
		AbstractURL   string `json:"AbstractURL"`
		RelatedTopics []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
			Topics   []struct {
				Text     string `json:"Text"`
				FirstURL string `json:"FirstURL"`
			} `json:"Topics"`
		} `json:"RelatedTopics"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}

	var lines []string
	lines = append(lines, "query: "+query)

	if payload.Heading != "" || payload.AbstractText != "" {
		line := payload.Heading
		if payload.AbstractText != "" {
			line = strings.TrimSpace(line + " - " + payload.AbstractText)
		}
		if payload.AbstractURL != "" {
			line = strings.TrimSpace(line + " (" + payload.AbstractURL + ")")
		}
		lines = append(lines, line)
	}

	count := 0
	for _, item := range payload.RelatedTopics {
		if item.Text != "" {
			lines = append(lines, "- "+item.Text+" ("+item.FirstURL+")")
			count++
		}
		for _, nested := range item.Topics {
			if nested.Text != "" {
				lines = append(lines, "- "+nested.Text+" ("+nested.FirstURL+")")
				count++
			}
			if count >= 5 {
				break
			}
		}
		if count >= 5 {
			break
		}
	}

	if len(lines) == 1 {
		lines = append(lines, "(no search summary returned)")
	}
	return strings.Join(lines, "\n"), nil
}

var (
	tagPattern   = regexp.MustCompile(`(?s)<[^>]*>`)
	spacePattern = regexp.MustCompile(`\s+`)
)

func compactHTML(body string) string {
	body = tagPattern.ReplaceAllString(body, " ")
	body = spacePattern.ReplaceAllString(body, " ")
	body = strings.TrimSpace(body)
	if len(body) > 4000 {
		body = body[:4000] + " ...[truncated]"
	}
	return body
}
