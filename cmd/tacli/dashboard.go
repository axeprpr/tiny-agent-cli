package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

//go:embed dashboard_assets/index.html
var dashboardAssets embed.FS

const (
	defaultDashboardHost      = "127.0.0.1"
	defaultDashboardPort      = 8421
	maxDashboardPreviewBytes  = 256 * 1024
	maxDashboardRecentFiles   = 20
	maxDashboardArtifactFiles = 12
	maxDashboardUploadMemory  = 64 << 20
	dashboardStateEvent       = "state"
	dashboardPingInterval     = 20 * time.Second
	dashboardTokenDebounce    = 60 * time.Millisecond
	dashboardUploadSubdirName = ".tacli/uploads"
)

var dashboardMarkdown = goldmark.New()
var dashboardSanitizer = bluemonday.UGCPolicy()

type dashboardFile struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modified_at,omitempty"`
	IsText      bool   `json:"is_text"`
	DownloadURL string `json:"download_url"`
	ViewURL     string `json:"view_url,omitempty"`
}

type dashboardToolEvent struct {
	Tool         string `json:"tool"`
	Status       string `json:"status"`
	DurationMs   int64  `json:"duration_ms"`
	ArgsPreview  string `json:"args_preview,omitempty"`
	OutputSample string `json:"output_sample,omitempty"`
	Error        string `json:"error,omitempty"`
}

type dashboardApproval struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Command   string `json:"command,omitempty"`
	Path      string `json:"path,omitempty"`
	Preview   string `json:"preview,omitempty"`
	ByteCount int    `json:"byte_count,omitempty"`
}

type dashboardEntry struct {
	ID        string              `json:"id"`
	Type      string              `json:"type"`
	Role      string              `json:"role,omitempty"`
	Text      string              `json:"text,omitempty"`
	HTML      string              `json:"html,omitempty"`
	Pending   bool                `json:"pending,omitempty"`
	CreatedAt string              `json:"created_at"`
	Files     []dashboardFile     `json:"files,omitempty"`
	Tool      *dashboardToolEvent `json:"tool,omitempty"`
}

type dashboardState struct {
	Version         string             `json:"version"`
	Session         string             `json:"session"`
	SessionID       string             `json:"session_id"`
	Model           string             `json:"model"`
	ApprovalMode    string             `json:"approval_mode"`
	Busy            bool               `json:"busy"`
	PendingApproval *dashboardApproval `json:"pending_approval,omitempty"`
	Entries         []dashboardEntry   `json:"entries"`
	RecentFiles     []dashboardFile    `json:"recent_files,omitempty"`
}

type dashboardSendRequest struct {
	Text        string   `json:"text"`
	Attachments []string `json:"attachments"`
}

type dashboardApproveRequest struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

type dashboardHub struct {
	mu      sync.Mutex
	clients map[chan dashboardEnvelope]struct{}
}

type dashboardEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

func newDashboardHub() *dashboardHub {
	return &dashboardHub{clients: make(map[chan dashboardEnvelope]struct{})}
}

func (h *dashboardHub) Subscribe() chan dashboardEnvelope {
	ch := make(chan dashboardEnvelope, 32)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *dashboardHub) Unsubscribe(ch chan dashboardEnvelope) {
	if ch == nil {
		return
	}
	h.mu.Lock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *dashboardHub) Broadcast(env dashboardEnvelope) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- env:
		default:
		}
	}
}

type workspaceFileState struct {
	Size    int64
	ModUnix int64
}

type approvalDecision struct {
	approve bool
	always  bool
}

type pendingApprovalRequest struct {
	info dashboardApproval
	done chan approvalDecision
}

type webApprover struct {
	server      *dashboardServer
	mu          sync.Mutex
	mode        string
	nextID      int
	allowedCmds map[string]bool
	allowedOps  map[string]bool
	pending     map[string]*pendingApprovalRequest
}

func newWebApprover(mode string, server *dashboardServer) *webApprover {
	return &webApprover{
		server:      server,
		mode:        tools.NormalizePermissionMode(mode),
		allowedCmds: make(map[string]bool),
		allowedOps:  make(map[string]bool),
		pending:     make(map[string]*pendingApprovalRequest),
	}
}

func (a *webApprover) Mode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

