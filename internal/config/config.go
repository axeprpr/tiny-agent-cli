package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"tiny-agent-cli/internal/tools"
)

type Config struct {
	BaseURL        string
	Model          string
	APIKey         string
	ContextWindow  int
	WorkDir        string
	StateDir       string
	MaxSteps       int
	CommandTimeout time.Duration
	ModelTimeout   time.Duration
	Shell          string
	ApprovalMode   string
	Team           string
	SettingsURL    string
	SettingsSync   bool
	Hooks          tools.HookConfig
}

const defaultMaxSteps = 0

func FromEnv() Config {
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}
	stateDir := defaultStateDir(workDir)

	cfg := Config{
		BaseURL:        firstEnv("MODEL_BASE_URL", "OPENAI_BASE_URL", "OLLAMA_BASE_URL"),
		Model:          firstEnv("MODEL_NAME", "OPENAI_MODEL", "OLLAMA_MODEL"),
		APIKey:         firstEnv("MODEL_API_KEY", "OPENAI_API_KEY"),
		ContextWindow:  getEnvInt("MODEL_CONTEXT_WINDOW", 32768),
		WorkDir:        getEnv("AGENT_WORKDIR", workDir),
		StateDir:       getEnv("AGENT_STATE_DIR", stateDir),
		MaxSteps:       getEnvInt("AGENT_MAX_STEPS", defaultMaxSteps),
		CommandTimeout: getEnvDuration("AGENT_COMMAND_TIMEOUT", 30*time.Second),
		ModelTimeout:   getEnvDuration("MODEL_TIMEOUT", 180*time.Second),
		Shell:          getEnv("AGENT_SHELL", defaultShell()),
		ApprovalMode:   getEnv("AGENT_APPROVAL", "confirm"),
		Team:           getEnv("AGENT_TEAM", ""),
		SettingsURL:    getEnv("AGENT_SETTINGS_ENDPOINT", ""),
		SettingsSync:   getEnvBool("AGENT_SETTINGS_SYNC", true),
		Hooks:          loadHookConfigFromEnv(),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:11434/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "qwen2.5-coder:7b"
	}

	return cfg
}

func loadHookConfigFromEnv() tools.HookConfig {
	return tools.HookConfig{
		PreToolUse:  splitHookCommands(os.Getenv("AGENT_PRE_TOOL_USE_HOOKS")),
		PostToolUse: splitHookCommands(os.Getenv("AGENT_POST_TOOL_USE_HOOKS")),
	}
}

func splitHookCommands(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func defaultStateDir(workDir string) string {
	newDir := filepath.Join(workDir, ".tacli")
	oldDir := filepath.Join(workDir, ".onek-agent")
	if _, err := os.Stat(newDir); err == nil {
		return newDir
	}
	if _, err := os.Stat(oldDir); err == nil {
		return oldDir
	}
	return newDir
}

func DefaultStateDir(workDir string) string {
	return defaultStateDir(workDir)
}

func (c *Config) SetCommandTimeout(text string) error {
	d, err := time.ParseDuration(text)
	if err != nil {
		return err
	}
	c.CommandTimeout = d
	return nil
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("base URL is required")
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if c.ContextWindow <= 0 {
		return fmt.Errorf("context window must be > 0")
	}
	if c.CommandTimeout <= 0 {
		return fmt.Errorf("command timeout must be > 0")
	}
	if c.ModelTimeout <= 0 {
		return fmt.Errorf("model timeout must be > 0")
	}

	abs, err := filepath.Abs(c.WorkDir)
	if err != nil {
		return fmt.Errorf("resolve workdir: %w", err)
	}
	c.WorkDir = abs
	stateAbs, err := filepath.Abs(c.StateDir)
	if err != nil {
		return fmt.Errorf("resolve state dir: %w", err)
	}
	c.StateDir = stateAbs
	c.Team = strings.TrimSpace(c.Team)
	c.SettingsURL = strings.TrimSpace(c.SettingsURL)
	return nil
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		return "powershell.exe"
	}
	return "bash"
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func getEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}

func getEnvBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
