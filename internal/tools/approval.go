package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
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
	reader      *bufio.Reader
	writer      io.Writer
	mode        string
	interactive bool
}

func NewTerminalApprover(reader *bufio.Reader, writer io.Writer, mode string, interactive bool) *TerminalApprover {
	return &TerminalApprover{
		reader:      reader,
		writer:      writer,
		mode:        normalizeApprovalMode(mode),
		interactive: interactive,
	}
}

func (a *TerminalApprover) Mode() string {
	return a.mode
}

func (a *TerminalApprover) SetMode(mode string) error {
	mode = normalizeApprovalMode(mode)
	switch mode {
	case ApprovalConfirm, ApprovalDangerously:
		a.mode = mode
		return nil
	default:
		return fmt.Errorf("invalid approval mode %q", mode)
	}
}

func (a *TerminalApprover) ApproveCommand(_ context.Context, command string) (bool, error) {
	if a.mode == ApprovalDangerously {
		return true, nil
	}
	if !a.interactive || a.reader == nil {
		return false, fmt.Errorf("command approval requires an interactive terminal; rerun with --dangerously to skip prompts")
	}

	for {
		fmt.Fprintln(a.writer)
		fmt.Fprintln(a.writer, "Command approval required:")
		fmt.Fprintf(a.writer, "  %s\n", strings.TrimSpace(command))
		fmt.Fprint(a.writer, "Run? [y]es / [n]o / [a]lways dangerously for this session: ")

		line, err := a.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return false, err
		}

		answer := strings.ToLower(strings.TrimSpace(line))
		switch answer {
		case "y", "yes":
			return true, nil
		case "n", "no", "":
			return false, nil
		case "a", "always", "dangerously":
			a.mode = ApprovalDangerously
			fmt.Fprintln(a.writer, "approval mode switched to dangerously for this session")
			return true, nil
		default:
			fmt.Fprintln(a.writer, "please answer y, n, or a")
		}

		if err == io.EOF {
			return false, nil
		}
	}
}

func (a *TerminalApprover) ApproveWrite(_ context.Context, path, content string) (bool, error) {
	if a.mode == ApprovalDangerously {
		return true, nil
	}
	if !a.interactive || a.reader == nil {
		return false, fmt.Errorf("file write approval requires an interactive terminal; rerun with --dangerously to skip prompts")
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
		fmt.Fprintln(a.writer)
		fmt.Fprintln(a.writer, "File write approval required:")
		fmt.Fprintf(a.writer, "  path: %s\n", strings.TrimSpace(path))
		fmt.Fprintf(a.writer, "  bytes: %d\n", len(content))
		fmt.Fprintf(a.writer, "  preview: %s\n", preview)
		fmt.Fprint(a.writer, "Write file? [y]es / [n]o / [a]lways dangerously for this session: ")

		line, err := a.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return false, err
		}

		answer := strings.ToLower(strings.TrimSpace(line))
		switch answer {
		case "y", "yes":
			return true, nil
		case "n", "no", "":
			return false, nil
		case "a", "always", "dangerously":
			a.mode = ApprovalDangerously
			fmt.Fprintln(a.writer, "approval mode switched to dangerously for this session")
			return true, nil
		default:
			fmt.Fprintln(a.writer, "please answer y, n, or a")
		}

		if err == io.EOF {
			return false, nil
		}
	}
}

func normalizeApprovalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ApprovalConfirm:
		return ApprovalConfirm
	case ApprovalDangerously:
		return ApprovalDangerously
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
