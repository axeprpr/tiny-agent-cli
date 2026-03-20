package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"onek-agent/internal/model"
	"onek-agent/internal/tools"
)

const systemPrompt = `You are a small terminal agent.

Constraints:
- You are running one task only.
- Prefer using tools instead of guessing.
- Keep the final answer concise and actionable.
- Do not reveal chain-of-thought, hidden reasoning, or thinking process.
- Do not access files outside the workspace.
- Avoid dangerous shell commands.

Behavior:
- Inspect the workspace before editing when the task depends on local files.
- Use web_search for broad discovery and fetch_url for direct page inspection.
- When writing files, explain what you changed in the final answer.
- Return only the final answer to the user, not your reasoning.
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
			final := SanitizeFinal(model.ContentString(msg.Content))
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

func SanitizeFinal(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	for {
		start := strings.Index(text, "<think>")
		end := strings.Index(text, "</think>")
		if start == -1 || end == -1 || end < start {
			break
		}
		text = strings.TrimSpace(text[:start] + text[end+len("</think>"):])
	}

	for {
		start := strings.Index(text, "<thinking>")
		end := strings.Index(text, "</thinking>")
		if start == -1 || end == -1 || end < start {
			break
		}
		text = strings.TrimSpace(text[:start] + text[end+len("</thinking>"):])
	}

	replacements := []string{
		"<think>", "",
		"</think>", "",
		"<thinking>", "",
		"</thinking>", "",
	}
	for i := 0; i < len(replacements); i += 2 {
		text = strings.ReplaceAll(text, replacements[i], replacements[i+1])
	}

	text = stripAfterMarker(text, "Output:")
	text = stripAfterMarker(text, "Final Answer:")
	text = stripAfterMarker(text, "Answer:")
	text = stripAfterMarker(text, "Final:")

	lines := strings.Split(text, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed == "</think>" || trimmed == "</thinking>" {
			continue
		}
		filtered = append(filtered, strings.TrimRight(line, " \t"))
	}

	text = strings.TrimSpace(strings.Join(filtered, "\n"))
	text = trimToDirectAnswer(text)
	return strings.TrimSpace(text)
}

func stripAfterMarker(text, marker string) string {
	index := strings.LastIndex(text, marker)
	if index == -1 {
		return text
	}
	suffix := strings.TrimSpace(text[index+len(marker):])
	if suffix == "" {
		return text
	}
	return suffix
}

var directAnswerPattern = regexp.MustCompile(`^(there|here|summary:|result:|final answer:|the answer is|it has|it is)\b`)

func trimToDirectAnswer(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 3 {
		return text
	}

	hasReasoningPrefix := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "thinking process") ||
			strings.Contains(lower, "let me") ||
			strings.Contains(lower, "returned") ||
			strings.Contains(lower, "analyze") ||
			strings.Contains(lower, "that's ") ||
			regexp.MustCompile(`^\d+\.`).MatchString(trimmed) {
			hasReasoningPrefix = true
			break
		}
	}
	if !hasReasoningPrefix {
		return text
	}

	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if directAnswerPattern.MatchString(strings.ToLower(trimmed)) {
			return strings.Join(lines[i:], "\n")
		}
	}
	return text
}
