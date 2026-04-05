package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/tools"
)

type jobStatus string

const (
	jobQueued                  jobStatus = "queued"
	jobRunning                 jobStatus = "running"
	jobReady                   jobStatus = "ready"
	jobFailed                  jobStatus = "failed"
	jobCanceled                jobStatus = "canceled"
	backgroundRoleGeneral                = "general"
	backgroundRoleExplore                = "explore"
	backgroundRolePlan                   = "plan"
	backgroundRoleImplement              = "implement"
	backgroundRoleVerify                 = "verify"
	maxActiveBackgroundJobs              = 2
	minBackgroundTaskChars               = 24
	minBackgroundTaskWordCount           = 4
)

type jobSnapshot struct {
	ID         string
	Status     jobStatus
	Role       string
	Model      string
	TaskCount  int
	Queued     int
	LastPrompt string
	LastOutput string
	LastError  string
	LogTail    string
	Session    agent.SubagentSessionState
	Summary    jobSummary
	Applied    bool
	CreatedAt  time.Time
	StartedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt time.Time
}

func (s jobSnapshot) toToolSnapshot() tools.BackgroundJobSnapshot {
	return tools.BackgroundJobSnapshot{
		ID:         s.ID,
		Status:     string(s.Status),
		Role:       s.Role,
		Model:      s.Model,
		TaskCount:  s.TaskCount,
		Queued:     s.Queued,
		LastPrompt: s.LastPrompt,
		LastOutput: s.LastOutput,
		LastError:  s.LastError,
		LogTail:    s.LogTail,
	}
}

type backgroundJob struct {
	id      string
	role    string
	model   string
	session *agent.Session
	cancel  context.CancelFunc
	prompts chan string

	mu         sync.RWMutex
	status     jobStatus
	taskCount  int
	queued     int
	lastPrompt string
	lastOutput string
	lastError  string
	logTail    string
	summary    jobSummary
	applied    bool
	createdAt  time.Time
	startedAt  time.Time
	updatedAt  time.Time
	finishedAt time.Time
}

type jobSummary struct {
	Findings  []string
	Files     []string
	Risks     []string
	NextSteps []string
}

func (j *backgroundJob) snapshot() jobSnapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return jobSnapshot{
		ID:         j.id,
		Status:     j.status,
		Role:       j.role,
		Model:      j.model,
		TaskCount:  j.taskCount,
		Queued:     j.queued,
		LastPrompt: j.lastPrompt,
		LastOutput: j.lastOutput,
		LastError:  j.lastError,
		LogTail:    j.logTail,
		Session:    agent.SnapshotSession(j.session),
		Summary:    j.summary,
		Applied:    j.applied,
		CreatedAt:  j.createdAt,
		StartedAt:  j.startedAt,
		UpdatedAt:  j.updatedAt,
		FinishedAt: j.finishedAt,
	}
}

type jobManager struct {
	cfg           config.Config
	memory        string
	notifier      func(string)
	router        backgroundRoleRouter
	orchestration *agent.OrchestrationRegistry

	mu     sync.RWMutex
	nextID int
	jobs   map[string]*backgroundJob
}

func newJobManager(cfg config.Config, memoryText string) *jobManager {
	return &jobManager{
		cfg:           cfg,
		memory:        memoryText,
		router:        keywordBackgroundRoleRouter(),
		orchestration: agent.NewOrchestrationRegistry(),
		jobs:          make(map[string]*backgroundJob),
	}
}

func (m *jobManager) SetNotifier(fn func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifier = fn
}

func (m *jobManager) UpdateConfig(cfg config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
}

func (m *jobManager) UpdateMemory(memoryText string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.memory = memoryText
}

func (m *jobManager) SetRoleRouter(router backgroundRoleRouter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if router == nil {
		m.router = keywordBackgroundRoleRouter()
		return
	}
	m.router = router
}

