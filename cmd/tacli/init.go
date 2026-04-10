package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const initGitignoreComment = "# tacli local artifacts"

var initGitignoreEntries = []string{
	".tacli/",
	"CLAW.local.md",
}

type initStatus int

const (
	initStatusCreated initStatus = iota
	initStatusUpdated
	initStatusSkipped
)

func (s initStatus) label() string {
	switch s {
	case initStatusCreated:
		return "created"
	case initStatusUpdated:
		return "updated"
	default:
		return "skipped (already exists)"
	}
}

type initArtifact struct {
	Name   string
	Status initStatus
}

type initReport struct {
	ProjectRoot string
	Artifacts   []initArtifact
}

func (r initReport) render() string {
	lines := []string{
		"Init",
		fmt.Sprintf("  Project          %s", r.ProjectRoot),
	}
	for _, artifact := range r.Artifacts {
		lines = append(lines, fmt.Sprintf("  %-16s %s", artifact.Name, artifact.Status.label()))
	}
	lines = append(lines, "  Next step        Review and tailor CLAW.md for this repo")
	return strings.Join(lines, "\n")
}

type repoDetection struct {
	goModule      bool
	rustRoot      bool
	rustWorkspace bool
	python        bool
	packageJSON   bool
	typescript    bool
	nextjs        bool
	react         bool
	vite          bool
	nest          bool
	srcDir        bool
	testsDir      bool
	cmdDir        bool
	internalDir   bool
}

func runInit(args []string) int {
	workDir, err := parseInitFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid init config: %v\n", err)
		printInitUsage()
		return 2
	}
	report, err := initializeRepo(workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init error: %v\n", err)
		return 1
	}
	fmt.Println(report.render())
	return 0
}

func parseInitFlags(args []string) (string, error) {
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&workDir, "workdir", workDir, "workspace root")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}
	return abs, nil
}

func initializeRepo(workDir string) (initReport, error) {
	artifacts := make([]initArtifact, 0, 3)

	clawDir := filepath.Join(workDir, ".claw")
	status, err := ensureDir(clawDir)
	if err != nil {
		return initReport{}, err
	}
	artifacts = append(artifacts, initArtifact{Name: ".claw/", Status: status})

	gitignorePath := filepath.Join(workDir, ".gitignore")
	status, err = ensureGitignoreEntries(gitignorePath)
	if err != nil {
		return initReport{}, err
	}
	artifacts = append(artifacts, initArtifact{Name: ".gitignore", Status: status})

	clawMDPath := filepath.Join(workDir, "CLAW.md")
	status, err = writeFileIfMissing(clawMDPath, renderInitCLAWMD(workDir))
	if err != nil {
		return initReport{}, err
	}
	artifacts = append(artifacts, initArtifact{Name: "CLAW.md", Status: status})

	return initReport{
		ProjectRoot: workDir,
		Artifacts:   artifacts,
	}, nil
}

func ensureDir(path string) (initStatus, error) {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return initStatusSkipped, nil
		}
		return initStatusSkipped, fmt.Errorf("%s exists and is not a directory", path)
	}
	if !os.IsNotExist(err) {
		return initStatusSkipped, err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return initStatusSkipped, err
	}
	return initStatusCreated, nil
}

func writeFileIfMissing(path, content string) (initStatus, error) {
	if _, err := os.Stat(path); err == nil {
		return initStatusSkipped, nil
	} else if !os.IsNotExist(err) {
		return initStatusSkipped, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return initStatusSkipped, err
	}
	return initStatusCreated, nil
}

func ensureGitignoreEntries(path string) (initStatus, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		lines := append([]string{initGitignoreComment}, initGitignoreEntries...)
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			return initStatusSkipped, err
		}
		return initStatusCreated, nil
	} else if err != nil {
		return initStatusSkipped, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return initStatusSkipped, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	changed := false
	if !containsExactLine(lines, initGitignoreComment) {
		lines = append(lines, initGitignoreComment)
		changed = true
	}
	for _, entry := range initGitignoreEntries {
		if containsExactLine(lines, entry) {
			continue
		}
		lines = append(lines, entry)
		changed = true
	}
	if !changed {
		return initStatusSkipped, nil
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return initStatusSkipped, err
	}
	return initStatusUpdated, nil
}

func containsExactLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func renderInitCLAWMD(workDir string) string {
	detection := detectRepo(workDir)
	lines := []string{
		"# CLAW.md",
		"",
		"This file gives tacli project-specific guidance for work in this repository.",
		"",
		"## Detected stack",
	}

	languages := detectedLanguages(detection)
	if len(languages) == 0 {
		lines = append(lines, "- No language markers detected yet. Add the canonical build, test, and lint commands once the repo structure settles.")
	} else {
		lines = append(lines, "- Languages: "+strings.Join(languages, ", ")+".")
	}

	frameworks := detectedFrameworks(detection)
	if len(frameworks) == 0 {
		lines = append(lines, "- Frameworks/tooling markers: none detected from the starter scan.")
	} else {
		lines = append(lines, "- Frameworks/tooling markers: "+strings.Join(frameworks, ", ")+".")
	}
	lines = append(lines, "")

	verification := verificationLines(detection)
	if len(verification) > 0 {
		lines = append(lines, "## Verification")
		lines = append(lines, verification...)
		lines = append(lines, "")
	}

	structure := repositoryShapeLines(detection)
	if len(structure) > 0 {
		lines = append(lines, "## Repository shape")
		lines = append(lines, structure...)
		lines = append(lines, "")
	}

	lines = append(lines,
		"## Working agreement",
		"- Prefer small, reviewable changes and keep bootstrap guidance aligned with the real repo workflows.",
		"- Run the narrowest relevant verification first, then broaden when shared behavior or cross-module contracts change.",
		"- Keep machine-local notes in `CLAW.local.md` and avoid committing local overrides.",
		"- Update this file whenever build, test, lint, release, or dev-server workflows change.",
		"",
	)

	return strings.Join(lines, "\n")
}

func detectRepo(workDir string) repoDetection {
	packageJSON := strings.ToLower(readFileOrEmpty(filepath.Join(workDir, "package.json")))
	return repoDetection{
		goModule:      isFile(filepath.Join(workDir, "go.mod")),
		rustRoot:      isFile(filepath.Join(workDir, "Cargo.toml")),
		rustWorkspace: isFile(filepath.Join(workDir, "rust", "Cargo.toml")),
		python:        isFile(filepath.Join(workDir, "pyproject.toml")) || isFile(filepath.Join(workDir, "requirements.txt")) || isFile(filepath.Join(workDir, "setup.py")),
		packageJSON:   isFile(filepath.Join(workDir, "package.json")),
		typescript:    isFile(filepath.Join(workDir, "tsconfig.json")) || strings.Contains(packageJSON, "typescript"),
		nextjs:        strings.Contains(packageJSON, "\"next\""),
		react:         strings.Contains(packageJSON, "\"react\""),
		vite:          strings.Contains(packageJSON, "\"vite\""),
		nest:          strings.Contains(packageJSON, "@nestjs"),
		srcDir:        isDir(filepath.Join(workDir, "src")),
		testsDir:      isDir(filepath.Join(workDir, "tests")),
		cmdDir:        isDir(filepath.Join(workDir, "cmd")),
		internalDir:   isDir(filepath.Join(workDir, "internal")),
	}
}

func readFileOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func detectedLanguages(d repoDetection) []string {
	var languages []string
	if d.goModule {
		languages = append(languages, "Go")
	}
	if d.rustRoot || d.rustWorkspace {
		languages = append(languages, "Rust")
	}
	if d.python {
		languages = append(languages, "Python")
	}
	if d.typescript {
		languages = append(languages, "TypeScript")
	} else if d.packageJSON {
		languages = append(languages, "JavaScript/Node.js")
	}
	return languages
}

func detectedFrameworks(d repoDetection) []string {
	var frameworks []string
	if d.nextjs {
		frameworks = append(frameworks, "Next.js")
	}
	if d.react {
		frameworks = append(frameworks, "React")
	}
	if d.vite {
		frameworks = append(frameworks, "Vite")
	}
	if d.nest {
		frameworks = append(frameworks, "NestJS")
	}
	return frameworks
}

func verificationLines(d repoDetection) []string {
	var lines []string
	if d.goModule {
		lines = append(lines, "- Go: `go test ./...`")
	}
	if d.rustRoot {
		lines = append(lines, "- Rust: `cargo test`")
	}
	if d.rustWorkspace {
		lines = append(lines, "- Rust workspace: `cargo test --manifest-path rust/Cargo.toml`")
	}
	if d.python {
		if d.testsDir {
			lines = append(lines, "- Python: `python3 -m unittest discover -s tests -v` or the repo-specific test runner")
		} else {
			lines = append(lines, "- Python: document the canonical test runner once it is established")
		}
	}
	if d.packageJSON {
		lines = append(lines, "- Node.js: `npm test`")
	}
	if len(lines) == 0 {
		lines = append(lines, "- Add the canonical verification commands for this repo before relying on agent-generated changes.")
	}
	lines = append(lines, "- Keep this section current when the project switches to wrappers like `make`, `just`, or task runners.")
	return lines
}

func repositoryShapeLines(d repoDetection) []string {
	var lines []string
	if d.cmdDir {
		lines = append(lines, "- `cmd/` contains entrypoints or CLI-facing binaries.")
	}
	if d.internalDir {
		lines = append(lines, "- `internal/` contains shared implementation details and runtime logic.")
	}
	if d.srcDir {
		lines = append(lines, "- `src/` contains primary source files.")
	}
	if d.testsDir {
		lines = append(lines, "- `tests/` contains standalone or integration-style tests.")
	}
	return lines
}

func printInitUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tacli init [--workdir <path>]")
}
