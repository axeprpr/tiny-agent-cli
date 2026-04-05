package tasks

import (
	"path/filepath"
	"testing"
)

func TestStoreCreateUpdateDelete(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "tasks.json"))
	item, err := store.Create("ship P3", "wire it up")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.ID != "task-001" {
		t.Fatalf("unexpected id: %q", item.ID)
	}

	status := StatusInProgress
	title := "ship task system"
	updated, err := store.Update(item.ID, Update{Status: &status, Title: &title})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != StatusInProgress || updated.Title != title {
		t.Fatalf("unexpected updated item: %#v", updated)
	}

	if err := store.Delete(item.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("expected empty store, got %#v", got)
	}
}

func TestFormat(t *testing.T) {
	text := Format([]Item{{ID: "task-001", Status: StatusPending, Title: "test", Details: "more info"}})
	if text == "" || text == "no tasks" {
		t.Fatalf("unexpected format: %q", text)
	}
}
