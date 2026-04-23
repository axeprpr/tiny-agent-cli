package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tiny-agent-cli/internal/platform"
)

type State struct {
	Notes    []string            `json:"notes,omitempty"`
	Global   []string            `json:"global,omitempty"`
	Teams    map[string][]string `json:"teams,omitempty"`
	Projects map[string][]string `json:"projects,omitempty"`
	SavedAt  time.Time           `json:"saved_at"`
}

type Summary struct {
	SavedAt      time.Time
	GlobalCount  int
	TeamCount    int
	ProjectCount int
	TeamScope    string
	ScopeCount   int
}

func Path(stateDir string) string {
	return filepath.Join(stateDir, "memory.json")
}

func ScopeKey(workDir string) string {
	return platform.NormalizeScopeKey(workDir)
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
	state.Global = Normalize(append(state.Global, state.Notes...))
	state.Teams = normalizeProjects(state.Teams)
	state.Projects = normalizeProjects(state.Projects)
	state.Notes = nil
	return state, nil
}

func Save(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	state.Global = Normalize(state.Global)
	state.Teams = normalizeProjects(state.Teams)
	state.Projects = normalizeProjects(state.Projects)
	state.Notes = nil
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

func Merge(base, update State) State {
	out := State{
		Global:   Normalize(append(base.Global, update.Global...)),
		Teams:    normalizeProjects(base.Teams),
		Projects: normalizeProjects(base.Projects),
		SavedAt:  base.SavedAt,
	}
	if out.Teams == nil {
		out.Teams = make(map[string][]string)
	}
	if out.Projects == nil {
		out.Projects = make(map[string][]string)
	}
	for key, notes := range update.Teams {
		normalized := Normalize(notes)
		if len(normalized) == 0 {
			delete(out.Teams, key)
			continue
		}
		out.Teams[key] = normalized
	}
	for key, notes := range update.Projects {
		normalized := Normalize(notes)
		if len(normalized) == 0 {
			delete(out.Projects, key)
			continue
		}
		out.Projects[key] = normalized
	}
	if len(out.Teams) == 0 {
		out.Teams = nil
	}
	if len(out.Projects) == 0 {
		out.Projects = nil
	}
	return out
}

func DeleteScope(state State, scopeKey string) State {
	scopeKey = strings.TrimSpace(scopeKey)
	if scopeKey == "" || len(state.Projects) == 0 {
		return state
	}
	state.Projects = normalizeProjects(state.Projects)
	delete(state.Projects, scopeKey)
	if len(state.Projects) == 0 {
		state.Projects = nil
	}
	return state
}

func DeleteTeamScope(state State, teamKey string) State {
	teamKey = strings.TrimSpace(teamKey)
	if teamKey == "" || len(state.Teams) == 0 {
		return state
	}
	state.Teams = normalizeProjects(state.Teams)
	delete(state.Teams, teamKey)
	if len(state.Teams) == 0 {
		state.Teams = nil
	}
	return state
}

func Summarize(state State, teamKey, scopeKey string) Summary {
	state.Global = Normalize(state.Global)
	state.Teams = normalizeProjects(state.Teams)
	state.Projects = normalizeProjects(state.Projects)
	return Summary{
		SavedAt:      state.SavedAt,
		GlobalCount:  len(state.Global),
		TeamCount:    len(state.Teams[strings.TrimSpace(teamKey)]),
		ProjectCount: len(state.Projects[strings.TrimSpace(scopeKey)]),
		TeamScope:    strings.TrimSpace(teamKey),
		ScopeCount:   len(state.Teams) + len(state.Projects),
	}
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

func RenderSystemMemory(global, team, project []string) string {
	global = Normalize(global)
	team = Normalize(team)
	project = Normalize(project)
	if len(global) == 0 && len(team) == 0 && len(project) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Persistent context memory:\n")
	if len(global) > 0 {
		b.WriteString("Global notes:\n")
		for _, note := range global {
			b.WriteString("- ")
			b.WriteString(note)
			b.WriteByte('\n')
		}
	}
	if len(team) > 0 {
		b.WriteString("Team notes:\n")
		for _, note := range team {
			b.WriteString("- ")
			b.WriteString(note)
			b.WriteByte('\n')
		}
	}
	if len(project) > 0 {
		b.WriteString("Project notes:\n")
		for _, note := range project {
			b.WriteString("- ")
			b.WriteString(note)
			b.WriteByte('\n')
		}
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

func FormatNotes(global, team, project []string) string {
	global = Normalize(global)
	team = Normalize(team)
	project = Normalize(project)
	if len(global) == 0 && len(team) == 0 && len(project) == 0 {
		return "(no memory notes)"
	}

	var b strings.Builder
	if len(global) > 0 {
		b.WriteString("Global memory:\n")
		for i, note := range global {
			fmt.Fprintf(&b, "%d. %s\n", i+1, note)
		}
	}
	if len(team) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("Team memory:\n")
		for i, note := range team {
			fmt.Fprintf(&b, "%d. %s\n", i+1, note)
		}
	}
	if len(project) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("Project memory:\n")
		for i, note := range project {
			fmt.Fprintf(&b, "%d. %s\n", i+1, note)
		}
	}
	return strings.TrimSpace(b.String())
}

func normalizeProjects(projects map[string][]string) map[string][]string {
	if len(projects) == 0 {
		return nil
	}
	out := make(map[string][]string, len(projects))
	keys := make([]string, 0, len(projects))
	for key := range projects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		notes := Normalize(projects[key])
		if len(notes) > 0 {
			out[key] = notes
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
