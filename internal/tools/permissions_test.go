package tools

import "testing"

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
}
