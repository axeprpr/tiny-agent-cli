package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tiny-agent-cli/internal/model"
)

type TaskContract struct {
	TaskKind         string         `json:"task_kind,omitempty"`
	Objective        string         `json:"objective"`
	Deliverables     []ContractItem `json:"deliverables,omitempty"`
	AcceptanceChecks []ContractItem `json:"acceptance_checks,omitempty"`
}

type ContractItem struct {
	Text         string `json:"text"`
	Status       string `json:"status"`
	Evidence     string `json:"evidence,omitempty"`
	EvidenceKind string `json:"evidence_kind,omitempty"`
	Terminal     bool   `json:"terminal,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Handoff      string `json:"handoff,omitempty"`
}

type TaskContractPatch struct {
	TaskKind         *string         `json:"task_kind,omitempty"`
	Objective        *string         `json:"objective,omitempty"`
	Deliverables     *[]ContractItem `json:"deliverables,omitempty"`
	AcceptanceChecks *[]ContractItem `json:"acceptance_checks,omitempty"`
}

type contractStore struct {
	mu       sync.Mutex
	contract TaskContract
	path     string
}

type contractFileV1 struct {
	Version   int          `json:"version"`
	UpdatedAt string       `json:"updated_at,omitempty"`
	Contract  TaskContract `json:"contract"`
}

type updateTaskContractTool struct {
	store *contractStore
}

type showTaskContractTool struct {
	store *contractStore
}

func newContractStoreWithPath(path string) *contractStore {
	store := &contractStore{path: strings.TrimSpace(path)}
	_ = store.load()
	return store
}

func ContractPath(workDir string) string {
	return filepath.Join(workDir, ".tacli", "contract-v1.json")
}

func LoadTaskContract(path string) (TaskContract, error) {
	store := newContractStoreWithPath(path)
	return store.Current(), nil
}

func SaveTaskContract(path string, contract TaskContract) error {
	store := newContractStoreWithPath(path)
	return store.Replace(contract)
}

func FormatTaskContract(contract TaskContract) string {
	return formatTaskContract(contract)
}

func newUpdateTaskContractTool(store *contractStore) Tool {
	return &updateTaskContractTool{store: store}
}

func newShowTaskContractTool(store *contractStore) Tool {
	return &showTaskContractTool{store: store}
}

func (t *updateTaskContractTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "update_task_contract",
			Description: "Create or patch the semantic task contract for the current work. Omitted fields keep their current values. Use it for non-trivial engineering tasks so the runtime can track deliverables and acceptance checks before finishing.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_kind": map[string]any{
						"type":        "string",
						"description": "Semantic task kind, such as webapp_with_deploy, bugfix, review, refactor, migration, or repo_research.",
					},
					"objective": map[string]any{
						"type":        "string",
						"description": "Short statement of the user-visible goal. Required when creating a new contract.",
					},
					"deliverables":      contractItemsSchema("Concrete outputs that must exist before the task is done."),
					"acceptance_checks": contractItemsSchema("Concrete checks that must pass before the task is done."),
				},
			},
		},
	}
}

func contractItemsSchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items": map[string]any{
			"type":     "object",
			"required": []string{"text", "status"},
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Specific deliverable or check.",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "One of pending, in_progress, completed, blocked.",
					"enum":        []string{"pending", "in_progress", "completed", "blocked"},
				},
				"evidence": map[string]any{
					"type":        "string",
					"description": "Short evidence note showing how this item was verified.",
				},
				"evidence_kind": map[string]any{
					"type":        "string",
					"description": "Expected evidence type. Use static, test, http, browser, runtime, manual_handoff, or file.",
					"enum":        []string{"static", "test", "http", "browser", "runtime", "manual_handoff", "file"},
				},
				"terminal": map[string]any{
					"type":        "boolean",
					"description": "When true, a blocked status can be treated as a terminal handoff state if reason and handoff are provided.",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Reason for a blocked item, especially for environment or permission constraints.",
				},
				"handoff": map[string]any{
					"type":        "string",
					"description": "Concrete next step for the user or another agent when the item is blocked.",
				},
			},
		},
	}
}

func (t *updateTaskContractTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var patch TaskContractPatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	if err := t.store.Patch(patch); err != nil {
		return "", err
	}
	return formatTaskContract(t.store.Current()), nil
}

func (t *showTaskContractTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "show_task_contract",
			Description: "Show the current semantic task contract, including deliverables and acceptance checks.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *showTaskContractTool) Call(_ context.Context, _ json.RawMessage) (string, error) {
	contract := t.store.Current()
	if strings.TrimSpace(contract.Objective) == "" && len(contract.Deliverables) == 0 && len(contract.AcceptanceChecks) == 0 {
		return "(no task contract)", nil
	}
	return formatTaskContract(contract), nil
}

func normalizeContractStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending":
		return "pending"
	case "in_progress":
		return "in_progress"
	case "completed":
		return "completed"
	case "blocked":
		return "blocked"
	default:
		return ""
	}
}

func normalizeEvidenceKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "":
		return ""
	case "static":
		return "static"
	case "test":
		return "test"
	case "http":
		return "http"
	case "browser":
		return "browser"
	case "runtime":
		return "runtime"
	case "manual_handoff":
		return "manual_handoff"
	case "file":
		return "file"
	default:
		return ""
	}
}

func (s *contractStore) Replace(contract TaskContract) error {
	contract.Objective = strings.Join(strings.Fields(strings.TrimSpace(contract.Objective)), " ")
	contract.TaskKind = strings.Join(strings.Fields(strings.TrimSpace(contract.TaskKind)), " ")
	if contract.Objective == "" {
		return fmt.Errorf("objective must not be empty")
	}
	deliverables, err := normalizeContractItems(contract.Deliverables)
	if err != nil {
		return fmt.Errorf("deliverables: %w", err)
	}
	checks, err := normalizeContractItems(contract.AcceptanceChecks)
	if err != nil {
		return fmt.Errorf("acceptance_checks: %w", err)
	}
	if len(deliverables) == 0 && len(checks) == 0 {
		return fmt.Errorf("task contract must include deliverables or acceptance checks")
	}
	s.mu.Lock()
	s.contract = TaskContract{
		TaskKind:         contract.TaskKind,
		Objective:        contract.Objective,
		Deliverables:     deliverables,
		AcceptanceChecks: checks,
	}
	s.mu.Unlock()
	return s.save()
}

func (s *contractStore) Patch(patch TaskContractPatch) error {
	current := s.Current()
	if patch.TaskKind != nil {
		current.TaskKind = *patch.TaskKind
	}
	if patch.Objective != nil {
		current.Objective = *patch.Objective
	}
	if patch.Deliverables != nil {
		current.Deliverables = append([]ContractItem(nil), (*patch.Deliverables)...)
	}
	if patch.AcceptanceChecks != nil {
		current.AcceptanceChecks = append([]ContractItem(nil), (*patch.AcceptanceChecks)...)
	}
	return s.Replace(current)
}

func normalizeContractItems(items []ContractItem) ([]ContractItem, error) {
	normalized := make([]ContractItem, 0, len(items))
	for _, item := range items {
		text := strings.Join(strings.Fields(strings.TrimSpace(item.Text)), " ")
		status := normalizeContractStatus(item.Status)
		evidence := strings.TrimSpace(item.Evidence)
		evidenceKind := normalizeEvidenceKind(item.EvidenceKind)
		reason := strings.Join(strings.Fields(strings.TrimSpace(item.Reason)), " ")
		handoff := strings.Join(strings.Fields(strings.TrimSpace(item.Handoff)), " ")
		if text == "" {
			return nil, fmt.Errorf("item text must not be empty")
		}
		if status == "" {
			return nil, fmt.Errorf("invalid status %q", item.Status)
		}
		if item.EvidenceKind != "" && evidenceKind == "" {
			return nil, fmt.Errorf("invalid evidence_kind %q", item.EvidenceKind)
		}
		if status == "blocked" && reason == "" {
			return nil, fmt.Errorf("blocked item must include reason")
		}
		if item.Terminal && status == "blocked" && handoff == "" {
			return nil, fmt.Errorf("terminal blocked item must include handoff")
		}
		normalized = append(normalized, ContractItem{
			Text:         text,
			Status:       status,
			Evidence:     evidence,
			EvidenceKind: evidenceKind,
			Terminal:     item.Terminal,
			Reason:       reason,
			Handoff:      handoff,
		})
	}
	return normalized, nil
}

func (s *contractStore) Current() TaskContract {
	_ = s.load()
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneTaskContract(s.contract)
}

func (s *contractStore) Clear() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.contract = TaskContract{}
	s.mu.Unlock()
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func cloneTaskContract(contract TaskContract) TaskContract {
	out := TaskContract{
		TaskKind:  contract.TaskKind,
		Objective: contract.Objective,
	}
	out.Deliverables = append([]ContractItem(nil), contract.Deliverables...)
	out.AcceptanceChecks = append([]ContractItem(nil), contract.AcceptanceChecks...)
	return out
}

func formatTaskContract(contract TaskContract) string {
	var lines []string
	if strings.TrimSpace(contract.TaskKind) != "" {
		lines = append(lines, "task_kind="+contract.TaskKind)
	}
	lines = append(lines, "objective="+contract.Objective)
	if len(contract.Deliverables) > 0 {
		lines = append(lines, "deliverables:")
		for i, item := range contract.Deliverables {
			lines = append(lines, fmt.Sprintf("%d. [%s] %s", i+1, contractStatusLabel(item.Status), item.Text))
			if item.Terminal {
				lines = append(lines, "   terminal: true")
			}
			if strings.TrimSpace(item.EvidenceKind) != "" {
				lines = append(lines, "   evidence_kind: "+item.EvidenceKind)
			}
			if strings.TrimSpace(item.Evidence) != "" {
				lines = append(lines, "   evidence: "+item.Evidence)
			}
			if strings.TrimSpace(item.Reason) != "" {
				lines = append(lines, "   reason: "+item.Reason)
			}
			if strings.TrimSpace(item.Handoff) != "" {
				lines = append(lines, "   handoff: "+item.Handoff)
			}
		}
	}
	if len(contract.AcceptanceChecks) > 0 {
		lines = append(lines, "acceptance_checks:")
		for i, item := range contract.AcceptanceChecks {
			lines = append(lines, fmt.Sprintf("%d. [%s] %s", i+1, contractStatusLabel(item.Status), item.Text))
			if item.Terminal {
				lines = append(lines, "   terminal: true")
			}
			if strings.TrimSpace(item.EvidenceKind) != "" {
				lines = append(lines, "   evidence_kind: "+item.EvidenceKind)
			}
			if strings.TrimSpace(item.Evidence) != "" {
				lines = append(lines, "   evidence: "+item.Evidence)
			}
			if strings.TrimSpace(item.Reason) != "" {
				lines = append(lines, "   reason: "+item.Reason)
			}
			if strings.TrimSpace(item.Handoff) != "" {
				lines = append(lines, "   handoff: "+item.Handoff)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func contractStatusLabel(status string) string {
	switch status {
	case "completed":
		return "done"
	case "in_progress":
		return "doing"
	case "blocked":
		return "blocked"
	default:
		return "todo"
	}
}

func (s *contractStore) load() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	var payload contractFileV1
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	objective := strings.TrimSpace(payload.Contract.Objective)
	if objective == "" {
		return nil
	}
	deliverables, err := normalizeContractItems(payload.Contract.Deliverables)
	if err != nil {
		return nil
	}
	checks, err := normalizeContractItems(payload.Contract.AcceptanceChecks)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	s.contract = TaskContract{
		TaskKind:         strings.TrimSpace(payload.Contract.TaskKind),
		Objective:        objective,
		Deliverables:     deliverables,
		AcceptanceChecks: checks,
	}
	s.mu.Unlock()
	return nil
}

func (s *contractStore) save() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	s.mu.Lock()
	contract := cloneTaskContract(s.contract)
	s.mu.Unlock()
	payload := contractFileV1{
		Version:   1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Contract:  contract,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