func (a *webApprover) SetMode(mode string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	mode = tools.NormalizePermissionMode(mode)
	switch mode {
	case tools.PermissionModePrompt, tools.PermissionModeReadOnly, tools.PermissionModeWorkspaceWrite, tools.PermissionModeDangerFullAccess, tools.PermissionModeAllow:
		a.mode = mode
		if a.server != nil {
			a.server.broadcastState()
		}
		return nil
	default:
		return fmt.Errorf("invalid approval mode %q", mode)
	}
}

func (a *webApprover) ApproveCommand(ctx context.Context, command string) (bool, error) {
	command = strings.TrimSpace(command)
	a.mu.Lock()
	if a.mode == tools.PermissionModeDangerFullAccess || a.mode == tools.PermissionModeAllow || a.allowedCmds[command] {
		a.mu.Unlock()
		return true, nil
	}
	req := &pendingApprovalRequest{
		info: dashboardApproval{
			ID:      a.nextApprovalIDLocked(),
			Kind:    "command",
			Command: command,
		},
		done: make(chan approvalDecision, 1),
	}
	a.pending[req.info.ID] = req
	a.mu.Unlock()

	a.setPending(&req.info)
	defer a.clearPending(req.info.ID)

	select {
	case decision := <-req.done:
		if decision.always {
			a.mu.Lock()
			a.mode = tools.PermissionModeDangerFullAccess
			a.allowedCmds[command] = true
			a.mu.Unlock()
			a.setPending(nil)
			return true, nil
		}
		if decision.approve {
			a.mu.Lock()
			a.allowedCmds[command] = true
			a.mu.Unlock()
			a.setPending(nil)
			return true, nil
		}
		a.setPending(nil)
		return false, nil
	case <-ctx.Done():
		a.setPending(nil)
		return false, ctx.Err()
	}
}

func (a *webApprover) ApproveWrite(ctx context.Context, path, content string) (bool, error) {
	path = strings.TrimSpace(path)
	key := tools.WriteApprovalKey(path, content)
	a.mu.Lock()
	if a.mode == tools.PermissionModeDangerFullAccess || a.mode == tools.PermissionModeAllow || a.allowedOps[key] {
		a.mu.Unlock()
		return true, nil
	}
	req := &pendingApprovalRequest{
		info: dashboardApproval{
			ID:        a.nextApprovalIDLocked(),
			Kind:      "write",
			Path:      path,
			Preview:   buildApprovalPreview(content),
			ByteCount: len(content),
		},
		done: make(chan approvalDecision, 1),
	}
	a.pending[req.info.ID] = req
	a.mu.Unlock()

	a.setPending(&req.info)
	defer a.clearPending(req.info.ID)

	select {
	case decision := <-req.done:
		if decision.always {
			a.mu.Lock()
			a.mode = tools.PermissionModeDangerFullAccess
			a.allowedOps[key] = true
			a.mu.Unlock()
			a.setPending(nil)
			return true, nil
		}
		if decision.approve {
			a.mu.Lock()
			a.allowedOps[key] = true
			a.mu.Unlock()
			a.setPending(nil)
			return true, nil
		}
		a.setPending(nil)
		return false, nil
	case <-ctx.Done():
		a.setPending(nil)
		return false, ctx.Err()
	}
}

func (a *webApprover) Resolve(id, decision string) bool {
	a.mu.Lock()
	req, ok := a.pending[strings.TrimSpace(id)]
	if !ok {
		a.mu.Unlock()
		return false
	}
	delete(a.pending, strings.TrimSpace(id))
	a.mu.Unlock()

	var out approvalDecision
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "approve", "allow", "yes":
		out.approve = true
	case "always", "dangerously":
		out.approve = true
		out.always = true
	default:
		out.approve = false
	}

	select {
	case req.done <- out:
	default:
	}
	return true
}

func (a *webApprover) nextApprovalIDLocked() string {
	a.nextID++
	return "approval-" + strconv.Itoa(a.nextID)
}

func (a *webApprover) setPending(info *dashboardApproval) {
	if a.server != nil {
		a.server.setPendingApproval(info)
	}
}

func (a *webApprover) clearPending(id string) {
	a.mu.Lock()
	delete(a.pending, strings.TrimSpace(id))
	a.mu.Unlock()
	if a.server != nil {
		a.server.setPendingApproval(nil)
	}
}

