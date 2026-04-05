package config

import (
	"context"
	"net/http"
	"testing"

	"tiny-agent-cli/internal/tools"
)

func TestPullSettings(t *testing.T) {
	settingsHTTPClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			return jsonResponse(http.StatusOK, `{"model":"gpt-test","approval_mode":"dangerously","team":"eng","hooks":{"PreToolUse":["printf 'pre'"],"PostToolUse":["printf 'post'"]},"permissions":{"default":"allow","tools":{"write_file":"deny"}}}`), nil
		}),
	}
	t.Cleanup(func() { settingsHTTPClient = http.DefaultClient })

	got, err := PullSettings(context.Background(), "https://settings.example/api")
	if err != nil {
		t.Fatalf("pull settings: %v", err)
	}
	if got.Model != "gpt-test" || got.Team != "eng" {
		t.Fatalf("unexpected settings: %#v", got)
	}
	if got.Permissions.Default != tools.PermissionModeAllow {
		t.Fatalf("unexpected permission state: %#v", got.Permissions)
	}
}

func TestPushSettings(t *testing.T) {
	called := false
	settingsHTTPClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			if r.Method != http.MethodPut {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			return jsonResponse(http.StatusNoContent, ""), nil
		}),
	}
	t.Cleanup(func() { settingsHTTPClient = http.DefaultClient })

	err := PushSettings(context.Background(), "https://settings.example/api", SyncedSettings{
		Model:        "gpt-test",
		ApprovalMode: "confirm",
		Permissions:  tools.PermissionState{Default: tools.PermissionModeConfirm},
	})
	if err != nil {
		t.Fatalf("push settings: %v", err)
	}
	if !called {
		t.Fatalf("expected push handler to be called")
	}
}

func TestApplySettings(t *testing.T) {
	cfg := Config{
		Model:        "before",
		ApprovalMode: "confirm",
		Team:         "local",
	}
	cfg.ApplySettings(SyncedSettings{
		Model:        "after",
		ApprovalMode: "dangerously",
		Team:         "eng",
		Hooks:        tools.HookConfig{PreToolUse: []string{"printf 'pre'"}, PostToolUse: []string{"printf 'post'"}},
	})
	if cfg.Model != "after" || cfg.ApprovalMode != "dangerously" || cfg.Team != "eng" {
		t.Fatalf("unexpected config after apply: %#v", cfg)
	}
	if len(cfg.Hooks.PreToolUse) != 1 || cfg.Hooks.PreToolUse[0] != "printf 'pre'" {
		t.Fatalf("unexpected pre hooks: %#v", cfg.Hooks)
	}
	if len(cfg.Hooks.PostToolUse) != 1 || cfg.Hooks.PostToolUse[0] != "printf 'post'" {
		t.Fatalf("unexpected hooks: %#v", cfg.Hooks)
	}
}
