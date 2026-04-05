package memory

import (
	"strings"
	"testing"
)

func TestMergePreservesOtherScopes(t *testing.T) {
	base := State{
		Global: []string{"Prefer concise answers"},
		Teams: map[string][]string{
			"team-a": {"Share release checklist"},
		},
		Projects: map[string][]string{
			"/repo/a": {"Use Go"},
			"/repo/b": {"Use Rust"},
		},
	}
	update := State{
		Global: []string{"Prefer concise answers", "Answer in English"},
		Teams: map[string][]string{
			"team-a": {"Share release checklist", "Use squash merges"},
		},
		Projects: map[string][]string{
			"/repo/a": {"Use Go", "Run go test"},
		},
	}

	got := Merge(base, update)
	if len(got.Global) != 2 {
		t.Fatalf("unexpected globals: %#v", got.Global)
	}
	if len(got.Teams["team-a"]) != 2 {
		t.Fatalf("unexpected merged team notes: %#v", got.Teams["team-a"])
	}
	if len(got.Projects["/repo/a"]) != 2 {
		t.Fatalf("unexpected merged project notes: %#v", got.Projects["/repo/a"])
	}
	if len(got.Projects["/repo/b"]) != 1 {
		t.Fatalf("expected untouched project scope, got %#v", got.Projects)
	}
}

func TestDeleteTeamScope(t *testing.T) {
	state := State{
		Teams: map[string][]string{
			"team-a": {"one"},
			"team-b": {"two"},
		},
	}
	got := DeleteTeamScope(state, "team-a")
	if _, ok := got.Teams["team-a"]; ok {
		t.Fatalf("expected team scope removal, got %#v", got.Teams)
	}
	if len(got.Teams["team-b"]) != 1 {
		t.Fatalf("expected other team scope to remain, got %#v", got.Teams)
	}
}

func TestDeleteScope(t *testing.T) {
	state := State{
		Projects: map[string][]string{
			"/repo/a": {"one"},
			"/repo/b": {"two"},
		},
	}
	got := DeleteScope(state, "/repo/a")
	if _, ok := got.Projects["/repo/a"]; ok {
		t.Fatalf("expected scope removal, got %#v", got.Projects)
	}
	if len(got.Projects["/repo/b"]) != 1 {
		t.Fatalf("expected other scope to remain, got %#v", got.Projects)
	}
}

func TestRenderSystemMemoryIncludesTeamNotes(t *testing.T) {
	got := RenderSystemMemory(
		[]string{"Global"},
		[]string{"Team"},
		[]string{"Project"},
	)
	for _, want := range []string{"Global notes:", "Team notes:", "Project notes:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}
