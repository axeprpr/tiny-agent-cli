package main

import (
	"context"
	"testing"

	"tiny-agent-cli/internal/model"
)

type fakeRoleRouterClient struct {
	resp model.Response
	err  error
}

func (f fakeRoleRouterClient) Complete(_ context.Context, _ model.Request) (model.Response, error) {
	return f.resp, f.err
}

func TestParseRoleClassifierOutputJSON(t *testing.T) {
	role, conf, ok := parseRoleClassifierOutput(`{"role":"verify","confidence":0.91}`)
	if !ok {
		t.Fatalf("expected parse success")
	}
	if role != backgroundRoleVerify {
		t.Fatalf("unexpected role: %q", role)
	}
	if conf < 0.9 {
		t.Fatalf("unexpected confidence: %v", conf)
	}
}

func TestParseRoleClassifierOutputWithWrapperText(t *testing.T) {
	role, _, ok := parseRoleClassifierOutput("answer: {\"role\":\"implement\",\"confidence\":0.7}")
	if !ok {
		t.Fatalf("expected parse success")
	}
	if role != backgroundRoleImplement {
		t.Fatalf("unexpected role: %q", role)
	}
}

func TestLLMBackgroundRoleRouterLowConfidenceFallback(t *testing.T) {
	router := llmBackgroundRoleRouterWithClient("demo", fakeRoleRouterClient{
		resp: model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: "assistant", Content: `{"role":"explore","confidence":0.2}`}},
			},
		},
	})
	if router == nil {
		t.Fatalf("expected router")
	}
	role, err := router(context.Background(), "inspect repository structure")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if role != backgroundRoleGeneral {
		t.Fatalf("unexpected role: %q", role)
	}
}

func TestLLMBackgroundRoleRouterNormalOutput(t *testing.T) {
	router := llmBackgroundRoleRouterWithClient("demo", fakeRoleRouterClient{
		resp: model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: "assistant", Content: `{"role":"plan","confidence":0.9}`}},
			},
		},
	})
	role, err := router(context.Background(), "give me a concrete implementation plan")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if role != backgroundRolePlan {
		t.Fatalf("unexpected role: %q", role)
	}
}
