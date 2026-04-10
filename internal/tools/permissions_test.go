package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPermissionStoreReplace(t *testing.T) {
	store := &PermissionStore{
		data: PermissionState{
			Default: PermissionModeConfirm,
			Tools: map[string]string{
				"run_command": PermissionModeAllow,
			},
		},
	}
	store.Replace(PermissionState{
		Default: PermissionModeAllow,
		Tools: map[string]string{
			"write_file": PermissionModeDeny,
		},
	})

	got := store.Snapshot()
	if got.Default != PermissionModeAllow {
		t.Fatalf("unexpected default mode: %#v", got)
	}
	if len(got.Tools) != 1 || got.Tools["write_file"] != PermissionModeDeny {
		t.Fatalf("unexpected tool modes: %#v", got.Tools)
	}
	if len(got.Commands) != 0 {
		t.Fatalf("unexpected command rules: %#v", got.Commands)
	}
}

func TestPermissionStoreCommandRulesMatchAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.json")
	store, err := LoadPermissionStore(path)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}

	store.SetCommandMode("git status *", PermissionModeAllow)
	store.SetCommandMode("git push *", PermissionModeDeny)
	if err := store.Save(); err != nil {
		t.Fatalf("save store: %v", err)
	}

	reloaded, err := LoadPermissionStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	rule, ok := reloaded.MatchCommandRule("git push origin main")
	if !ok {
		t.Fatalf("expected matching command rule")
	}
	if rule.Mode != PermissionModeDeny || rule.Pattern != "git push *" {
		t.Fatalf("unexpected command rule: %#v", rule)
	}

	formatted := FormatPermissionState(reloaded.Snapshot())
	for _, want := range []string{"command_rules:", "1. allow git status *", "2. deny git push *"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted state to contain %q, got %q", want, formatted)
		}
	}
}

func TestPermissionStoreRemoveCommandRule(t *testing.T) {
	store := &PermissionStore{
		data: PermissionState{
			Default: PermissionModePrompt,
			Commands: []CommandPermissionRule{
				{Pattern: "git status *", Mode: PermissionModeAllow},
				{Pattern: "git push *", Mode: PermissionModeDeny},
			},
		},
	}
	if !store.RemoveCommandRule(0) {
		t.Fatalf("expected remove to succeed")
	}
	if len(store.CommandRules()) != 1 || store.CommandRules()[0].Pattern != "git push *" {
		t.Fatalf("unexpected command rules after remove: %#v", store.CommandRules())
	}
}
