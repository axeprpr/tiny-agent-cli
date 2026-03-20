package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"onek-agent/internal/model"
	"onek-agent/internal/tools"
)

const systemPrompt = `You are a small terminal agent.

Constraints:
- You are running one task only.
- Prefer using tools instead of guessing.
- Keep the final answer concise and actionable.
- Do not access files outside the workspace.
- Avoid dangerous shell commands.

Behavior:
- Inspect the workspace before editing when the task depends on local files.
- Use web_search for broad discovery and fetch_url for direct page inspection.
- When writing files, explain what you changed in the final answer.
- Stop as soon as the task is complete.`

type chatClient interface {
	Complete(ctx context.Context, req model.Request) (model.Response, error)
}

type Result struct {
	Final string
	Steps int
}

type Agent struct {
	client   chatClient
	registry *tools.Registry
	maxSteps int
	log      io.Writer
}

func New(client chatClient, registry *tools.Registry, maxSteps int, log io.Writer) *Agent {
	return &Agent{
		client:   client,
		registry: registry,
		maxSteps: maxSteps,
		log:      log,
	}
}

func (a *Agent) Run(ctx context.Context, task string) (Result, error) {
	messages := []model.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task},
	}

	for step := 1; step <= a.maxSteps; step++ {
		a.logf("step %d/%d\n", step, a.maxSteps)

		resp, err := a.client.Complete(ctx, model.Request{
			Messages:   messages,
			Tools:      a.registry.Definitions(),
			ToolChoice: "auto",
		})
		if err != nil {
			return Result{}, err
		}
		if len(resp.Choices) == 0 {
			return Result{}, fmt.Errorf("model returned no choices")
		}

		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) == 0 {
			final := strings.TrimSpace(model.ContentString(msg.Content))
			if final == "" {
				final = "(empty response)"
			}
			return Result{Final: final, Steps: step}, nil
		}

		messages = append(messages, model.Message{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: msg.ToolCalls,
		})

		for _, call := range msg.ToolCalls {
			a.logf("tool %s\n", call.Function.Name)

			output, err := a.registry.Call(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			if err != nil {
				output = "tool error: " + err.Error()
			}

			messages = append(messages, model.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    output,
			})
		}
	}

	return Result{}, fmt.Errorf("max steps reached without final response")
}

func (a *Agent) logf(format string, args ...any) {
	if a.log == nil {
		return
	}
	fmt.Fprintf(a.log, format, args...)
}
