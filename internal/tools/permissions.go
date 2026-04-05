package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	PermissionModeConfirm = "confirm"
	PermissionModeAllow   = "allow"
	PermissionModeDeny    = "deny"
)

type PermissionState struct {
	Default string            `json:"default,omitempty"`
	Tools   map[string]string `json:"tools,omitempty"`
	SavedAt time.Time         `json:"saved_at"`
}

type PermissionStore struct {
	path string
	mu   sync.RWMutex
	data PermissionState
}

func PermissionPath(stateDir string) string {
	return filepath.Join(stateDir, "permissions.json")
}

func LoadPermissionStore(path string) (*PermissionStore, error) {
	store := &PermissionStore{
		path: path,
		data: PermissionState{Default: PermissionModeConfirm},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &store.data); err != nil {
		return nil, err
	}
	store.data.Default = normalizePermissionMode(store.data.Default)
	if store.data.Tools == nil {
		store.data.Tools = make(map[string]string)
	}
	for key, value := range store.data.Tools {
		store.data.Tools[key] = normalizePermissionMode(value)
	}
	return store, nil
}

func (s *PermissionStore) ModeForTool(name string) string {
	if s == nil {
		return PermissionModeConfirm
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	name = strings.TrimSpace(name)
	if mode := normalizePermissionMode(s.data.Tools[name]); mode != "" && mode != PermissionModeConfirm {
		return mode
	}
	return normalizePermissionMode(s.data.Default)
}

func (s *PermissionStore) SetDefault(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Default = normalizePermissionMode(mode)
}

func (s *PermissionStore) SetToolMode(name, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Tools == nil {
		s.data.Tools = make(map[string]string)
	}
	name = strings.TrimSpace(name)
	mode = normalizePermissionMode(mode)
	if name == "" {
		return
	}
	if mode == PermissionModeConfirm {
		delete(s.data.Tools, name)
		return
	}
	s.data.Tools[name] = mode
}

func (s *PermissionStore) Snapshot() PermissionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := PermissionState{
		Default: normalizePermissionMode(s.data.Default),
		Tools:   make(map[string]string, len(s.data.Tools)),
		SavedAt: s.data.SavedAt,
	}
	for key, value := range s.data.Tools {
		out.Tools[key] = normalizePermissionMode(value)
	}
	return out
}

func (s *PermissionStore) Save() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	s.data.Default = normalizePermissionMode(s.data.Default)
	s.data.SavedAt = time.Now().UTC()
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func FormatPermissionState(state PermissionState) string {
	lines := []string{"default=" + normalizePermissionMode(state.Default)}
	if len(state.Tools) == 0 {
		lines = append(lines, "(no per-tool overrides)")
		return strings.Join(lines, "\n")
	}
	keys := make([]string, 0, len(state.Tools))
	for key := range state.Tools {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, key+"="+normalizePermissionMode(state.Tools[key]))
	}
	return strings.Join(lines, "\n")
}

func normalizePermissionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", PermissionModeConfirm:
		return PermissionModeConfirm
	case PermissionModeAllow:
		return PermissionModeAllow
	case PermissionModeDeny:
		return PermissionModeDeny
	default:
		return PermissionModeConfirm
	}
}
