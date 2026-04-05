package memory

import "testing"

func TestMergePreservesOtherScopes(t *testing.T) {
	base := State{
		Global: []string{"Prefer concise answers"},
		Projects: map[string][]string{
			"/repo/a": {"Use Go"},
			"/repo/b": {"Use Rust"},
		},
	}
	update := State{
		Global: []string{"Prefer concise answers", "Answer in English"},
		Projects: map[string][]string{
			"/repo/a": {"Use Go", "Run go test"},
		},
	}

	got := Merge(base, update)
	if len(got.Global) != 2 {
		t.Fatalf("unexpected globals: %#v", got.Global)
	}
	if len(got.Projects["/repo/a"]) != 2 {
		t.Fatalf("unexpected merged project notes: %#v", got.Projects["/repo/a"])
	}
	if len(got.Projects["/repo/b"]) != 1 {
		t.Fatalf("expected untouched project scope, got %#v", got.Projects)
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
