package openaiapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"tiny-agent-cli/internal/model"
)

func TestCompleteStreamUsesConfiguredHTTPClient(t *testing.T) {
	used := false
	client := NewClient("https://api.example.test", "test-model", "")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			used = true
			body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n" +
				"data: [DONE]\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	chunks, errc := client.CompleteStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: "user", Content: "hi"}},
	})

	var got []model.StreamChunk
	for chunk := range chunks {
		got = append(got, chunk)
	}
	for err := range errc {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}
	if !used {
		t.Fatalf("expected configured http client to be used")
	}
	if len(got) != 1 {
		t.Fatalf("unexpected chunks: %#v", got)
	}
}

func TestCompleteRetriesOnRateLimitWithRetryAfter(t *testing.T) {
	attempts := 0
	client := NewClient("https://api.example.test", "test-model", "")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Status:     "429 Too Many Requests",
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limit"}`)),
					Header:     http.Header{"Retry-After": []string{"0"}},
					Request:    req,
				}, nil
			}
			body := `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	resp, err := client.Complete(context.Background(), model.Request{
		Messages: []model.Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected one retry after rate limit, got %d attempts", attempts)
	}
	if len(resp.Choices) != 1 || model.ContentString(resp.Choices[0].Message.Content) != "ok" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestParseRetryAfterSupportsSecondsAndHTTPDate(t *testing.T) {
	if got := parseRetryAfter("3"); got != 3*time.Second {
		t.Fatalf("unexpected seconds retry-after: %s", got)
	}
	future := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 {
		t.Fatalf("expected positive duration for retry-after date, got %s", got)
	}
	if got := parseRetryAfter("not-a-date"); got != 0 {
		t.Fatalf("expected zero duration for invalid retry-after, got %s", got)
	}
}

func TestCompleteReturnsRateLimitErrorWhenRetriesExhausted(t *testing.T) {
	client := NewClient("https://api.example.test", "test-model", "")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limit"}`)),
				Header:     http.Header{"Retry-After": []string{"0"}},
				Request:    req,
			}, nil
		}),
	}

	_, err := client.Complete(context.Background(), model.Request{
		Messages: []model.Message{{Role: "user", Content: "ping"}},
	})
	if err == nil {
		t.Fatalf("expected rate limit error")
	}
	if !strings.Contains(err.Error(), "429 Too Many Requests") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModelsInfoParsesContextWindowFromModelsResponse(t *testing.T) {
	client := NewClient("https://api.example.test", "test-model", "")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := `{"data":[{"id":"gpt-5-mini","max_model_len":400000}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	info, ok, err := client.ModelInfo(context.Background(), "gpt-5-mini")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("expected model info to be found")
	}
	if info.ContextWindow != 400000 {
		t.Fatalf("unexpected context window: %#v", info)
	}
}

func TestModelsInfoFallsBackToAPIMetadataEndpoint(t *testing.T) {
	client := NewClient("https://api.example.test", "test-model", "")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/v1/models":
				body := `{"data":[{"id":"gpt-5-mini"}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			case "/api/models":
				body := `{"data":[{"id":"gpt-5-mini","top_provider":{"context_length":400000}}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Status:     "404 Not Found",
					Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}
		}),
	}

	info, ok, err := client.ModelInfo(context.Background(), "gpt-5-mini")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("expected model info to be found")
	}
	if info.ContextWindow != 400000 {
		t.Fatalf("unexpected context window after fallback: %#v", info)
	}
}

func TestModelsInfoParsesVisionCapability(t *testing.T) {
	client := NewClient("https://api.example.test", "test-model", "")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := `{"data":[{"id":"gpt-5","capabilities":{"vision":true}}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	info, ok, err := client.ModelInfo(context.Background(), "gpt-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || !info.SupportsVision {
		t.Fatalf("expected model to support vision, got %#v", info)
	}
}

func TestSupportsVisionByName(t *testing.T) {
	for _, modelName := range []string{"gpt-5", "gpt-4o-mini", "qwen2.5-vl", "llava:latest"} {
		if !SupportsVisionByName(modelName) {
			t.Fatalf("expected %q to be treated as vision-capable", modelName)
		}
	}
	if SupportsVisionByName("qwen2.5-coder:7b") {
		t.Fatalf("expected plain coder model to be non-vision by default")
	}
}

func TestCompleteMarshalsMultimodalContent(t *testing.T) {
	var body map[string]any
	client := NewClient("https://api.example.test", "test-model", "")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			reply := `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(reply)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	_, err := client.Complete(context.Background(), model.Request{
		Messages: []model.Message{{
			Role: "user",
			Content: []model.ContentPart{
				{Type: "text", Text: "what is in this image?"},
				{Type: "image_url", ImageURL: &model.ImageURL{URL: "data:image/png;base64,aaa"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("unexpected request body: %#v", body)
	}
	first, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected first message: %#v", messages[0])
	}
	content, ok := first["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("unexpected content payload: %#v", first["content"])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