type dashboardAuditSink struct {
	server *dashboardServer
}

func (s dashboardAuditSink) RecordToolEvent(_ context.Context, event tools.ToolAuditEvent) {
	if s.server == nil {
		return
	}
	s.server.addToolEvent(event)
}

type dashboardServer struct {
	runtime *chatRuntime
	hub     *dashboardHub

	runMu sync.Mutex
	mu    sync.Mutex

	approver        *webApprover
	entries         []dashboardEntry
	recentFiles     []dashboardFile
	runArtifacts    map[string]struct{}
	pendingApproval *dashboardApproval
	busy            bool
	nextEntryID     int

	tokenNotifyMu  sync.Mutex
	tokenScheduled bool
}

func newDashboardServer(runtime *chatRuntime) *dashboardServer {
	s := &dashboardServer{
		runtime: runtime,
		hub:     newDashboardHub(),
	}
	s.rebuildEntriesFromRuntime()
	s.approver = newWebApprover(runtime.cfg.ApprovalMode, s)
	runtime.approver = s.approver
	runtime.outputMode = "raw"
	runtime.onAgentEvent = func(_ agent.AgentEvent) {}
	var jobs tools.JobControl
	if runtime.orchestrator != nil {
		jobs = runtime.orchestrator
		runtime.orchestrator.SetNotifier(func(text string) {
			s.addSystemEntry(strings.TrimSpace(text))
		})
	}
	runtime.loop = buildAgentWith(runtime.cfg, s.approver, io.Discard, jobs, runtime.permissions, dashboardAuditSink{server: s})
	runtime.attachAgentEventSink()
	if runtime.session != nil {
		runtime.session.SetAgent(runtime.loop)
	}
	return s
}

func (s *dashboardServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/message", s.handleMessage)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/file", s.handleFileView)
	mux.HandleFunc("/api/download", s.handleFileDownload)
	mux.HandleFunc("/api/approve", s.handleApprove)
	return mux
}

func runDashboard(args []string) int {
	cfg := config.FromEnv()
	opts := runtimeOptions{
		outputMode: "raw",
	}
	host := defaultDashboardHost
	port := defaultDashboardPort
	dangerously := false

	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "OpenAI-compatible API base URL")
	fs.StringVar(&cfg.Model, "model", cfg.Model, "model name")
	fs.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "optional API key")
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "workspace root")
	fs.StringVar(&opts.conversation, "resume", "", "conversation name to resume or create")
	fs.StringVar(&host, "host", host, "dashboard listen host")
	fs.IntVar(&port, "port", port, "dashboard listen port")
	fs.BoolVar(&dangerously, "dangerously", false, "skip command and file-write approval prompts")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		return 2
	}
	if strings.TrimSpace(os.Getenv("AGENT_STATE_DIR")) == "" {
		cfg.StateDir = config.DefaultStateDir(cfg.WorkDir)
	}
	if dangerously {
		cfg.ApprovalMode = tools.PermissionModeDangerFullAccess
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		return 2
	}

	runtime, err := newChatRuntime(cfg, opts, bufio.NewReader(strings.NewReader("")))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard setup error: %v\n", err)
		return 1
	}
	server := newDashboardServer(runtime)
	addr := net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
	fmt.Fprintf(os.Stdout, "Dashboard listening on http://%s\n", addr)
	if err := http.ListenAndServe(addr, server.routes()); err != nil {
		fmt.Fprintf(os.Stderr, "dashboard error: %v\n", err)
		return 1
	}
	return 0
}