func (m *jobManager) Start(task string) (string, error) {
	role := routeBackgroundRole(task)
	m.mu.RLock()
	router := m.router
	cfg := m.cfg
	m.mu.RUnlock()

	if router != nil {
		timeout := roleRoutingTimeout(cfg.ModelTimeout)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		routed, err := router(ctx, task)
		if err == nil {
			routed = normalizeBackgroundRole(routed)
			if validateBackgroundRole(routed) == nil {
				role = routed
			}
		}
	}

	return m.StartWithRole(role, task)
}

func (m *jobManager) StartWithRole(role, task string) (string, error) {
	if strings.TrimSpace(role) == "" {
		role = routeBackgroundRole(task)
	}
	role = normalizeBackgroundRole(role)
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("usage: /bg <task>")
	}
	if m.cfg.ApprovalMode != tools.ApprovalDangerously {
		return "", fmt.Errorf("background jobs require dangerously mode so they can run without blocking on interactive approval")
	}
	if err := validateBackgroundRole(role); err != nil {
		return "", err
	}
	if err := validateBackgroundTask(task); err != nil {
		return "", err
	}

	m.mu.Lock()
	if m.activeJobsLocked() >= maxActiveBackgroundJobs {
		m.mu.Unlock()
		return "", fmt.Errorf("too many active background jobs; wait for one to finish before starting another")
	}
	m.nextID++
	id := fmt.Sprintf("job-%03d", m.nextID)
	bgCfg := m.cfg
	memoryText := m.memory
	bgCfg.ApprovalMode = tools.ApprovalDangerously
	bgApprover := tools.NewTerminalApprover(nil, io.Discard, bgCfg.ApprovalMode, false)
	logWriter := &backgroundLogWriter{manager: m, jobID: id}
	loop := buildAgentWith(bgCfg, bgApprover, logWriter, nil, loadRuntimePolicy(bgCfg))
	job := &backgroundJob{
		id:        id,
		role:      role,
		model:     bgCfg.Model,
		session:   loop.NewSessionWithPrompt(promptContextFor(bgCfg, loop, "background:"+role, memoryText)),
		status:    jobQueued,
		prompts:   make(chan string, 32),
		createdAt: time.Now(),
		updatedAt: time.Now(),
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel
	m.jobs[id] = job
	if m.orchestration != nil {
		m.orchestration.Register(agent.SubagentSnapshot{
			ID:        id,
			Status:    string(jobQueued),
			Role:      role,
			Model:     bgCfg.Model,
			Session:   agent.SnapshotSession(job.session),
			CreatedAt: job.createdAt,
			UpdatedAt: job.updatedAt,
		}, cancel)
	}
	m.mu.Unlock()

	go m.run(workerCtx, job)
	if err := m.enqueue(job, task); err != nil {
		return "", err
	}
	return id, nil
}

func (m *jobManager) Send(id, task string) error {
	task = strings.TrimSpace(task)
	if task == "" {
		return fmt.Errorf("usage: /job-send <id> <message>")
	}
	job, ok := m.get(id)
	if !ok {
		return fmt.Errorf("unknown job %q", id)
	}
	if snap := job.snapshot(); snap.Status == jobCanceled {
		return fmt.Errorf("job %s is canceled", id)
	}
	return m.enqueue(job, task)
}

func (m *jobManager) Cancel(id string) error {
	job, ok := m.get(id)
	if !ok {
		return fmt.Errorf("unknown job %q", id)
	}
	job.mu.Lock()
	if job.status == jobCanceled {
		job.mu.Unlock()
		return nil
	}
	job.status = jobCanceled
	job.updatedAt = time.Now()
	job.finishedAt = job.updatedAt
	job.mu.Unlock()
	job.cancel()
	m.syncOrchestration(job)
	m.notify(fmt.Sprintf("%s canceled", id))
	return nil
}

