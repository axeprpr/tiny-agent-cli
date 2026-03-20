package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type State struct {
	Notes   []string  `json:"notes"`
	SavedAt time.Time `json:"saved_at"`
}

func Path(stateDir string) string {
	return filepath.Join(stateDir, "memory.json")
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

func Normalize(notes []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, note := range notes {
		note = strings.TrimSpace(note)
		if note == "" || seen[note] {
			continue
		}
		seen[note] = true
		out = append(out, note)
	}
	return out
}

func RenderSystemMemory(notes []string) string {
	notes = Normalize(notes)
	if len(notes) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Persistent context memory:\n")
	for _, note := range notes {
		b.WriteString("- ")
		b.WriteString(note)
		b.WriteByte('\n')
	}
	b.WriteString("Use this memory as background context when relevant. Do not mention it unless it helps answer the user.")
	return strings.TrimSpace(b.String())
}

func Add(notes []string, note string) []string {
	notes = append(notes, note)
	return Normalize(notes)
}

func ForgetMatching(notes []string, query string) ([]string, int) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return Normalize(notes), 0
	}

	var out []string
	removed := 0
	for _, note := range Normalize(notes) {
		if strings.Contains(strings.ToLower(note), query) {
			removed++
			continue
		}
		out = append(out, note)
	}
	return out, removed
}

func FormatNotes(notes []string) string {
	notes = Normalize(notes)
	if len(notes) == 0 {
		return "(no memory notes)"
	}

	var b strings.Builder
	for i, note := range notes {
		fmt.Fprintf(&b, "%d. %s\n", i+1, note)
	}
	return strings.TrimSpace(b.String())
}
