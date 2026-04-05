package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
)

type State struct {
	SessionName   string          `json:"session_name"`
	Model         string          `json:"model"`
	OutputMode    string          `json:"output_mode"`
	ApprovalMode  string          `json:"approval_mode"`
	TeamKey       string          `json:"team_key,omitempty"`
	ScopeKey      string          `json:"scope_key,omitempty"`
	GlobalMemory  []string        `json:"global_memory,omitempty"`
	TeamMemory    []string        `json:"team_memory,omitempty"`
	ProjectMemory []string        `json:"project_memory,omitempty"`
	Jobs          json.RawMessage `json:"jobs,omitempty"`
	SavedAt       time.Time       `json:"saved_at"`
	Messages      []model.Message `json:"messages"`
}

type Summary struct {
	Name         string
	SavedAt      time.Time
	Model        string
	ApprovalMode string
	MessageCount int
}

func SessionPath(stateDir, name string) string {
	return filepath.Join(stateDir, "sessions", safeName(name)+".json")
}

func TranscriptPath(stateDir, name string) string {
	return filepath.Join(stateDir, "transcripts", safeName(name)+".log")
}

func ListSessionNames(stateDir string) ([]string, error) {
	summaries, err := ListSessions(stateDir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(summaries))
	for _, entry := range summaries {
		names = append(names, entry.Name)
	}
	return names, nil
}

func ListSessions(stateDir string) ([]Summary, error) {
	dir := filepath.Join(stateDir, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	out := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		state, err := Load(path)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(state.SessionName)
		if name == "" {
			name = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		}
		out = append(out, Summary{
			Name:         name,
			SavedAt:      state.SavedAt,
			Model:        strings.TrimSpace(state.Model),
			ApprovalMode: strings.TrimSpace(state.ApprovalMode),
			MessageCount: len(state.Messages),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].SavedAt.After(out[j].SavedAt)
	})
	return out, nil
}

func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

func Save(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	state.SavedAt = time.Now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func AppendTranscript(path, role, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	line := fmt.Sprintf("[%s] %s: %s\n", time.Now().Format(time.RFC3339), role, strings.TrimSpace(text))
	_, err = f.WriteString(line)
	return err
}

func safeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, " ", "-")
	return name
}
