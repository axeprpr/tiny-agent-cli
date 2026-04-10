package tools

import (
	"sort"
	"strings"
)

type CapabilityPack struct {
	Name        string
	Description string
	When        string
	Roles       []string
	Tools       []string
	Steps       []string
}

func BundledCapabilityPacks() []CapabilityPack {
	packs := []CapabilityPack{
		{
			Name:        "repo-research",
			Description: "Map a repository before implementation or review.",
			When:        "Use when the user asks for architecture, onboarding, impact analysis, or broad codebase investigation.",
			Roles:       []string{"explore", "plan"},
			Tools:       []string{"list_files", "read_file", "search_files", "run_command"},
			Steps: []string{
				"Identify project shape, core modules, and verification commands.",
				"Read the smallest set of files that explains the behavior.",
				"Return findings with concrete file references and open risks.",
			},
		},
		{
			Name:        "web-app",
			Description: "Build and verify frontend or full-stack local app changes.",
			When:        "Use when work involves UI behavior, dev servers, screenshots, or browser-visible state.",
			Roles:       []string{"implement", "verify"},
			Tools:       []string{"read_file", "write_file", "edit_file", "run_command", "start_background_job"},
			Steps: []string{
				"Inspect existing app structure and package scripts.",
				"Make targeted edits using the existing UI conventions.",
				"Run tests or a dev server and verify rendered behavior when possible.",
			},
		},
		{
			Name:        "release",
			Description: "Run release verification, build artifacts, and prepare tags.",
			When:        "Use when the user asks to test, build, tag, checksum, or publish release artifacts.",
			Roles:       []string{"verify", "implement"},
			Tools:       []string{"read_file", "run_command"},
			Steps: []string{
				"Check repository status and existing release conventions.",
				"Run the configured test and build commands.",
				"Create artifacts, checksums, tags, and a concise release summary as requested.",
			},
		},
		{
			Name:        "ops",
			Description: "Inspect local operational state, logs, services, and health checks.",
			When:        "Use when work involves deployed processes, service logs, ports, or local runtime health.",
			Roles:       []string{"explore", "verify"},
			Tools:       []string{"run_command", "start_background_job"},
			Steps: []string{
				"Inspect process, port, and log state before changing anything.",
				"Prefer read-only diagnostics unless the user authorizes operational changes.",
				"Report concrete commands run, observed failures, and cleanup actions.",
			},
		},
	}
	sort.Slice(packs, func(i, j int) bool {
		return strings.ToLower(packs[i].Name) < strings.ToLower(packs[j].Name)
	})
	return packs
}

func FindCapabilityPack(name string) (CapabilityPack, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return CapabilityPack{}, false
	}
	for _, pack := range BundledCapabilityPacks() {
		if strings.ToLower(pack.Name) == name {
			return pack, true
		}
	}
	return CapabilityPack{}, false
}
