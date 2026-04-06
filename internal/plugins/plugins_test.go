package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tiny-agent-cli/internal/tools"
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

func TestManagerUnloadRemovesLoadedPlugin(t *testing.T) {
	dir := t.TempDir()
	manager := &Manager{
		dir: dir,
		discovered: []Descriptor{{
			Name: "alpha",
			Path: filepath.Join(dir, "alpha.so"),
		}},
		loaded: map[string]Loaded{
			filepath.Join(dir, "alpha.so"): {
				Descriptor: Descriptor{Name: "alpha", Path: filepath.Join(dir, "alpha.so")},
				Plugin:     fakePlugin{},
			},
		},
	}

	loaded, ok, err := manager.Unload("alpha")
	if err != nil {
		t.Fatalf("unload: %v", err)
	}
	if !ok {
		t.Fatalf("expected plugin to be unloaded")
	}
	if loaded.Descriptor.Name != "alpha" {
		t.Fatalf("unexpected loaded plugin: %#v", loaded)
	}
	if len(manager.loaded) != 0 {
		t.Fatalf("expected no loaded plugins, got %#v", manager.loaded)
	}
}

func TestManagerReloadLoadedReopensTrackedPlugins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "alpha.so")
	manager := &Manager{
		dir:        dir,
		discovered: []Descriptor{{Name: "alpha", Path: path}},
		loaded: map[string]Loaded{
			path: {
				Descriptor: Descriptor{Name: "alpha", Path: path},
				Plugin:     fakePlugin{},
			},
		},
	}
	orig := openPlugin
	defer func() { openPlugin = orig }()
	openPlugin = func(path string) (Plugin, error) {
		return fakePlugin{}, nil
	}

	reloaded, err := manager.ReloadLoaded()
	if err != nil {
		t.Fatalf("reload loaded: %v", err)
	}
	if len(reloaded) != 1 || reloaded[0].Descriptor.Name != "alpha" {
		t.Fatalf("unexpected reloaded plugins: %#v", reloaded)
	}
}

type fakePlugin struct{}

func (fakePlugin) Metadata() Metadata      { return Metadata{Name: "fake"} }
func (fakePlugin) Tools() []tools.Tool     { return nil }
func (fakePlugin) Hooks() []tools.ToolHook { return nil }
func (fakePlugin) Commands() []Command {
	return []Command{{
		Name: "/fake",
		Handler: func(context.Context, []string, string) (string, error) {
			return "ok", nil
		},
	}}
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
