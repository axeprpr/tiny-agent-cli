package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BaseURL        string
	Model          string
	APIKey         string
	WorkDir        string
	MaxSteps       int
	CommandTimeout time.Duration
	Shell          string
	ApprovalMode   string
}

func FromEnv() Config {
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	cfg := Config{
		BaseURL:        firstEnv("MODEL_BASE_URL", "OPENAI_BASE_URL", "OLLAMA_BASE_URL"),
		Model:          firstEnv("MODEL_NAME", "OPENAI_MODEL", "OLLAMA_MODEL"),
		APIKey:         firstEnv("MODEL_API_KEY", "OPENAI_API_KEY"),
		WorkDir:        getEnv("AGENT_WORKDIR", workDir),
		MaxSteps:       getEnvInt("AGENT_MAX_STEPS", 8),
		CommandTimeout: getEnvDuration("AGENT_COMMAND_TIMEOUT", 30*time.Second),
		Shell:          getEnv("AGENT_SHELL", defaultShell()),
		ApprovalMode:   getEnv("AGENT_APPROVAL", "confirm"),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:11434/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "qwen2.5-coder:7b"
	}

	return cfg
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
	if c.MaxSteps <= 0 {
		return fmt.Errorf("max steps must be > 0")
	}
	if c.CommandTimeout <= 0 {
		return fmt.Errorf("command timeout must be > 0")
	}

	abs, err := filepath.Abs(c.WorkDir)
	if err != nil {
		return fmt.Errorf("resolve workdir: %w", err)
	}
	c.WorkDir = abs
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