func (m *jobManager) List() []jobSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]jobSnapshot, 0, len(m.jobs))
	for _, job := range m.jobs {
		out = append(out, job.snapshot())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (m *jobManager) ToolList() []tools.BackgroundJobSnapshot {
	snaps := m.List()
	out := make([]tools.BackgroundJobSnapshot, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, snap.toToolSnapshot())
	}
	return out
}

func (m *jobManager) Snapshot(id string) (jobSnapshot, bool) {
	job, ok := m.get(id)
	if !ok {
		return jobSnapshot{}, false
	}
	return job.snapshot(), true
}

func (m *jobManager) ToolSnapshot(id string) (tools.BackgroundJobSnapshot, bool) {
	snap, ok := m.Snapshot(id)
	if !ok {
		return tools.BackgroundJobSnapshot{}, false
	}
	return snap.toToolSnapshot(), true
}

func (m *jobManager) CollectReadyForApply() []jobSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []jobSnapshot
	for _, job := range m.jobs {
		snap := job.snapshot()
		if snap.Status == jobReady && !snap.Applied {
			out = append(out, snap)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.Before(out[j].UpdatedAt)
	})
	return out
}

func (m *jobManager) MarkApplied(id string) {
	job, ok := m.get(id)
	if !ok {
		return
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	job.applied = true
	job.updatedAt = time.Now()
}

func (m *jobManager) Summary() string {
	snaps := m.List()
	if len(snaps) == 0 {
		return "jobs=0"
	}
	var running, queued int
	for _, snap := range snaps {
		switch snap.Status {
		case jobRunning:
			running++
		case jobQueued:
			queued++
		}
	}
	return fmt.Sprintf("jobs=%d running=%d queued=%d", len(snaps), running, queued)
}

func (m *jobManager) ClearFinished() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	for id, job := range m.jobs {
		switch job.snapshot().Status {
		case jobReady, jobFailed, jobCanceled:
			delete(m.jobs, id)
			removed++
		}
	}
	return removed
}

func (m *jobManager) Export() json.RawMessage {
	snaps := m.List()
	data, err := json.Marshal(snaps)
	if err != nil {
		return nil
	}
	return data
}

func (m *jobManager) Restore(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var snaps []jobSnapshot
	if err := json.Unmarshal(raw, &snaps); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, snap := range snaps {
		job := &backgroundJob{
			id:         snap.ID,
			role:       normalizeBackgroundRole(snap.Role),
			model:      snap.Model,
			status:     snap.Status,
			taskCount:  snap.TaskCount,
			queued:     0,
			lastPrompt: snap.LastPrompt,
			lastOutput: snap.LastOutput,
			lastError:  snap.LastError,
			logTail:    snap.LogTail,
			summary:    snap.Summary,
			applied:    snap.Applied,
			createdAt:  snap.CreatedAt,
			startedAt:  snap.StartedAt,
			updatedAt:  snap.UpdatedAt,
			finishedAt: snap.FinishedAt,
		}
		if job.status == jobQueued || job.status == jobRunning {
			job.status = jobFailed
			job.lastError = "restored from saved state; original background worker is no longer running"
			if job.finishedAt.IsZero() {
				job.finishedAt = time.Now()
			}
		}
		m.jobs[job.id] = job
		if m.orchestration != nil {
			m.orchestration.Register(agent.SubagentSnapshot{
				ID:         job.id,
				Status:     string(job.status),
				Role:       job.role,
				Model:      job.model,
				TaskCount:  job.taskCount,
				Queued:     job.queued,
				LastPrompt: job.lastPrompt,
				LastOutput: job.lastOutput,
				LastError:  job.lastError,
				LogTail:    job.logTail,
				Session:    snap.Session,
				CreatedAt:  job.createdAt,
				StartedAt:  job.startedAt,
				UpdatedAt:  job.updatedAt,
				FinishedAt: job.finishedAt,
			}, nil)
		}
		if n := parseJobOrdinal(job.id); n > m.nextID {
			m.nextID = n
		}
	}
	return nil
}

