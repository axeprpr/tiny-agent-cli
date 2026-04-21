package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice  string    `json:"tool_choice,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
}

type Response struct {
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage,omitempty"`
}

type Choice struct {
	Message Message `json:"message"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
	InputTokens      int `json:"input_tokens,omitempty"`
	OutputTokens     int `json:"output_tokens,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamDelta is the incremental content in a single SSE chunk.
type StreamDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// StreamChoice is one choice inside a streaming chunk.
type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// StreamChunk is a single SSE "data:" object.
type StreamChunk struct {
	Choices []StreamChoice `json:"choices"`
}

func ContentString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if text := ContentString(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return text
		}
		if content, ok := v["content"]; ok {
			return ContentString(content)
		}
		if content, ok := v["value"]; ok {
			return ContentString(content)
		}
		if marshaled, err := json.Marshal(v); err == nil {
			return string(marshaled)
		}
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ThinkingTagFilter incrementally strips <think>/<thinking> blocks.
type ThinkingTagFilter struct {
	inBlock bool
	carry   string
}

// Strip removes visible thinking tags and hidden reasoning content from a chunk.
func (f *ThinkingTagFilter) Strip(chunk string) string {
	if chunk == "" {
		return ""
	}
	data := f.carry + chunk
	f.carry = ""

	var out strings.Builder
	for i := 0; i < len(data); {
		if f.inBlock {
			lt := strings.IndexByte(data[i:], '<')
			if lt == -1 {
				// Still inside a hidden block; drop the rest of this chunk.
				i = len(data)
				break
			}
			i += lt
			tag, next, ok := consumeTag(data, i)
			if !ok {
				f.carry = data[i:]
				break
			}
			if isThinkingCloseTag(tag) {
				f.inBlock = false
			}
			i = next
			continue
		}

		lt := strings.IndexByte(data[i:], '<')
		if lt == -1 {
			out.WriteString(data[i:])
			break
		}
		lt += i
		out.WriteString(data[i:lt])

		tag, next, ok := consumeTag(data, lt)
		if !ok {
			f.carry = data[lt:]
			break
		}
		switch {
		case isThinkingOpenTag(tag):
			f.inBlock = true
		case isThinkingCloseTag(tag):
			// Drop dangling close tags from visible output.
		default:
			out.WriteString(tag)
		}
		i = next
	}
	return out.String()
}

// Flush emits any remaining non-hidden buffered text at stream end.
func (f *ThinkingTagFilter) Flush() string {
	if f.inBlock {
		f.carry = ""
		return ""
	}
	rest := f.carry
	f.carry = ""
	return rest
}

// StripThinkingTags removes <think>/<thinking> tags and their enclosed content.
func StripThinkingTags(text string) string {
	var filter ThinkingTagFilter
	return filter.Strip(text) + filter.Flush()
}

func consumeTag(text string, start int) (tag string, next int, ok bool) {
	if start < 0 || start >= len(text) || text[start] != '<' {
		return "", start, false
	}
	end := strings.IndexByte(text[start:], '>')
	if end == -1 {
		return "", start, false
	}
	end += start
	return text[start : end+1], end + 1, true
}

func isThinkingOpenTag(tag string) bool {
	name, closing, ok := parseTagName(tag)
	return ok && !closing && (name == "think" || name == "thinking")
}

func isThinkingCloseTag(tag string) bool {
	name, closing, ok := parseTagName(tag)
	return ok && closing && (name == "think" || name == "thinking")
}

func parseTagName(tag string) (name string, closing bool, ok bool) {
	if len(tag) < 3 || tag[0] != '<' || tag[len(tag)-1] != '>' {
		return "", false, false
	}
	i := 1
	for i < len(tag)-1 && unicode.IsSpace(rune(tag[i])) {
		i++
	}
	if i < len(tag)-1 && tag[i] == '/' {
		closing = true
		i++
		for i < len(tag)-1 && unicode.IsSpace(rune(tag[i])) {
			i++
		}
	}
	start := i
	for i < len(tag)-1 {
		r := rune(tag[i])
		if !unicode.IsLetter(r) {
			break
		}
		i++
	}
	if i == start {
		return "", false, false
	}
	return strings.ToLower(tag[start:i]), closing, true
}
