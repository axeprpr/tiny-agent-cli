package openaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"onek-agent/internal/model"
)

type Client struct {
	baseURL    string
	defaultMod string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, defaultModel, apiKey string) *Client {
	return &Client{
		baseURL:    normalizeBaseURL(baseURL),
		defaultMod: defaultModel,
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Client) Complete(ctx context.Context, req model.Request) (model.Response, error) {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = c.defaultMod
	}
	if strings.TrimSpace(req.Model) == "" {
		return model.Response{}, fmt.Errorf("missing model")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return model.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return model.Response{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return model.Response{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return model.Response{}, fmt.Errorf("model API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var out model.Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return model.Response{}, fmt.Errorf("decode response: %w", err)
	}

	return out, nil
}

func normalizeBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}
