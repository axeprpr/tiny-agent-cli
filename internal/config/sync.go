package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tiny-agent-cli/internal/tools"
)

var settingsHTTPClient = http.DefaultClient

type SyncedSettings struct {
	Model        string                `json:"model,omitempty"`
	ApprovalMode string                `json:"approval_mode,omitempty"`
	Team         string                `json:"team,omitempty"`
	Hooks        tools.HookConfig      `json:"hooks,omitempty"`
	Permissions  tools.PermissionState `json:"permissions,omitempty"`
}

func SnapshotSettings(cfg Config, permissions tools.PermissionState) SyncedSettings {
	return SyncedSettings{
		Model:        strings.TrimSpace(cfg.Model),
		ApprovalMode: strings.TrimSpace(cfg.ApprovalMode),
		Team:         strings.TrimSpace(cfg.Team),
		Hooks:        cfg.Hooks,
		Permissions:  permissions,
	}
}

func (c *Config) ApplySettings(settings SyncedSettings) {
	if strings.TrimSpace(settings.Model) != "" {
		c.Model = strings.TrimSpace(settings.Model)
	}
	if strings.TrimSpace(settings.ApprovalMode) != "" {
		c.ApprovalMode = strings.TrimSpace(settings.ApprovalMode)
	}
	if strings.TrimSpace(settings.Team) != "" {
		c.Team = strings.TrimSpace(settings.Team)
	}
	if settings.Hooks.Enabled != tools.DefaultHookConfig().Enabled || len(settings.Hooks.Disabled) > 0 {
		c.Hooks = settings.Hooks
	}
}

func PullSettings(ctx context.Context, endpoint string) (SyncedSettings, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(endpoint), nil)
	if err != nil {
		return SyncedSettings{}, err
	}
	resp, err := settingsHTTPClient.Do(req)
	if err != nil {
		return SyncedSettings{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return SyncedSettings{}, fmt.Errorf("settings pull failed: %s", resp.Status)
	}
	var settings SyncedSettings
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return SyncedSettings{}, err
	}
	return settings, nil
}

func PushSettings(ctx context.Context, endpoint string, settings SyncedSettings) error {
	body, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimSpace(endpoint), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := settingsHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("settings push failed: %s", resp.Status)
	}
	return nil
}

func SettingsSyncContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 3*time.Second)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
