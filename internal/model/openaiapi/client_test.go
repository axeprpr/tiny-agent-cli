package openaiapi

import (
	"context"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
