package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	PermissionModeReadOnly         = "read-only"
	PermissionModeWorkspaceWrite   = "workspace-write"
	PermissionModeDangerFullAccess = "danger-full-access"
	PermissionModePrompt           = "prompt"
	PermissionModeAllow            = "allow"
	PermissionModeDeny             = "deny"

	PermissionModeConfirm     = "confirm"
	PermissionModeDangerously = "dangerously"
)

type PermissionState struct {
	Default  string                  `json:"default,omitempty"`
	Tools    map[string]string       `json:"tools,omitempty"`
	Commands []CommandPermissionRule `json:"commands,omitempty"`
	SavedAt  time.Time               `json:"saved_at"`
}

type CommandPermissionRule struct {
	Pattern string `json:"pattern"`
	Mode    string `json:"mode"`
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
		data: PermissionState{Default: PermissionModePrompt},
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
	store.data.Commands = normalizeCommandRules(store.data.Commands)
	return store, nil
}

func (s *PermissionStore) ModeForTool(name string) string {
	if s == nil {
		return PermissionModePrompt
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	name = strings.TrimSpace(name)
	if mode := normalizePermissionMode(s.data.Tools[name]); mode != "" && mode != PermissionModePrompt {
		return mode
	}
	return normalizePermissionMode(s.data.Default)
}

func (s *PermissionStore) DefaultMode() string {
	if s == nil {
		return PermissionModePrompt
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return normalizePermissionMode(s.data.Default)
}

func (s *PermissionStore) ToolMode(name string) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data.Tools[strings.TrimSpace(name)]
	if !ok {
		return ""
	}
	return normalizePermissionMode(value)
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
	if mode == PermissionModePrompt {
		delete(s.data.Tools, name)
		return
	}
	s.data.Tools[name] = mode
}

func (s *PermissionStore) Snapshot() PermissionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := PermissionState{
		Default:  normalizePermissionMode(s.data.Default),
		Tools:    make(map[string]string, len(s.data.Tools)),
		Commands: append([]CommandPermissionRule(nil), s.data.Commands...),
		SavedAt:  s.data.SavedAt,
	}
	for key, value := range s.data.Tools {
		out.Tools[key] = normalizePermissionMode(value)
	}
	return out
}

func (s *PermissionStore) Replace(state PermissionState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Default = normalizePermissionMode(state.Default)
	s.data.Tools = make(map[string]string, len(state.Tools))
	for key, value := range state.Tools {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		mode := normalizePermissionMode(value)
		if mode == PermissionModePrompt {
			continue
		}
		s.data.Tools[name] = mode
	}
	s.data.Commands = normalizeCommandRules(state.Commands)
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
	s.data.Commands = normalizeCommandRules(s.data.Commands)
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
	} else {
		keys := make([]string, 0, len(state.Tools))
		for key := range state.Tools {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, key+"="+normalizePermissionMode(state.Tools[key]))
		}
	}
	if len(state.Commands) == 0 {
		lines = append(lines, "(no run_command patterns)")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "command_rules:")
	for i, rule := range state.Commands {
		lines = append(lines, formatCommandRule(i+1, rule))
	}
	return strings.Join(lines, "\n")
}

func (s *PermissionStore) CommandRules() []CommandPermissionRule {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]CommandPermissionRule(nil), s.data.Commands...)
}

func (s *PermissionStore) MatchCommandRule(command string) (CommandPermissionRule, bool) {
	if s == nil {
		return CommandPermissionRule{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	normalized := normalizeCommandPattern(command)
	for _, rule := range s.data.Commands {
		if commandPatternMatches(rule.Pattern, normalized) {
			return rule, true
		}
	}
	return CommandPermissionRule{}, false
}

func (s *PermissionStore) SetCommandMode(pattern, mode string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pattern = normalizeCommandPattern(pattern)
	mode = normalizePermissionMode(mode)
	if pattern == "" {
		return
	}
	for i := range s.data.Commands {
		if s.data.Commands[i].Pattern != pattern {
			continue
		}
		if mode == PermissionModePrompt {
			s.data.Commands = append(s.data.Commands[:i], s.data.Commands[i+1:]...)
			return
		}
		s.data.Commands[i].Mode = mode
		return
	}
	if mode == PermissionModePrompt {
		return
	}
	s.data.Commands = append(s.data.Commands, CommandPermissionRule{
		Pattern: pattern,
		Mode:    mode,
	})
}

func (s *PermissionStore) RemoveCommandRule(index int) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.data.Commands) {
		return false
	}
	s.data.Commands = append(s.data.Commands[:index], s.data.Commands[index+1:]...)
	return true
}

func normalizeCommandRules(rules []CommandPermissionRule) []CommandPermissionRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]CommandPermissionRule, 0, len(rules))
	for _, rule := range rules {
		pattern := normalizeCommandPattern(rule.Pattern)
		mode := normalizePermissionMode(rule.Mode)
		if pattern == "" || mode == PermissionModePrompt {
			continue
		}
		out = append(out, CommandPermissionRule{
			Pattern: pattern,
			Mode:    mode,
		})
	}
	return out
}

func normalizeCommandPattern(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func commandPatternMatches(pattern, command string) bool {
	pattern = normalizeCommandPattern(pattern)
	command = normalizeCommandPattern(command)
	if pattern == "" || command == "" {
		return false
	}
	return globToRegexp(pattern).MatchString(command)
}

func globToRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func formatCommandRule(index int, rule CommandPermissionRule) string {
	return strconv.Itoa(index) + ". " + normalizePermissionMode(rule.Mode) + " " + normalizeCommandPattern(rule.Pattern)
}

func normalizePermissionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", PermissionModePrompt, PermissionModeConfirm:
		return PermissionModePrompt
	case PermissionModeReadOnly:
		return PermissionModeReadOnly
	case PermissionModeWorkspaceWrite:
		return PermissionModeWorkspaceWrite
	case PermissionModeDangerFullAccess, PermissionModeDangerously:
		return PermissionModeDangerFullAccess
	case PermissionModeAllow:
		return PermissionModeAllow
	case PermissionModeDeny:
		return PermissionModeDeny
	default:
		return PermissionModePrompt
	}
}

func NormalizePermissionMode(mode string) string {
	return normalizePermissionMode(mode)
}

func PermissionModeRank(mode string) int {
	switch normalizePermissionMode(mode) {
	case PermissionModeReadOnly:
		return 0
	case PermissionModeWorkspaceWrite:
		return 1
	case PermissionModeDangerFullAccess:
		return 2
	case PermissionModeAllow:
		return 3
	default:
		return -1
	}
}

func PermissionModeAllows(activeMode, requiredMode string) bool {
	active := normalizePermissionMode(activeMode)
	required := normalizePermissionMode(requiredMode)
	if active == PermissionModeAllow {
		return true
	}
	if active == PermissionModeDeny {
		return false
	}
	if active == PermissionModePrompt {
		return required == PermissionModeReadOnly
	}
	return PermissionModeRank(active) >= PermissionModeRank(required)
}
