package model

import "testing"

func TestContentStringFromArray(t *testing.T) {
	value := []any{
		map[string]any{"text": "hello"},
		map[string]any{"text": "world"},
	}

	got := ContentString(value)
	if got != "hello\nworld" {
		t.Fatalf("unexpected content string: %q", got)
	}
}

func TestContentStringFromMultimodalParts(t *testing.T) {
	value := []ContentPart{
		{Type: "text", Text: "describe this"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,aaa"}},
	}

	got := ContentString(value)
	if got != "describe this\n[image]" {
		t.Fatalf("unexpected multimodal content string: %q", got)
	}
}

func TestStripThinkingTagsRemovesBlockAndMixedClosers(t *testing.T) {
	input := "start<think>private</thinking> end"
	got := StripThinkingTags(input)
	if got != "start end" {
		t.Fatalf("unexpected stripped text: %q", got)
	}
}

func TestThinkingTagFilterHandlesSplitStreamingTags(t *testing.T) {
	var filter ThinkingTagFilter
	var out string
	out += filter.Strip("hello <thi")
	out += filter.Strip("nk>hidden")
	out += filter.Strip(" stuff</think> world")
	out += filter.Flush()

	if out != "hello  world" {
		t.Fatalf("unexpected streamed output: %q", out)
	}
}

func TestStripThinkingTagsDropsMalformedCloseTagVariant(t *testing.T) {
	input := "A<think>secret</thinking </thinking> B"
	got := StripThinkingTags(input)
	if got != "A B" {
		t.Fatalf("unexpected malformed close handling: %q", got)
	}
}
