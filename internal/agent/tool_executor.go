package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
)

type toolExecutor interface {
	ExecuteToolCall(ctx context.Context, turn, index, total int, call model.ToolCall) model.Message
}

type registryToolExecutor struct {
	agent *Agent
}

func (e registryToolExecutor) ExecuteToolCall(ctx context.Context, turn, index, total int, call model.ToolCall) model.Message {
	if e.agent == nil || e.agent.registry == nil {
		return model.Message{
			Role:       "tool",
			ToolCallID: call.ID,
			Content:    annotateToolResult("tool error: tool registry is not configured"),
		}
	}

	args := json.RawMessage(call.Function.Arguments)
	e.agent.logToolStart(index, total, call.Function.Name, call.ID, args)
	e.agent.emitEvent(ctx, "tool_start", map[string]any{
		"turn":    turn,
		"index":   index,
		"total":   total,
		"name":    call.Function.Name,
		"call_id": call.ID,
		"preview": strings.TrimSpace(e.agent.registry.Preview(call.Function.Name, args)),
	})

	inv := tools.ToolInvocation{Name: call.Function.Name, Raw: args}
	if e.agent.permission != nil {
		if err := e.agent.permission.Decide(ctx, inv); err != nil {
			e.agent.registry.RecordToolAudit(ctx, inv, tools.ToolOutcome{Err: err}, "permission_denied")
			output := truncateToolMessage("tool error: "+err.Error(), maxToolMessageChars)
			e.agent.logToolFinish(0, output, err)
			e.agent.emitEvent(ctx, "tool_finish", map[string]any{
				"turn":        turn,
				"index":       index,
				"total":       total,
				"name":        call.Function.Name,
				"call_id":     call.ID,
				"duration_ms": int64(0),
				"status":      toolEventStatus(err),
				"summary":     firstOutputSummary(output),
				"error":       toolEventError(err),
			})
			return model.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    annotateToolResult(output),
			}
		}
	}

	result, err := e.agent.registry.CallStructuredWithoutPermission(ctx, call.Function.Name, args)
	output := truncateToolMessage(result.Output, maxToolMessageChars)
	if err != nil && strings.TrimSpace(output) == "" {
		output = "tool error: " + err.Error()
	}
	e.agent.logToolFinish(resultDuration(result), output, err)
	e.agent.emitEvent(ctx, "tool_finish", map[string]any{
		"turn":        turn,
		"index":       index,
		"total":       total,
		"name":        call.Function.Name,
		"call_id":     call.ID,
		"duration_ms": result.DurationMs,
		"status":      toolEventStatus(err),
		"summary":     firstOutputSummary(output),
		"error":       toolEventError(err),
	})

	return model.Message{
		Role:       "tool",
		ToolCallID: call.ID,
		Content:    annotateToolResult(output),
	}
}

func resultDuration(result tools.ToolResult) time.Duration {
	if result.DurationMs <= 0 {
		return 0
	}
	return time.Duration(result.DurationMs) * time.Millisecond
}