func (s *dashboardServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(dashboardAssets, "dashboard_assets/index.html")
	if err != nil {
		http.Error(w, "dashboard asset missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *dashboardServer) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.snapshot())
}

func (s *dashboardServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sub := s.hub.Subscribe()
	defer s.hub.Unsubscribe(sub)

	writeSSE(w, dashboardStateEvent, s.snapshot())
	flusher.Flush()

	ticker := time.NewTicker(dashboardPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeSSE(w, "ping", map[string]string{"at": time.Now().UTC().Format(time.RFC3339)})
			flusher.Flush()
		case env, ok := <-sub:
			if !ok {
				return
			}
			writeSSE(w, env.Type, env.Data)
			flusher.Flush()
		}
	}
}

func (s *dashboardServer) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req dashboardSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	attachments, err := s.normalizeAttachmentFiles(req.Attachments)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if text == "" && len(attachments) == 0 {
		http.Error(w, "missing message text or attachments", http.StatusBadRequest)
		return
	}
	if strings.HasPrefix(text, "/") && len(attachments) == 0 {
		s.runMu.Lock()
		result := s.runtime.executeCommand(text)
		if !result.handled {
			s.runMu.Unlock()
			http.Error(w, "unknown command", http.StatusBadRequest)
			return
		}
		if result.reloadSessionView {
			s.rebuildEntriesFromRuntime()
		}
		if result.handled && strings.TrimSpace(result.output) != "" {
			s.addSystemEntry(strings.TrimSpace(result.output))
		}
		s.runMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "command": true})
		return
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		http.Error(w, "dashboard is busy", http.StatusConflict)
		return
	}
	displayText := text
	if displayText == "" {
		displayText = "Please inspect the attached files."
	}
	s.addEntryLocked(dashboardEntry{
		Type:      "message",
		Role:      "user",
		Text:      displayText,
		HTML:      renderMarkdownHTML(displayText),
		Files:     attachments,
		CreatedAt: nowText(),
	})
	assistantID := s.addEntryLocked(dashboardEntry{
		Type:      "message",
		Role:      "assistant",
		Pending:   true,
		CreatedAt: nowText(),
	})
	s.busy = true
	s.mu.Unlock()
	s.broadcastState()

	taskText := buildDashboardTask(text, attachments)
	go s.runConversationTask(taskText, assistantID)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *dashboardServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(maxDashboardUploadMemory); err != nil {
		http.Error(w, "invalid multipart body", http.StatusBadRequest)
		return
	}
	var out []dashboardFile
	files := r.MultipartForm.File["files"]
	for _, item := range files {
		saved, err := s.saveUploadedFile(item)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out = append(out, saved)
	}
	s.mu.Lock()
	s.mergeRecentFilesLocked(out)
	s.mu.Unlock()
	s.broadcastState()
	writeJSON(w, http.StatusOK, map[string]any{"files": out})
}

func (s *dashboardServer) handleFileView(w http.ResponseWriter, r *http.Request) {
	file, absPath, err := s.resolveFileFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !file.IsText {
		http.Error(w, "file is not previewable as text", http.StatusBadRequest)
		return
	}
	truncated := false
	if len(data) > maxDashboardPreviewBytes {
		data = data[:maxDashboardPreviewBytes]
		truncated = true
	}
	text := string(data)
	writeJSON(w, http.StatusOK, map[string]any{
		"file":      file,
		"content":   text,
		"truncated": truncated,
	})
}

func (s *dashboardServer) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	file, absPath, err := s.resolveFileFromQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.Name))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, absPath)
}

func (s *dashboardServer) handleApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req dashboardApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if !s.approver.Resolve(req.ID, req.Decision) {
		http.Error(w, "approval request not found", http.StatusNotFound)
		return
	}
	s.broadcastState()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *dashboardServer) runConversationTask(taskText, assistantID string) {
	s.runMu.Lock()
	defer s.runMu.Unlock()

	before, _ := captureWorkspaceSnapshot(s.runtime.cfg.WorkDir)
	s.mu.Lock()
	s.runArtifacts = make(map[string]struct{})
	s.mu.Unlock()
	output, err := s.runtime.executeTaskStreaming(context.Background(), taskText, func(token string) {
		s.appendAssistantToken(assistantID, token)
	})
	after, _ := captureWorkspaceSnapshot(s.runtime.cfg.WorkDir)
	createdFiles := createdWorkspaceFiles(before, after)

	s.mu.Lock()
	artifactPaths := collectDashboardArtifactPaths(s.runArtifacts, createdFiles)
	fileCards := s.buildFileCards(artifactPaths)
	s.updateAssistantEntryLocked(assistantID, strings.TrimSpace(output), false)
	if err != nil {
		if strings.TrimSpace(output) == "" {
			s.updateAssistantEntryLocked(assistantID, "agent error: "+err.Error(), false)
		} else {
			s.addEntryLocked(dashboardEntry{
				Type:      "system",
				Role:      "system",
				Text:      "agent error: " + err.Error(),
				HTML:      renderMarkdownHTML("agent error: " + err.Error()),
				CreatedAt: nowText(),
			})
		}
	}
	if len(fileCards) > 0 {
		s.addEntryLocked(dashboardEntry{
			Type:      "files",
			Role:      "system",
			Text:      "Generated or updated files",
			HTML:      renderMarkdownHTML("Generated or updated files"),
			Files:     fileCards,
			CreatedAt: nowText(),
		})
		s.mergeRecentFilesLocked(fileCards)
	}
	s.runArtifacts = nil
	s.busy = false
	s.mu.Unlock()
	s.broadcastState()
}

