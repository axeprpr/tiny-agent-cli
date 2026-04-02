package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/model/openaiapi"
)

type backgroundRoleRouter func(ctx context.Context, task string) (string, error)

type roleRouterClient interface {
	Complete(ctx context.Context, req model.Request) (model.Response, error)
}

func keywordBackgroundRoleRouter() backgroundRoleRouter {
	return func(_ context.Context, task string) (string, error) {
		return routeBackgroundRole(task), nil
	}
}

func llmBackgroundRoleRouter(cfg config.Config) backgroundRoleRouter {
	client := openaiapi.NewClient(cfg.BaseURL, cfg.Model, cfg.APIKey, openaiapi.WithTimeout(roleRoutingTimeout(cfg.ModelTimeout)))
	return llmBackgroundRoleRouterWithClient(cfg.Model, client)
}

func llmBackgroundRoleRouterWithClient(modelName string, client roleRouterClient) backgroundRoleRouter {
	if strings.TrimSpace(modelName) == "" || client == nil {
		return nil
	}
	return func(ctx context.Context, task string) (string, error) {
		task = strings.TrimSpace(task)
		if task == "" {
			return backgroundRoleGeneral, nil
		}
		req := model.Request{
			Model: modelName,
			Messages: []model.Message{
				{Role: "system", Content: bgRoleRouterSystemPrompt},
				{Role: "user", Content: task},
			},
			Temperature: 0,
		}
		resp, err := client.Complete(ctx, req)
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("role router returned no choices")
		}
		raw := model.ContentString(resp.Choices[0].Message.Content)
		role, conf, ok := parseRoleClassifierOutput(raw)
		if !ok {
			return "", fmt.Errorf("role router output parse failed")
		}
		if validateBackgroundRole(role) != nil {
			return "", fmt.Errorf("role router output invalid")
		}
		if conf > 0 && conf < 0.45 {
			return backgroundRoleGeneral, nil
		}
		return role, nil
	}
}

func roleRoutingTimeout(modelTimeout time.Duration) time.Duration {
	timeout := 8 * time.Second
	if modelTimeout > 0 && modelTimeout < timeout {
		timeout = modelTimeout
	}
	if timeout < 3*time.Second {
		timeout = 3 * time.Second
	}
	return timeout
}

type roleClassifierOutput struct {
	Role       string  `json:"role"`
	Confidence float64 `json:"confidence"`
}

func parseRoleClassifierOutput(raw string) (string, float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, false
	}

	var out roleClassifierOutput
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		role := normalizeBackgroundRole(out.Role)
		return role, out.Confidence, role != ""
	}

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(raw[start:end+1]), &out); err == nil {
			role := normalizeBackgroundRole(out.Role)
			return role, out.Confidence, role != ""
		}
	}

	lower := strings.ToLower(raw)
	for _, role := range []string{
		backgroundRoleVerify,
		backgroundRolePlan,
		backgroundRoleImplement,
		backgroundRoleExplore,
		backgroundRoleGeneral,
	} {
		if strings.Contains(lower, role) {
			return role, 0.5, true
		}
	}
	return "", 0, false
}

const bgRoleRouterSystemPrompt = `You classify one background task into exactly one role.

Allowed roles:
- general
- explore
- plan
- implement
- verify

Decision intent:
- explore: read-only inspection, architecture mapping, investigation, risk discovery
- plan: produce execution plan, decomposition, strategy, sequencing
- implement: code changes, patching, refactor, adding feature
- verify: run tests/build/checks, validate behavior, collect evidence/verdict
- general: mixed/ambiguous requests or unclear intent

Output requirements:
- Return JSON only, no markdown, no extra text.
- JSON shape: {"role":"general|explore|plan|implement|verify","confidence":0..1}
- confidence should reflect certainty.`
