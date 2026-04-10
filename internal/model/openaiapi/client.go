package openaiapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
)

type Client struct {
	baseURL    string
	defaultMod string
	apiKey     string
	httpClient *http.Client
}

type ClientOption func(*Client)

func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient.Timeout = d
	}
}

type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func NewClient(baseURL, defaultModel, apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    normalizeBaseURL(baseURL),
		defaultMod: defaultModel,
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Complete(ctx context.Context, req model.Request) (model.Response, error) {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = c.defaultMod
	}
	if strings.TrimSpace(req.Model) == "" {
		return model.Response{}, fmt.Errorf("missing model")
	}
	req.Stream = false

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := retryBackoffForAttempt(lastErr, attempt)
			select {
			case <-ctx.Done():
				return model.Response{}, ctx.Err()
			case <-time.After(backoff):
			}
		}

		resp, err := c.doComplete(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryableError(err) {
			return model.Response{}, err
		}
	}
	return model.Response{}, lastErr
}

func (c *Client) doComplete(ctx context.Context, req model.Request) (model.Response, error) {
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

	if resp.StatusCode >= 500 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return model.Response{}, &serverError{status: resp.Status, body: string(data)}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return model.Response{}, &rateLimitError{
			status:     resp.Status,
			body:       string(data),
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
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

// CompleteStream sends a streaming request and returns channels for chunks and errors.
func (c *Client) CompleteStream(ctx context.Context, req model.Request) (<-chan model.StreamChunk, <-chan error) {
	chunks := make(chan model.StreamChunk, 64)
	errc := make(chan error, 1)

	if strings.TrimSpace(req.Model) == "" {
		req.Model = c.defaultMod
	}
	if strings.TrimSpace(req.Model) == "" {
		errc <- fmt.Errorf("missing model")
		close(chunks)
		close(errc)
		return chunks, errc
	}
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		errc <- fmt.Errorf("marshal request: %w", err)
		close(chunks)
		close(errc)
		return chunks, errc
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		errc <- fmt.Errorf("build request: %w", err)
		close(chunks)
		close(errc)
		return chunks, errc
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		errc <- fmt.Errorf("request failed: %w", err)
		close(chunks)
		close(errc)
		return chunks, errc
	}

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		errc <- fmt.Errorf("model API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
		close(chunks)
		close(errc)
		return chunks, errc
	}

	go func() {
		defer resp.Body.Close()
		defer close(chunks)
		defer close(errc)

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			data = strings.TrimSpace(data)
			if data == "[DONE]" {
				return
			}
			var chunk model.StreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			select {
			case chunks <- chunk:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errc <- err
		}
	}()

	return chunks, errc
}

type serverError struct {
	status string
	body   string
}

func (e *serverError) Error() string {
	return fmt.Sprintf("model API returned %s: %s", e.status, strings.TrimSpace(e.body))
}

type rateLimitError struct {
	status     string
	body       string
	retryAfter time.Duration
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("model API returned %s: %s", e.status, strings.TrimSpace(e.body))
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*serverError); ok {
		return true
	}
	if _, ok := err.(*rateLimitError); ok {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "connection reset") ||
		strings.Contains(text, "connection refused") ||
		strings.Contains(text, "eof") ||
		strings.Contains(text, "timeout")
}

func retryBackoffForAttempt(err error, attempt int) time.Duration {
	if rl, ok := err.(*rateLimitError); ok {
		if rl.retryAfter > 0 {
			return rl.retryAfter
		}
		return time.Duration(attempt) * 200 * time.Millisecond
	}
	return time.Duration(attempt) * 2 * time.Second
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		d := time.Until(at)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("model API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var out modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	names := make([]string, 0, len(out.Data))
	for _, item := range out.Data {
		if strings.TrimSpace(item.ID) != "" {
			names = append(names, item.ID)
		}
	}
	return names, nil
}

func PingRequest(modelName string) model.Request {
	return model.Request{
		Model: modelName,
		Messages: []model.Message{
			{
				Role:    "system",
				Content: "Reply with the exact final answer only. Do not include reasoning, analysis, or any prefix.",
			},
			{
				Role:    "user",
				Content: "Reply with exactly: pong",
			},
		},
		Temperature: 0,
	}
}

func ContentString(value any) string {
	return model.ContentString(value)
}

func normalizeBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}