func (s *dashboardServer) snapshot() dashboardState {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := append([]dashboardEntry{}, s.entries...)
	recent := append([]dashboardFile{}, s.recentFiles...)
	return dashboardState{
		Version:         version,
		Session:         s.runtime.sessionName,
		SessionID:       s.runtime.sessionID,
		Model:           s.runtime.cfg.Model,
		ApprovalMode:    s.approver.Mode(),
		Busy:            s.busy,
		PendingApproval: cloneApproval(s.pendingApproval),
		Entries:         entries,
		RecentFiles:     recent,
	}
}

func (s *dashboardServer) broadcastState() {
	s.hub.Broadcast(dashboardEnvelope{
		Type: dashboardStateEvent,
		Data: s.snapshot(),
	})
}

func (s *dashboardServer) scheduleBroadcast() {
	s.tokenNotifyMu.Lock()
	if s.tokenScheduled {
		s.tokenNotifyMu.Unlock()
		return
	}
	s.tokenScheduled = true
	s.tokenNotifyMu.Unlock()

	time.AfterFunc(dashboardTokenDebounce, func() {
		s.tokenNotifyMu.Lock()
		s.tokenScheduled = false
		s.tokenNotifyMu.Unlock()
		s.broadcastState()
	})
}

func (s *dashboardServer) addToolEvent(event tools.ToolAuditEvent) {
	s.mu.Lock()
	s.recordArtifactPathsLocked(toolArtifactPaths(event))
	s.addEntryLocked(dashboardEntry{
		Type: "tool",
		Role: "system",
		Tool: &dashboardToolEvent{
			Tool:         strings.TrimSpace(event.Tool),
			Status:       strings.TrimSpace(event.Status),
			DurationMs:   event.DurationMs,
			ArgsPreview:  strings.TrimSpace(event.ArgsPreview),
			OutputSample: strings.TrimSpace(event.OutputSample),
			Error:        strings.TrimSpace(event.Error),
		},
		CreatedAt: nowText(),
	})
	s.mu.Unlock()
	s.broadcastState()
}

func (s *dashboardServer) appendAssistantToken(id, token string) {
	s.mu.Lock()
	for i := range s.entries {
		if s.entries[i].ID != id {
			continue
		}
		s.entries[i].Text += token
		s.entries[i].HTML = renderMarkdownHTML(s.entries[i].Text)
		break
	}
	s.mu.Unlock()
	s.scheduleBroadcast()
}

func (s *dashboardServer) addSystemEntry(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	s.addEntryLocked(dashboardEntry{
		Type:      "system",
		Role:      "system",
		Text:      text,
		HTML:      renderMarkdownHTML(text),
		CreatedAt: nowText(),
	})
	s.mu.Unlock()
	s.broadcastState()
}

func (s *dashboardServer) setPendingApproval(info *dashboardApproval) {
	s.mu.Lock()
	s.pendingApproval = cloneApproval(info)
	s.mu.Unlock()
	s.broadcastState()
}

func (s *dashboardServer) rebuildEntriesFromRuntime() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
	if s.runtime == nil || s.runtime.session == nil {
		return
	}
	for _, msg := range s.runtime.session.Messages() {
		switch strings.TrimSpace(msg.Role) {
		case "user", "assistant":
			text := model.ContentString(msg.Content)
			if strings.TrimSpace(text) == "" {
				continue
			}
			s.addEntryLocked(dashboardEntry{
				Type:      "message",
				Role:      strings.TrimSpace(msg.Role),
				Text:      formatRunOutput(text, "raw"),
				HTML:      renderMarkdownHTML(formatRunOutput(text, "raw")),
				CreatedAt: nowText(),
			})
		}
	}
}

