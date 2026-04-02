package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const skillFileName = "SKILL.md"

type Skill struct {
	Name        string
	Description string
	Path        string
}

func DiscoverSkills(workDir string) ([]Skill, error) {
	roots, err := skillRoots(workDir)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []Skill
	for _, root := range roots {
		items, err := discoverSkillsInRoot(root)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key := strings.ToLower(strings.TrimSpace(item.Path))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, item)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name+"\x00"+out[i].Path) < strings.ToLower(out[j].Name+"\x00"+out[j].Path)
	})
	return out, nil
}

func skillRoots(workDir string) ([]string, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workdir: %w", err)
	}
	codeHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codeHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			codeHome = filepath.Join(home, ".codex")
		}
	}

	roots := []string{
		filepath.Join(absWorkDir, ".codex", "skills"),
		filepath.Join(absWorkDir, ".agents", "skills"),
	}
	if strings.TrimSpace(codeHome) != "" {
		roots = append(roots, filepath.Join(codeHome, "skills"))
	}

	uniq := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		uniq = append(uniq, abs)
	}
	return uniq, nil
}

func discoverSkillsInRoot(root string) ([]Skill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skill root %s: %w", root, err)
	}

	var out []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(root, entry.Name())
		skillFile := filepath.Join(skillDir, skillFileName)
		data, err := os.ReadFile(skillFile)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read skill file %s: %w", skillFile, err)
		}
		skill := parseSkillMarkdown(entry.Name(), skillFile, string(data))
		out = append(out, skill)
	}
	return out, nil
}

func parseSkillMarkdown(fallbackName, path, text string) Skill {
	name := strings.TrimSpace(fallbackName)
	desc := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			header := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if header != "" && name == fallbackName {
				name = header
			}
			continue
		}
		desc = line
		break
	}

	if name == "" {
		name = fallbackName
	}
	if desc == "" {
		desc = "No description"
	}

	return Skill{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(desc),
		Path:        strings.TrimSpace(path),
	}
}

