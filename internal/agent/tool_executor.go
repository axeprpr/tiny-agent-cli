package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
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

	started := time.Now()
	output, err := e.agent.registry.Call(ctx, call.Function.Name, args)
	output = truncateToolMessage(output, maxToolMessageChars)
	e.agent.logToolFinish(time.Since(started), output, err)
	e.agent.emitEvent(ctx, "tool_finish", map[string]any{
		"turn":        turn,
		"index":       index,
		"total":       total,
		"name":        call.Function.Name,
		"call_id":     call.ID,
		"duration_ms": time.Since(started).Milliseconds(),
		"status":      toolEventStatus(err),
		"summary":     firstOutputSummary(output),
		"error":       toolEventError(err),
	})
	if err != nil {
		output = "tool error: " + err.Error()
	}

	return model.Message{
		Role:       "tool",
		ToolCallID: call.ID,
		Content:    annotateToolResult(output),
	}
}