func (s *dashboardServer) normalizeAttachmentFiles(items []string) ([]dashboardFile, error) {
	out := make([]dashboardFile, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		file, err := buildDashboardFile(s.runtime.cfg.WorkDir, item)
		if err != nil {
			return nil, err
		}
		out = append(out, file)
	}
	return out, nil
}

func (s *dashboardServer) saveUploadedFile(item *multipart.FileHeader) (dashboardFile, error) {
	src, err := item.Open()
	if err != nil {
		return dashboardFile{}, err
	}
	defer src.Close()

	name := filepath.Base(strings.TrimSpace(item.Filename))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "upload.bin"
	}
	uploadDir := filepath.Join(s.runtime.cfg.WorkDir, dashboardUploadSubdirName, time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return dashboardFile{}, err
	}
	dstPath := filepath.Join(uploadDir, name)
	dst, err := os.Create(dstPath)
	if err != nil {
		return dashboardFile{}, err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return dashboardFile{}, err
	}
	rel, err := filepath.Rel(s.runtime.cfg.WorkDir, dstPath)
	if err != nil {
		return dashboardFile{}, err
	}
	return buildDashboardFile(s.runtime.cfg.WorkDir, rel)
}

func (s *dashboardServer) buildFileCards(paths []string) []dashboardFile {
	out := make([]dashboardFile, 0, len(paths))
	for _, path := range paths {
		file, err := buildDashboardFile(s.runtime.cfg.WorkDir, path)
		if err != nil {
			continue
		}
		out = append(out, file)
	}
	return out
}

func (s *dashboardServer) resolveFileFromQuery(r *http.Request) (dashboardFile, string, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		return dashboardFile{}, "", fmt.Errorf("missing path")
	}
	abs, rel, err := resolveWorkspacePath(s.runtime.cfg.WorkDir, raw)
	if err != nil {
		return dashboardFile{}, "", err
	}
	file, err := buildDashboardFile(s.runtime.cfg.WorkDir, rel)
	if err != nil {
		return dashboardFile{}, "", err
	}
	return file, abs, nil
}

func (s *dashboardServer) addEntryLocked(entry dashboardEntry) string {
	s.nextEntryID++
	entry.ID = "entry-" + strconv.Itoa(s.nextEntryID)
	if strings.TrimSpace(entry.CreatedAt) == "" {
		entry.CreatedAt = nowText()
	}
	s.entries = append(s.entries, entry)
	return entry.ID
}

func (s *dashboardServer) updateAssistantEntryLocked(id, text string, pending bool) {
	for i := range s.entries {
		if s.entries[i].ID != id {
			continue
		}
		s.entries[i].Text = strings.TrimSpace(text)
		s.entries[i].HTML = renderMarkdownHTML(s.entries[i].Text)
		s.entries[i].Pending = pending
		return
	}
}

func (s *dashboardServer) mergeRecentFilesLocked(items []dashboardFile) {
	if len(items) == 0 {
		return
	}
	byPath := make(map[string]dashboardFile, len(s.recentFiles)+len(items))
	for _, item := range s.recentFiles {
		byPath[item.Path] = item
	}
	for _, item := range items {
		byPath[item.Path] = item
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		left := byPath[paths[i]]
		right := byPath[paths[j]]
		if left.ModifiedAt == right.ModifiedAt {
			return left.Path < right.Path
		}
		return left.ModifiedAt > right.ModifiedAt
	})
	merged := make([]dashboardFile, 0, len(paths))
	for _, path := range paths {
		merged = append(merged, byPath[path])
	}
	if len(merged) > maxDashboardRecentFiles {
		merged = merged[:maxDashboardRecentFiles]
	}
	s.recentFiles = merged
}

func (s *dashboardServer) recordArtifactPathsLocked(paths []string) {
	if len(paths) == 0 {
		return
	}
	if s.runArtifacts == nil {
		s.runArtifacts = make(map[string]struct{}, len(paths))
	}
	for _, path := range paths {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" {
			continue
		}
		s.runArtifacts[path] = struct{}{}
	}
}

func captureWorkspaceSnapshot(root string) (map[string]workspaceFileState, error) {
	root = filepath.Clean(root)
	snap := make(map[string]workspaceFileState)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch rel {
			case ".git", ".tacli":
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		snap[rel] = workspaceFileState{
			Size:    info.Size(),
			ModUnix: info.ModTime().UnixNano(),
		}
		return nil
	})
	return snap, err
}

