package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverDirFindsSharedObjects(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "alpha.so"))
	mustWriteFile(t, filepath.Join(dir, "notes.txt"))

	items, err := discoverDir(dir)
	if err != nil {
		t.Fatalf("discover dir: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected plugins: %#v", items)
	}
	if items[0].Name != "alpha" {
		t.Fatalf("unexpected plugin name: %#v", items[0])
	}
}

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