func (m *jobManager) syncOrchestration(job *backgroundJob) {
	if m == nil || m.orchestration == nil || job == nil {
		return
	}
	snap := job.snapshot()
	m.orchestration.Update(agent.SubagentSnapshot{
		ID:         snap.ID,
		Status:     string(snap.Status),
		Role:       snap.Role,
		Model:      snap.Model,
		TaskCount:  snap.TaskCount,
		Queued:     snap.Queued,
		LastPrompt: snap.LastPrompt,
		LastOutput: snap.LastOutput,
		LastError:  snap.LastError,
		LogTail:    snap.LogTail,
		Session:    snap.Session,
		CreatedAt:  snap.CreatedAt,
		StartedAt:  snap.StartedAt,
		UpdatedAt:  snap.UpdatedAt,
		FinishedAt: snap.FinishedAt,
	})
}

func (m *jobManager) activeJobsLocked() int {
	active := 0
	for _, job := range m.jobs {
		switch job.snapshot().Status {
		case jobQueued, jobRunning:
			active++
		}
	}
	return active
}

func (m *jobManager) enqueue(job *backgroundJob, task string) error {
	job.mu.Lock()
	job.queued++
	job.updatedAt = time.Now()
	if job.status != jobRunning {
		job.status = jobQueued
	}
	job.mu.Unlock()

	select {
	case job.prompts <- task:
		m.notify(fmt.Sprintf("%s queued: %s", job.id, compactJobText(task, 80)))
		return nil
	default:
		job.mu.Lock()
		job.queued--
		job.mu.Unlock()
		return fmt.Errorf("job %s queue is full", job.id)
	}
}

func (m *jobManager) run(ctx context.Context, job *backgroundJob) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-job.prompts:
			job.mu.Lock()
			job.queued--
			job.status = jobRunning
			job.taskCount++
			job.lastPrompt = task
			now := time.Now()
			if job.startedAt.IsZero() {
				job.startedAt = now
			}
			job.updatedAt = now
			job.mu.Unlock()
			m.syncOrchestration(job)

			m.notify(fmt.Sprintf("%s (%s) running: %s", job.id, job.role, compactJobText(task, 80)))

			result, err := job.session.RunTask(ctx, task)

			job.mu.Lock()
			job.updatedAt = time.Now()
			if err != nil {
				if ctx.Err() != nil {
					job.status = jobCanceled
					job.lastError = ctx.Err().Error()
					job.finishedAt = job.updatedAt
					job.mu.Unlock()
					m.syncOrchestration(job)
					m.notify(fmt.Sprintf("%s canceled", job.id))
					return
				}
				job.status = jobFailed
				job.lastError = err.Error()
				job.finishedAt = job.updatedAt
				job.mu.Unlock()
				m.syncOrchestration(job)
				m.notify(fmt.Sprintf("%s (%s) failed: %s", job.id, job.role, compactJobText(err.Error(), 120)))
				continue
			}

			job.lastOutput = formatRunOutput(result.Final, "terminal")
			job.lastError = ""
			job.summary = summarizeBackgroundResult(job.lastOutput)
			job.finishedAt = job.updatedAt
			if job.queued > 0 {
				job.status = jobQueued
			} else {
				job.status = jobReady
			}
			job.mu.Unlock()
			m.syncOrchestration(job)

			m.notify(fmt.Sprintf("%s (%s) ready: %s", job.id, job.role, compactJobText(job.snapshot().LastOutput, 120)))
		}
	}
}