func createdWorkspaceFiles(before, after map[string]workspaceFileState) []string {
	if len(after) == 0 {
		return nil
	}
	type pair struct {
		path string
		mod  int64
	}
	var items []pair
	for path, now := range after {
		if _, ok := before[path]; !ok {
			items = append(items, pair{path: path, mod: now.ModUnix})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].mod == items[j].mod {
			return items[i].path < items[j].path
		}
		return items[i].mod > items[j].mod
	})
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.path)
	}
	return out
}

func collectDashboardArtifactPaths(explicit map[string]struct{}, created []string) []string {
	seen := make(map[string]struct{}, len(explicit)+len(created))
	out := make([]string, 0, len(explicit)+len(created))
	for path := range explicit {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	for _, path := range created {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	if len(out) > maxDashboardArtifactFiles {
		out = out[:maxDashboardArtifactFiles]
	}
	return out
}

func toolArtifactPaths(event tools.ToolAuditEvent) []string {
	if strings.TrimSpace(event.Status) != "ok" {
		return nil
	}
	switch strings.TrimSpace(event.Tool) {
	case "write_file", "edit_file":
	default:
		return nil
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(event.InputJSON), &payload); err != nil {
		return nil
	}
	path := strings.TrimSpace(filepath.ToSlash(payload.Path))
	if path == "" {
		return nil
	}
	return []string{path}
}

func buildDashboardTask(text string, attachments []dashboardFile) string {
	text = strings.TrimSpace(text)
	if len(attachments) == 0 {
		return text
	}
	lines := []string{
		"Attached files are available in the workspace:",
	}
	for _, item := range attachments {
		lines = append(lines, "- "+item.Path)
	}
	lines = append(lines, "Use direct file-reading or document inspection tools when you need the actual contents.")
	if text != "" {
		lines = append(lines, "", text)
	}
	return strings.Join(lines, "\n")
}

func buildApprovalPreview(content string) string {
	preview := strings.TrimSpace(content)
	if preview == "" {
		return "(empty file)"
	}
	if len(preview) > 160 {
		preview = preview[:160] + "..."
	}
	return strings.ReplaceAll(preview, "\n", "\\n")
}

func buildDashboardFile(root, rel string) (dashboardFile, error) {
	abs, normalized, err := resolveWorkspacePath(root, rel)
	if err != nil {
		return dashboardFile{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return dashboardFile{}, err
	}
	isText := false
	if info.Mode().IsRegular() {
		isText = fileLooksText(abs)
	}
	file := dashboardFile{
		Path:        normalized,
		Name:        filepath.Base(normalized),
		Size:        info.Size(),
		ModifiedAt:  info.ModTime().UTC().Format(time.RFC3339),
		IsText:      isText,
		DownloadURL: "/api/download?path=" + normalized,
	}
	if isText {
		file.ViewURL = "/api/file?path=" + normalized
	}
	return file, nil
}

func resolveWorkspacePath(root, raw string) (string, string, error) {
	root = filepath.Clean(root)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("missing path")
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") || rel == ".." {
		return "", "", fmt.Errorf("path escapes workspace")
	}
	return candidate, rel, nil
}

func fileLooksText(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if len(buf) == 0 {
		return true
	}
	if bytes.IndexByte(buf, 0) >= 0 {
		return false
	}
	if utf8.Valid(buf) {
		return true
	}
	printable := 0
	for _, b := range buf {
		if b == '\n' || b == '\r' || b == '\t' || (b >= 32 && b < 127) {
			printable++
		}
	}
	return printable*100/len(buf) >= 90
}

func renderMarkdownHTML(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := dashboardMarkdown.Convert([]byte(text), &buf); err != nil {
		return "<p>" + html.EscapeString(text) + "</p>"
	}
	return string(dashboardSanitizer.SanitizeBytes(buf.Bytes()))
}

func cloneApproval(info *dashboardApproval) *dashboardApproval {
	if info == nil {
		return nil
	}
	copy := *info
	return &copy
}

func nowText() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeSSE(w http.ResponseWriter, eventType string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\n", strings.TrimSpace(eventType))
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}
