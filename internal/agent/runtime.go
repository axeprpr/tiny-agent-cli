package agent

import (
	"context"
	"fmt"
	"strings"

	"tiny-agent-cli/internal/model"
)

type ConversationRuntime struct {
	session *Session
}

func NewConversationRuntime(session *Session) *ConversationRuntime {
	return &ConversationRuntime{session: session}
}

func (a *Agent) NewRuntime() *ConversationRuntime {
	if a == nil {
		return NewConversationRuntime(nil)
	}
	return NewConversationRuntime(a.NewSession())
}

func (s *Session) Runtime() *ConversationRuntime {
	return NewConversationRuntime(s)
}

func (r *ConversationRuntime) Session() *Session {
	if r == nil {
		return nil
	}
	return r.session
}

func (r *ConversationRuntime) RunTask(ctx context.Context, task string) (Result, error) {
	if r == nil || r.session == nil {
		return Result{}, fmt.Errorf("conversation runtime is not configured")
	}
	s := r.session
	s.messages = append(s.messages, model.Message{
		Role:    "user",
		Content: task,
	})

	turn := 0
	for {
		turn++
		s.maybeInjectTodoReminder()
		s.compactForContext()
		if stop, reason := shouldStopSession(s.messages); stop {
			s.agent.logf("stopping early: %s\n", reason)
			return Result{Final: reason, Steps: len(s.turns)}, nil
		}

		req := s.buildRequest()
		s.agent.logModelRequest(turn, req)
		s.agent.emitEvent(ctx, "model_request", map[string]any{
			"turn":          turn,
			"messages":      len(req.Messages),
			"tools":         len(req.Tools),
			"approx_tokens": conversationSize(req.Messages),
		})
		const maxContextRetries = 2
		var (
			resp model.Response
			err  error
		)
		for retry := 0; retry <= maxContextRetries; retry++ {
			resp, err = s.agent.client.Complete(ctx, req)
			if err == nil {
				break
			}
			if !isContextLengthError(err) || retry == maxContextRetries {
				break
			}
			if !s.compactForRetry() {
				break
			}
			s.agent.logf("context too long, retrying with shorter history\n")
			req = s.buildRequest()
			s.agent.logModelRequest(turn, req)
		}
		if err != nil {
			return Result{}, err
		}
		if len(resp.Choices) == 0 {
			return Result{}, fmt.Errorf("model returned no choices")
		}

		msg := resp.Choices[0].Message
		s.agent.logModelResponse(turn, msg)
		s.agent.emitEvent(ctx, modelResponseEventType(msg), map[string]any{
			"turn":       turn,
			"chars":      len(strings.TrimSpace(model.ContentString(msg.Content))),
			"tool_calls": len(msg.ToolCalls),
			"tool_names": toolCallNames(msg.ToolCalls, 8),
		})
		if result, shouldContinue := r.handleTurnResult(ctx, turn, msg); !shouldContinue {
			result.Steps = len(s.turns)
			return *result, nil
		}
	}
}

func (r *ConversationRuntime) RunTaskStreaming(ctx context.Context, task string, onToken func(string)) (Result, error) {
	if r == nil || r.session == nil {
		return Result{}, fmt.Errorf("conversation runtime is not configured")
	}
	s := r.session
	if s.agent.streamClient == nil {
		return r.RunTask(ctx, task)
	}

	s.messages = append(s.messages, model.Message{
		Role:    "user",
		Content: task,
	})

	turn := 0
	var streamFilter model.ThinkingTagFilter
	emitToken := onToken
	if onToken != nil {
		emitToken = func(token string) {
			visible := streamFilter.Strip(token)
			if visible != "" {
				onToken(visible)
			}
		}
	}

	for {
		turn++
		s.maybeInjectTodoReminder()
		s.compactForContext()
		if stop, reason := shouldStopSession(s.messages); stop {
			s.agent.logf("stopping early: %s\n", reason)
			return Result{Final: reason, Steps: len(s.turns)}, nil
		}

		req := s.buildRequest()
		s.agent.logModelRequest(turn, req)
		s.agent.emitEvent(ctx, "model_request", map[string]any{
			"turn":          turn,
			"messages":      len(req.Messages),
			"tools":         len(req.Tools),
			"approx_tokens": conversationSize(req.Messages),
		})

		chunks, errc := s.agent.streamClient.CompleteStream(ctx, req)

		var contentBuf strings.Builder
		var role string
		toolCallMap := map[string]*model.ToolCall{}
		var toolCallOrder []string

		for chunk := range chunks {
			for _, choice := range chunk.Choices {
				d := choice.Delta
				if d.Role != "" {
					role = d.Role
				}
				if d.Content != "" {
					contentBuf.WriteString(d.Content)
					if emitToken != nil {
						emitToken(d.Content)
					}
				}
				for _, tc := range d.ToolCalls {
					key := streamToolCallKey(choice.Index, tc, len(toolCallOrder))
					existing, ok := toolCallMap[key]
					if !ok {
						copy := tc
						existing = &copy
						toolCallMap[key] = existing
						toolCallOrder = append(toolCallOrder, key)
					} else {
						if tc.ID != "" {
							existing.ID = tc.ID
						}
						if tc.Function.Name != "" {
							if existing.Function.Name == "" {
								existing.Function.Name = tc.Function.Name
							} else {
								existing.Function.Name += tc.Function.Name
							}
						}
						if tc.Function.Arguments != "" {
							existing.Function.Arguments += tc.Function.Arguments
						}
					}
				}
			}
		}
		if emitToken != nil {
			if tail := streamFilter.Flush(); tail != "" {
				onToken(tail)
			}
		}
		if err := <-errc; err != nil {
			if isContextLengthError(err) {
				if s.compactForRetry() {
					s.agent.logf("context too long, retrying\n")
					continue
				}
			}
			return Result{}, err
		}

		if role == "" {
			role = "assistant"
		}

		var toolCalls []model.ToolCall
		for _, key := range toolCallOrder {
			tc := *toolCallMap[key]
			tc.Index = nil
			toolCalls = append(toolCalls, tc)
		}

		msg := model.Message{
			Role:      role,
			Content:   contentBuf.String(),
			ToolCalls: toolCalls,
		}
		s.agent.logModelResponse(turn, msg)
		s.agent.emitEvent(ctx, modelResponseEventType(msg), map[string]any{
			"turn":       turn,
			"chars":      len(strings.TrimSpace(model.ContentString(msg.Content))),
			"tool_calls": len(msg.ToolCalls),
			"tool_names": toolCallNames(msg.ToolCalls, 8),
		})
		if result, shouldContinue := r.handleTurnResult(ctx, turn, msg); !shouldContinue {
			result.Steps = len(s.turns)
			return *result, nil
		}
	}
}