func (m *jobManager) appendLog(jobID, line string) {
	job, ok := m.get(jobID)
	if !ok {
		return
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.logTail == "" {
		job.logTail = strings.TrimSpace(line)
	} else {
		job.logTail = strings.TrimSpace(job.logTail + "\n" + strings.TrimSpace(line))
	}
	lines := strings.Split(job.logTail, "\n")
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	job.logTail = strings.Join(lines, "\n")
	job.updatedAt = time.Now()
	jobSnap := job.snapshot()
	if m.orchestration != nil {
		m.orchestration.Update(agent.SubagentSnapshot{
			ID:         jobSnap.ID,
			Status:     string(jobSnap.Status),
			Role:       jobSnap.Role,
			Model:      jobSnap.Model,
			TaskCount:  jobSnap.TaskCount,
			Queued:     jobSnap.Queued,
			LastPrompt: jobSnap.LastPrompt,
			LastOutput: jobSnap.LastOutput,
			LastError:  jobSnap.LastError,
			LogTail:    jobSnap.LogTail,
			Session:    jobSnap.Session,
			CreatedAt:  jobSnap.CreatedAt,
			StartedAt:  jobSnap.StartedAt,
			UpdatedAt:  jobSnap.UpdatedAt,
			FinishedAt: jobSnap.FinishedAt,
		})
	}
}

func (m *jobManager) notify(text string) {
	m.mu.RLock()
	fn := m.notifier
	m.mu.RUnlock()
	if fn != nil && strings.TrimSpace(text) != "" {
		fn(strings.TrimSpace(text))
	}
}

func (m *jobManager) get(id string) (*backgroundJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[strings.TrimSpace(id)]
	return job, ok
}

type backgroundLogWriter struct {
	manager *jobManager
	jobID   string
}

func (w *backgroundLogWriter) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(p))
	if text == "" {
		return len(p), nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			w.manager.appendLog(w.jobID, line)
		}
	}
	return len(p), nil
}

