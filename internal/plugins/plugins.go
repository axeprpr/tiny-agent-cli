package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tiny-agent-cli/internal/tools"
)

const SymbolName = "Plugin"

type Metadata struct {
	Name        string
	Description string
	Version     string
}

type Command struct {
	Name        string
	Description string
	Handler     func(ctx context.Context, args []string, raw string) (string, error)
}

type Plugin interface {
	Metadata() Metadata
	Tools() []tools.Tool
	Hooks() []tools.ToolHook
	Commands() []Command
}

type Descriptor struct {
	Name string
	Path string
}

type Loaded struct {
	Descriptor Descriptor
	Plugin     Plugin
}

type Manager struct {
	dir        string
	discovered []Descriptor
	loaded     map[string]Loaded
}

func NewManager() (*Manager, error) {
	dir, err := pluginDir()
	if err != nil {
		return nil, err
	}
	return &Manager{
		dir:    dir,
		loaded: make(map[string]Loaded),
	}, nil
}

func (m *Manager) Discover() ([]Descriptor, error) {
	if m == nil {
		return nil, nil
	}
	items, err := discoverDir(m.dir)
	if err != nil {
		return nil, err
	}
	m.discovered = items
	out := make([]Descriptor, len(items))
	copy(out, items)
	return out, nil
}

func (m *Manager) List() []Descriptor {
	if m == nil {
		return nil
	}
	out := make([]Descriptor, len(m.discovered))
	copy(out, m.discovered)
	return out
}

func (m *Manager) Loaded() []Loaded {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m.loaded))
	for key := range m.loaded {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Loaded, 0, len(keys))
	for _, key := range keys {
		out = append(out, m.loaded[key])
	}
	return out
}

func (m *Manager) Load(nameOrPath string) (Loaded, error) {
	if m == nil {
		return Loaded{}, fmt.Errorf("plugin manager is not initialized")
	}
	desc, err := m.resolve(nameOrPath)
	if err != nil {
		return Loaded{}, err
	}
	if loaded, ok := m.loaded[desc.Path]; ok {
		return loaded, nil
	}
	instance, err := openPlugin(desc.Path)
	if err != nil {
		return Loaded{}, err
	}
	loaded := Loaded{
		Descriptor: desc,
		Plugin:     instance,
	}
	m.loaded[desc.Path] = loaded
	return loaded, nil
}

func (m *Manager) resolve(nameOrPath string) (Descriptor, error) {
	trimmed := strings.TrimSpace(nameOrPath)
	if trimmed == "" {
		return Descriptor{}, fmt.Errorf("plugin name or path is required")
	}
	if strings.Contains(trimmed, string(os.PathSeparator)) || strings.HasSuffix(trimmed, ".so") {
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return Descriptor{}, err
		}
		return Descriptor{
			Name: strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)),
			Path: abs,
		}, nil
	}
	for _, item := range m.discovered {
		if strings.EqualFold(item.Name, trimmed) {
			return item, nil
		}
	}
	return Descriptor{}, fmt.Errorf("unknown plugin %q", trimmed)
}

func pluginDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "tiny-agent-cli", "plugins"), nil
}

func discoverDir(dir string) ([]Descriptor, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Descriptor, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".so" {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		out = append(out, Descriptor{
			Name: strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Path: full,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}