func (r *ConversationRuntime) handleTurnResult(ctx context.Context, turn int, msg model.Message) (*Result, bool) {
	s := r.session
	decision := decideTurn(s.messages, msg)
	if decision.action == turnActionFinish {
		reminder := s.finishGateReminder()
		if reminder != "" {
			if s.tryAutoCloseTaskContract() {
				reminder = s.finishGateReminder()
				if reminder == "" {
					s.agent.logf("finish gate auto-closed task contract from runtime evidence\n")
				}
			}
		}
		if reminder != "" {
			s.finishGateBlocks++
			if s.finishGateBlocks >= finishGateLoopLimit {
				summary := TurnSummary{
					Turn:      turn,
					Decision:  "finish_gate_doom_loop",
					Assistant: copyMessage(model.Message{Role: "assistant", Content: msg.Content, ToolCalls: msg.ToolCalls}),
					Reminder:  reminder,
					Final:     "Stopped after detecting a finish-gate doom loop. The agent kept trying to finish without satisfying tracked work. Inspect the task contract or acceptance checks before continuing.",
				}
				s.turns = append(s.turns, summary)
				s.agent.emitTurnSummaryEvent(ctx, summary)
				s.agent.logf("finish gate doom loop detected\n")
				result := Result{Final: summary.Final}
				return &result, false
			}
			decision = turnDecision{
				action:   turnActionRetry,
				reminder: reminder,
				logLine:  "finish gate blocked premature completion",
			}
		} else {
			s.finishGateBlocks = 0
		}
	} else {
		s.finishGateBlocks = 0
	}
	if decision.action == turnActionExecuteTools {
		if reminder := s.planGateReminder(msg.ToolCalls); reminder != "" {
			decision = turnDecision{
				action:   turnActionRetry,
				reminder: reminder,
				logLine:  "plan gate blocked mutating tool execution",
			}
		} else if reminder := s.mutatingFailureCooldownReminder(msg.ToolCalls); reminder != "" {
			decision = turnDecision{
				action:   turnActionRetry,
				reminder: reminder,
				logLine:  "mutating failure cooldown blocked another mutating attempt",
			}
		}
	}
	summary := TurnSummary{
		Turn:      turn,
		Decision:  decision.action.String(),
		Assistant: copyMessage(model.Message{Role: "assistant", Content: msg.Content, ToolCalls: msg.ToolCalls}),
		Reminder:  decision.reminder,
		Final:     decision.final,
	}
	switch decision.action {
	case turnActionRetry:
		s.messages = append(s.messages, model.Message{
			Role:    "user",
			Content: decision.reminder,
		})
		s.turns = append(s.turns, summary)
		s.agent.emitTurnSummaryEvent(ctx, summary)
		if decision.logLine != "" {
			s.agent.logf("%s\n", decision.logLine)
		}
		return nil, true
	case turnActionExecuteTools:
		s.messages = append(s.messages, model.Message{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: msg.ToolCalls,
		})
		summary.ToolResults = r.executeToolCalls(ctx, turn, msg.ToolCalls)
		s.turns = append(s.turns, summary)
		s.agent.emitTurnSummaryEvent(ctx, summary)
		s.trackTodoRounds(msg.ToolCalls)
		return nil, true
	default:
		s.messages = append(s.messages, model.Message{
			Role:    "assistant",
			Content: msg.Content,
		})
		s.turns = append(s.turns, summary)
		s.agent.emitTurnSummaryEvent(ctx, summary)
		result := Result{Final: decision.final}
		return &result, false
	}
}

func (r *ConversationRuntime) executeToolCalls(ctx context.Context, turn int, calls []model.ToolCall) []model.Message {
	s := r.session
	s.agent.logf("executing %d tool(s)\n", len(calls))
	exec := s.agent.executor
	if exec == nil {
		exec = registryToolExecutor{agent: s.agent, role: s.role}
	} else if regExec, ok := exec.(registryToolExecutor); ok {
		regExec.role = s.role
		exec = regExec
	}
	results := make([]model.Message, 0, len(calls))
	for i, call := range calls {
		msg := exec.ExecuteToolCall(ctx, turn, i+1, len(calls), call)
		s.messages = append(s.messages, msg)
		results = append(results, copyMessage(msg))
	}
	return results
}
