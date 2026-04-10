package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if e.agent.permissionPolicy != nil {
		decision := e.agent.permissionPolicy.Evaluate(ctx, inv)
		e.agent.emitEvent(ctx, "permission_decision", map[string]any{
			"turn":          turn,
			"index":         index,
			"total":         total,
			"tool":          call.Function.Name,
			"call_id":       call.ID,
			"required_mode": tools.RequiredPermissionMode(call.Function.Name),
			"decision":      decision,
		})
		if !decision.Allowed {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "tool is denied by permission policy"
			}
			err := errors.New(reason)
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
	} else if e.agent.permission != nil {
		err := e.agent.permission.Decide(ctx, inv)
		e.agent.emitEvent(ctx, "permission_decision", map[string]any{
			"turn":          turn,
			"index":         index,
			"total":         total,
			"tool":          call.Function.Name,
			"call_id":       call.ID,
			"required_mode": tools.RequiredPermissionMode(call.Function.Name),
			"decision": tools.PermissionDecision{
				Allowed: err == nil,
				Reason:  strings.TrimSpace(toolEventError(err)),
			},
		})
		if err != nil {
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

	preHookResult := e.agent.hookRunner.RunPreToolUse(call.Function.Name, string(args))
	if preHookResult.IsDenied() {
		err := errors.New(tools.FormatHookMessage(preHookResult, "PreToolUse hook denied tool `"+call.Function.Name+"`"))
		outcome := tools.ToolOutcome{
			Output: tools.MergeHookFeedback(preHookResult.Messages(), "", true),
			Err:    err,
		}
		e.agent.registry.RecordToolAudit(ctx, inv, outcome, "pre_hook_denied")
		output := truncateToolMessage(outcome.Output, maxToolMessageChars)
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

	result, err := e.agent.registry.CallStructuredForRuntime(ctx, call.Function.Name, args)
	outcome := tools.ToolOutcome{
		Output:   tools.MergeHookFeedback(preHookResult.Messages(), result.Output, false),
		Err:      err,
		Duration: resultDuration(result),
	}
	postHookResult := e.agent.hookRunner.RunPostToolUse(call.Function.Name, string(args), outcome.Output, outcome.Err != nil)
	status := result.Status
	if postHookResult.IsDenied() {
		status = "post_hook_denied"
		outcome.Err = errors.New(tools.FormatHookMessage(postHookResult, "PostToolUse hook denied tool `"+call.Function.Name+"`"))
	}
	outcome.Output = tools.MergeHookFeedback(postHookResult.Messages(), outcome.Output, postHookResult.IsDenied())
	if status == "" {
		if outcome.Err != nil {
			status = "error"
		} else {
			status = "ok"
		}
	}
	e.agent.registry.RecordToolAudit(ctx, inv, outcome, status)

	output := compactToolResultForConversation(truncateToolMessage(outcome.Output, maxToolMessageChars), maxToolMessageChars)
	if outcome.Err != nil {
		errLine := "tool error: " + outcome.Err.Error()
		if strings.TrimSpace(output) == "" {
			output = errLine
		} else if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(output)), "tool error:") {
			output = errLine + "\n" + output
		}
	}
	e.agent.logToolFinish(outcome.Duration, output, outcome.Err)
	e.agent.emitEvent(ctx, "tool_finish", map[string]any{
		"turn":        turn,
		"index":       index,
		"total":       total,
		"name":        call.Function.Name,
		"call_id":     call.ID,
		"duration_ms": outcome.Duration.Milliseconds(),
		"status":      toolEventStatus(outcome.Err),
		"summary":     firstOutputSummary(output),
		"error":       toolEventError(outcome.Err),
	})

	return model.Message{
		Role:       "tool",
		ToolCallID: call.ID,
		Content:    annotateToolResult(output),
	}
}

func compactToolResultForConversation(output string, limit int) string {
	output = strings.TrimSpace(output)
	if output == "" || limit <= 0 || len(output) <= limit {
		return output
	}
	header := fmt.Sprintf("[tool output truncated: %d chars, %d lines]\n", len(output), strings.Count(output, "\n")+1)
	bodyLimit := limit - len(header)
	if bodyLimit < 120 {
		bodyLimit = 120
	}
	return header + truncateToolMessage(output, bodyLimit)
}

func resultDuration(result tools.ToolResult) time.Duration {
	if result.DurationMs <= 0 {
		return 0
	}
	return time.Duration(result.DurationMs) * time.Millisecond
}
