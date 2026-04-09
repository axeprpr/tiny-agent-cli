package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"tiny-agent-cli/internal/i18n"
)

const (
	ApprovalConfirm     = "confirm"
	ApprovalDangerously = "dangerously"
)

type Approver interface {
	ApproveCommand(ctx context.Context, command string) (bool, error)
	ApproveWrite(ctx context.Context, path, content string) (bool, error)
	Mode() string
	SetMode(mode string) error
}

type TerminalApprover struct {
	mu          sync.Mutex
	reader      *bufio.Reader
	writer      io.Writer
	mode        string
	interactive bool
	allowedCmds map[string]bool
	allowedOps  map[string]bool
}

func NewTerminalApprover(reader *bufio.Reader, writer io.Writer, mode string, interactive bool) *TerminalApprover {
	return &TerminalApprover{
		reader:      reader,
		writer:      writer,
		mode:        normalizeApprovalMode(mode),
		interactive: interactive,
		allowedCmds: make(map[string]bool),
		allowedOps:  make(map[string]bool),
	}
}

func (a *TerminalApprover) Mode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

func (a *TerminalApprover) SetMode(mode string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	mode = normalizeApprovalMode(mode)
	switch mode {
	case PermissionModePrompt, PermissionModeReadOnly, PermissionModeWorkspaceWrite, PermissionModeDangerFullAccess, PermissionModeAllow:
		a.mode = mode
		return nil
	default:
		return fmt.Errorf("invalid approval mode %q", mode)
	}
}

func (a *TerminalApprover) ApproveCommand(_ context.Context, command string) (bool, error) {
	command = strings.TrimSpace(command)
	a.mu.Lock()
	if a.mode == PermissionModeDangerFullAccess || a.mode == PermissionModeAllow || a.allowedCmds[command] {
		a.mu.Unlock()
		return true, nil
	}
	interactive := a.interactive
	reader := a.reader
	writer := a.writer
	a.mu.Unlock()
	if !interactive || reader == nil {
		return false, fmt.Errorf("%s", i18n.T("approval.cmd.need.tty"))
	}

	for {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, i18n.T("approval.cmd.header"))
		fmt.Fprintf(writer, "  %s\n", command)
		fmt.Fprint(writer, i18n.T("approval.cmd.prompt"))

		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return false, err
		}

		answer := strings.ToLower(strings.TrimSpace(line))
		switch answer {
		case "y", "yes":
			a.mu.Lock()
			a.allowedCmds[command] = true
			a.mu.Unlock()
			return true, nil
		case "n", "no", "":
			return false, nil
		case "a", "always", "dangerously":
			a.mu.Lock()
			a.mode = PermissionModeDangerFullAccess
			a.mu.Unlock()
			fmt.Fprintln(writer, i18n.T("approval.switched"))
			return true, nil
		default:
			fmt.Fprintln(writer, i18n.T("approval.invalid"))
		}

		if err == io.EOF {
			return false, nil
		}
	}
}

func (a *TerminalApprover) ApproveWrite(_ context.Context, path, content string) (bool, error) {
	path = strings.TrimSpace(path)
	opKey := WriteApprovalKey(path, content)
	a.mu.Lock()
	if a.mode == PermissionModeDangerFullAccess || a.mode == PermissionModeAllow || a.allowedOps[opKey] {
		a.mu.Unlock()
		return true, nil
	}
	interactive := a.interactive
	reader := a.reader
	writer := a.writer
	a.mu.Unlock()
	if !interactive || reader == nil {
		return false, fmt.Errorf("%s", i18n.T("approval.write.need.tty"))
	}

	preview := strings.TrimSpace(content)
	if preview == "" {
		preview = "(empty file)"
	}
	if len(preview) > 160 {
		preview = preview[:160] + "..."
	}
	preview = strings.ReplaceAll(preview, "\n", "\\n")

	for {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, i18n.T("approval.write.header"))
		fmt.Fprintf(writer, "  path: %s\n", path)
		fmt.Fprintf(writer, "  bytes: %d\n", len(content))
		fmt.Fprintf(writer, "  preview: %s\n", preview)
		fmt.Fprint(writer, i18n.T("approval.write.prompt"))

		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return false, err
		}

		answer := strings.ToLower(strings.TrimSpace(line))
		switch answer {
		case "y", "yes":
			a.mu.Lock()
			a.allowedOps[opKey] = true
			a.mu.Unlock()
			return true, nil
		case "n", "no", "":
			return false, nil
		case "a", "always", "dangerously":
			a.mu.Lock()
			a.mode = PermissionModeDangerFullAccess
			a.mu.Unlock()
			fmt.Fprintln(writer, i18n.T("approval.switched"))
			return true, nil
		default:
			fmt.Fprintln(writer, i18n.T("approval.invalid"))
		}

		if err == io.EOF {
			return false, nil
		}
	}
}

func WriteApprovalKey(path, content string) string {
	h := sha256.Sum256([]byte(content))
	return path + "\x00" + fmt.Sprintf("%x", h[:])
}

func normalizeApprovalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ApprovalConfirm, PermissionModePrompt:
		return PermissionModePrompt
	case PermissionModeReadOnly:
		return PermissionModeReadOnly
	case PermissionModeWorkspaceWrite:
		return PermissionModeWorkspaceWrite
	case ApprovalDangerously, PermissionModeDangerFullAccess:
		return PermissionModeDangerFullAccess
	case PermissionModeAllow:
		return PermissionModeAllow
	default:
		return mode
	}
}

func IsInteractiveTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
