package openaiapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
