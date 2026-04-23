package platform

import (
	"os"
	"path/filepath"
	"strings"
)

type Capabilities struct {
	OS               string `json:"os"`
	DefaultShell     string `json:"default_shell"`
	PluginSupport    bool   `json:"plugin_support"`
	PluginLibraryExt string `json:"plugin_library_ext,omitempty"`
	InteractiveStdin bool   `json:"interactive_stdin"`
}

func CurrentCapabilities(stdin *os.File) Capabilities {
	return Capabilities{
		OS:               osName(),
		DefaultShell:     DefaultShell(),
		PluginSupport:    PluginsSupported(),
		PluginLibraryExt: PluginLibraryExt(),
		InteractiveStdin: IsInteractiveTerminal(stdin),
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

func NormalizeScopeKey(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return "default"
	}
	workDir = filepath.Clean(workDir)
	workDir = strings.ReplaceAll(workDir, "\\", "/")
	return workDir
}

func SafeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-")
	name = replacer.Replace(name)
	if name == "" {
		return "default"
	}
	return name
}

func PluginsSupported() bool {
	return !IsWindows()
}

func PluginLibraryExt() string {
	if PluginsSupported() {
		return ".so"
	}
	return ""
}

func PluginSupportMessage() string {
	if PluginsSupported() {
		return "plugins supported"
	}
	return "plugins are not supported on this platform"
}

func osName() string {
	if IsWindows() {
		return "windows"
	}
	return "unix"
}