func formatJobList(snaps []jobSnapshot) string {
	if len(snaps) == 0 {
		return "no background jobs"
	}
	lines := make([]string, 0, len(snaps))
	for _, snap := range snaps {
		line := fmt.Sprintf("%s  %s  tasks=%d", snap.ID, snap.Status, snap.TaskCount)
		line += "  role=" + normalizeBackgroundRole(snap.Role)
		if snap.Queued > 0 {
			line += fmt.Sprintf("  queued=%d", snap.Queued)
		}
		if snap.Model != "" {
			line += "  model=" + snap.Model
		}
		if strings.TrimSpace(snap.LastPrompt) != "" {
			line += "\n  last_prompt: " + compactJobText(snap.LastPrompt, 120)
		}
		if strings.TrimSpace(snap.LastError) != "" {
			line += "\n  last_error: " + compactJobText(snap.LastError, 120)
		} else if strings.TrimSpace(snap.LastOutput) != "" {
			line += "\n  last_output: " + compactJobText(tools.SingleLineText(snap.LastOutput), 120)
		}
		if snap.Session.MessageCount > 0 {
			line += fmt.Sprintf("\n  session_messages: %d", snap.Session.MessageCount)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n\n")
}

func formatJobSnapshot(snap jobSnapshot) string {
	lines := []string{
		"id=" + snap.ID,
		"status=" + string(snap.Status),
		"role=" + normalizeBackgroundRole(snap.Role),
		"model=" + snap.Model,
		fmt.Sprintf("tasks=%d", snap.TaskCount),
		fmt.Sprintf("queued=%d", snap.Queued),
		"created=" + snap.CreatedAt.Format(time.RFC3339),
		"updated=" + snap.UpdatedAt.Format(time.RFC3339),
	}
	if !snap.StartedAt.IsZero() {
		lines = append(lines, "started="+snap.StartedAt.Format(time.RFC3339))
	}
	if !snap.FinishedAt.IsZero() {
		lines = append(lines, "finished="+snap.FinishedAt.Format(time.RFC3339))
	}
	if strings.TrimSpace(snap.LastPrompt) != "" {
		lines = append(lines, "last_prompt="+compactJobText(snap.LastPrompt, 240))
	}
	if strings.TrimSpace(snap.LastError) != "" {
		lines = append(lines, "last_error="+compactJobText(snap.LastError, 240))
	}
	if strings.TrimSpace(snap.LastOutput) != "" {
		lines = append(lines, "last_output="+compactJobText(tools.SingleLineText(snap.LastOutput), 240))
	}
	if snap.Session.MessageCount > 0 {
		lines = append(lines, fmt.Sprintf("session_messages=%d", snap.Session.MessageCount))
	}
	if summary := renderJobSummary(snap.Summary); summary != "" {
		lines = append(lines, "summary:\n"+summary)
	}
	if strings.TrimSpace(snap.LogTail) != "" {
		lines = append(lines, "log_tail:\n"+snap.LogTail)
	}
	return strings.Join(lines, "\n")
}

func summarizeJobForSession(snap jobSnapshot) string {
	lines := []string{
		fmt.Sprintf("[background job %s]", snap.ID),
		"status: " + string(snap.Status),
		"role: " + normalizeBackgroundRole(snap.Role),
	}
	if strings.TrimSpace(snap.LastPrompt) != "" {
		lines = append(lines, "last prompt: "+compactJobText(snap.LastPrompt, 240))
	}
	if strings.TrimSpace(snap.LastError) != "" {
		lines = append(lines, "last error: "+compactJobText(snap.LastError, 240))
	}
	if strings.TrimSpace(snap.LastOutput) != "" {
		if summary := renderJobSummary(snap.Summary); summary != "" {
			lines = append(lines, summary)
		} else {
			lines = append(lines, "result: "+compactJobText(tools.SingleLineText(snap.LastOutput), 400))
		}
	}
	if strings.TrimSpace(snap.LogTail) != "" {
		lines = append(lines, "activity: "+compactJobText(tools.SingleLineText(snap.LogTail), 300))
	}
	return strings.Join(lines, "\n")
}

type jobToolAdapter struct {
	manager *jobManager
}

func (a jobToolAdapter) Start(task string) (string, error) {
	return a.manager.Start(task)
}

func (a jobToolAdapter) StartWithRole(role, task string) (string, error) {
	return a.manager.StartWithRole(role, task)
}

func (a jobToolAdapter) Send(id, task string) error {
	return a.manager.Send(id, task)
}

func (a jobToolAdapter) Cancel(id string) error {
	return a.manager.Cancel(id)
}

func (a jobToolAdapter) List() []tools.BackgroundJobSnapshot {
	return a.manager.ToolList()
}

func (a jobToolAdapter) Snapshot(id string) (tools.BackgroundJobSnapshot, bool) {
	return a.manager.ToolSnapshot(id)
}

func compactJobText(text string, limit int) string {
	text = tools.SingleLineText(text)
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

func parseJobOrdinal(id string) int {
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(id), "job-%d", &n)
	return n
}

func summarizeBackgroundResult(text string) jobSummary {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var summary jobSummary
	section := ""
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch classifySummarySection(line) {
		case "findings", "files", "risks", "next_steps":
			section = classifySummarySection(line)
			continue
		}
		item := trimSummaryItem(line)
		if item == "" {
			continue
		}
		switch section {
		case "findings":
			summary.Findings = append(summary.Findings, item)
		case "files":
			summary.Files = append(summary.Files, item)
		case "risks":
			summary.Risks = append(summary.Risks, item)
		case "next_steps":
			summary.NextSteps = append(summary.NextSteps, item)
		}
	}
	if isEmptyJobSummary(summary) {
		summary.Findings = fallbackSummaryItems(text, 3)
	}
	return summary
}

func classifySummarySection(line string) string {
	lower := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(line, ":")))
	switch lower {
	case "key findings", "findings", "主要发现", "发现":
		return "findings"
	case "relevant files", "files", "相关文件", "文件":
		return "files"
	case "risks", "unknowns", "risks or unknowns", "风险", "风险或未知点":
		return "risks"
	case "next steps", "recommended next steps", "next step", "下一步", "建议下一步":
		return "next_steps"
	default:
		return ""
	}
}

func trimSummaryItem(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-*• \t")
	line = strings.TrimLeft(line, "0123456789.")
	return strings.TrimSpace(line)
}

