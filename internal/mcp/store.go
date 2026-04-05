package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Server struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Transport string            `json:"transport,omitempty"`
}

type State struct {
	Servers []Server  `json:"servers,omitempty"`
	SavedAt time.Time `json:"saved_at"`
}

func Path(stateDir string) string {
	return filepath.Join(stateDir, "mcp", "servers.json")
}

func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	state.Servers = normalizeServers(state.Servers)
	return state, nil
}

func Save(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	state.Servers = normalizeServers(state.Servers)
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

func Upsert(state State, server Server) State {
	server = normalizeServer(server)
	if server.Name == "" {
		return state
	}
	replaced := false
	for i := range state.Servers {
		if strings.EqualFold(state.Servers[i].Name, server.Name) {
			state.Servers[i] = server
			replaced = true
			break
		}
	}
	if !replaced {
		state.Servers = append(state.Servers, server)
	}
	state.Servers = normalizeServers(state.Servers)
	return state
}

func Remove(state State, name string) (State, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return state, false
	}
	out := state.Servers[:0]
	removed := false
	for _, server := range state.Servers {
		if strings.EqualFold(server.Name, name) {
			removed = true
			continue
		}
		out = append(out, server)
	}
	state.Servers = normalizeServers(out)
	return state, removed
}

func normalizeServers(servers []Server) []Server {
	if len(servers) == 0 {
		return nil
	}
	out := make([]Server, 0, len(servers))
	seen := make(map[string]bool, len(servers))
	for _, server := range servers {
		server = normalizeServer(server)
		key := strings.ToLower(server.Name)
		if server.Name == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, server)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func normalizeServer(server Server) Server {
	server.Name = strings.TrimSpace(server.Name)
	server.Command = strings.TrimSpace(server.Command)
	server.Transport = strings.TrimSpace(server.Transport)
	if server.Transport == "" {
		server.Transport = "stdio"
	}
	for i := range server.Args {
		server.Args[i] = strings.TrimSpace(server.Args[i])
	}
	return server
}
