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
	SessionName  string          `json:"session_name"`
	Model        string          `json:"model"`
	OutputMode   string          `json:"output_mode"`
	ApprovalMode string          `json:"approval_mode"`
	SavedAt      time.Time       `json:"saved_at"`
	Messages     []model.Message `json:"messages"`
}

func SessionPath(stateDir, name string) string {
	return filepath.Join(stateDir, "sessions", safeName(name)+".json")
}

func TranscriptPath(stateDir, name string) string {
	return filepath.Join(stateDir, "transcripts", safeName(name)+".log")
}

func ListSessionNames(stateDir string) ([]string, error) {
	dir := filepath.Join(stateDir, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	type sessionEntry struct {
		name    string
		modTime time.Time
	}

	out := make([]sessionEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, sessionEntry{
			name:    strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			modTime: info.ModTime(),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].modTime.After(out[j].modTime)
	})

	names := make([]string, 0, len(out))
	for _, entry := range out {
		names = append(names, entry.name)
	}
	return names, nil
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
	return os.WriteFile(path, data, 0o644)
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