func isEmptyJobSummary(summary jobSummary) bool {
	return len(summary.Findings) == 0 && len(summary.Files) == 0 && len(summary.Risks) == 0 && len(summary.NextSteps) == 0
}

func fallbackSummaryItems(text string, maxItems int) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = trimSummaryItem(line)
		if line == "" {
			continue
		}
		out = append(out, compactJobText(line, 160))
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func renderJobSummary(summary jobSummary) string {
	var parts []string
	if len(summary.Findings) > 0 {
		parts = append(parts, renderJobSummarySection("findings", summary.Findings, 3))
	}
	if len(summary.Files) > 0 {
		parts = append(parts, renderJobSummarySection("files", summary.Files, 4))
	}
	if len(summary.Risks) > 0 {
		parts = append(parts, renderJobSummarySection("risks", summary.Risks, 3))
	}
	if len(summary.NextSteps) > 0 {
		parts = append(parts, renderJobSummarySection("next_steps", summary.NextSteps, 3))
	}
	return strings.Join(parts, "\n")
}

func renderJobSummarySection(name string, items []string, limit int) string {
	if len(items) == 0 {
		return ""
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	lines := []string{name + ":"}
	for _, item := range items {
		lines = append(lines, "- "+compactJobText(item, 160))
	}
	return strings.Join(lines, "\n")
}

func validateBackgroundTask(task string) error {
	task = strings.TrimSpace(task)
	if len(task) < minBackgroundTaskChars {
		return fmt.Errorf("background task is too short; keep small work in the main conversation")
	}
	if len(strings.Fields(task)) < minBackgroundTaskWordCount {
		return fmt.Errorf("background task is too vague; describe the subtask more clearly before delegating")
	}
	lower := strings.ToLower(task)
	tooSmall := []string{
		"download it",
		"look at it",
		"check it",
		"fix it",
		"do it",
	}
	for _, phrase := range tooSmall {
		if lower == phrase {
			return fmt.Errorf("background task is too vague; describe the subtask more clearly before delegating")
		}
	}
	return nil
}

func normalizeBackgroundRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return backgroundRoleGeneral
	}
	return role
}

func validateBackgroundRole(role string) error {
	switch normalizeBackgroundRole(role) {
	case backgroundRoleGeneral, backgroundRoleExplore, backgroundRolePlan, backgroundRoleImplement, backgroundRoleVerify:
		return nil
	default:
		return fmt.Errorf("invalid background role %q (expected general|explore|plan|implement|verify)", strings.TrimSpace(role))
	}
}

func routeBackgroundRole(task string) string {
	lower := strings.ToLower(strings.TrimSpace(task))
	if lower == "" {
		return backgroundRoleGeneral
	}

	if hasAny(lower,
		"verify", "verification", "validate", "validation", "regression", "prove", "evidence",
		"test", "tests", "go test", "type-check", "typecheck", "build",
		"验证", "校验", "验收", "回归", "证据", "测试", "构建", "编译",
	) {
		return backgroundRoleVerify
	}
	if hasAny(lower,
		"plan", "roadmap", "breakdown", "steps", "strategy", "design",
		"计划", "路线图", "拆解", "步骤", "方案", "设计",
	) {
		return backgroundRolePlan
	}
	if hasAny(lower,
		"implement", "implementation", "fix", "patch", "refactor", "rewrite", "add feature", "code",
		"实现", "修复", "补丁", "重构", "改代码", "开发",
	) {
		return backgroundRoleImplement
	}
	if hasAny(lower,
		"explore", "inspect", "analyze", "analysis", "review", "investigate", "map", "understand",
		"read-only", "read only", "risk", "architecture", "flow", "dependency",
		"探索", "检查", "分析", "审查", "排查", "梳理", "了解", "只读", "风险", "架构", "流程", "依赖",
	) {
		return backgroundRoleExplore
	}
	return backgroundRoleGeneral
}
