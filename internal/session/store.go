package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/platform"
	"tiny-agent-cli/internal/tools"
)

type State struct {
	SessionID     string             `json:"session_id,omitempty"`
	ParentSession string             `json:"parent_session,omitempty"`
	SessionName   string             `json:"session_name"`
	Model         string             `json:"model"`
	OutputMode    string             `json:"output_mode"`
	ApprovalMode  string             `json:"approval_mode"`
	TeamKey       string             `json:"team_key,omitempty"`
	ScopeKey      string             `json:"scope_key,omitempty"`
	GlobalMemory  []string           `json:"global_memory,omitempty"`
	TeamMemory    []string           `json:"team_memory,omitempty"`
	ProjectMemory []string           `json:"project_memory,omitempty"`
	Jobs          json.RawMessage    `json:"jobs,omitempty"`
	SavedAt       time.Time          `json:"saved_at"`
	TodoItems     []tools.TodoItem   `json:"todo_items,omitempty"`
	TaskContract  tools.TaskContract `json:"task_contract,omitempty"`
	Messages      []model.Message    `json:"messages"`
}

type Summary struct {
	Name         string
	Path         string
	SessionID    string
	Parent       string
	SavedAt      time.Time
	Model        string
	ApprovalMode string
	MessageCount int
}

type TreeNode struct {
	Summary    Summary
	ParentName string
	Children   []*TreeNode
}

func SessionPath(stateDir, name string) string {
	return filepath.Join(stateDir, "sessions", platform.SafeName(name)+".json")
}

func TranscriptPath(stateDir, name string) string {
	return filepath.Join(stateDir, "transcripts", platform.SafeName(name)+".log")
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
			Path:         path,
			SessionID:    strings.TrimSpace(state.SessionID),
			Parent:       strings.TrimSpace(state.ParentSession),
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

func BuildSessionTree(stateDir string) ([]*TreeNode, error) {
	summaries, err := ListSessions(stateDir)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, nil
	}

	nodes := make(map[string]*TreeNode, len(summaries))
	idToName := make(map[string]string, len(summaries))
	pathToName := make(map[string]string, len(summaries))
	safeToName := make(map[string]string, len(summaries))
	for _, item := range summaries {
		copied := item
		nodes[item.Name] = &TreeNode{Summary: copied}
		if id := strings.TrimSpace(item.SessionID); id != "" {
			idToName[id] = item.Name
		}
		if p := strings.TrimSpace(item.Path); p != "" {
			pathToName[p] = item.Name
		}
		safeToName[platform.SafeName(item.Name)] = item.Name
	}

	roots := make([]*TreeNode, 0, len(summaries))
	for _, item := range summaries {
		node := nodes[item.Name]
		parent := resolveParentName(item.Parent, idToName, pathToName, safeToName, nodes)
		node.ParentName = parent
		if parent == "" || parent == item.Name {
			roots = append(roots, node)
			continue
		}
		parentNode := nodes[parent]
		if parentNode == nil {
			roots = append(roots, node)
			continue
		}
		parentNode.Children = append(parentNode.Children, node)
	}

	sortTreeNodes(roots)
	return roots, nil
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

func NewSessionID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UTC().UnixNano())
	}
	return "sess-" + hex.EncodeToString(buf)
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

func resolveParentName(parent string, idToName, pathToName, safeToName map[string]string, nodes map[string]*TreeNode) string {
	ref := strings.TrimSpace(parent)
	if ref == "" {
		return ""
	}
	if name := strings.TrimSpace(idToName[ref]); name != "" {
		return name
	}
	if name := strings.TrimSpace(pathToName[ref]); name != "" {
		return name
	}
	base := strings.TrimSuffix(filepath.Base(ref), filepath.Ext(ref))
	if name := strings.TrimSpace(safeToName[base]); name != "" {
		return name
	}
	if name := strings.TrimSpace(safeToName[platform.SafeName(ref)]); name != "" {
		return name
	}
	if _, ok := nodes[ref]; ok {
		return ref
	}
	return ""
}

func sortTreeNodes(nodes []*TreeNode) {
	sort.Slice(nodes, func(i, j int) bool {
		left := nodes[i].Summary
		right := nodes[j].Summary
		switch {
		case left.SavedAt.Equal(right.SavedAt):
			return strings.ToLower(left.Name) < strings.ToLower(right.Name)
		case left.SavedAt.IsZero():
			return false
		case right.SavedAt.IsZero():
			return true
		default:
			return left.SavedAt.After(right.SavedAt)
		}
	})
	for _, node := range nodes {
		if len(node.Children) > 0 {
			sortTreeNodes(node.Children)
		}
	}
}
