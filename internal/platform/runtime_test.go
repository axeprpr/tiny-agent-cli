package platform

import (
	"os"
	"os/exec"
	"testing"
)

func TestShellInvocationUsesProvidedShell(t *testing.T) {
	name, args := ShellInvocation("bash", "echo ok")
	if name != "bash" {
		t.Fatalf("unexpected shell name: %q", name)
	}
	if len(args) == 0 {
		t.Fatalf("expected shell args")
	}
}

func TestHookShellCommandReturnsCommand(t *testing.T) {
	cmd := HookShellCommand("echo ok")
	if cmd == nil {
		t.Fatalf("expected command")
	}
	if cmd.Path == "" {
		t.Fatalf("expected executable path, got %#v", cmd)
	}
}

func TestConfigureCommandCancellationDoesNotPanic(t *testing.T) {
	cmd := &exec.Cmd{}
	ConfigureCommandCancellation(cmd)
}

func TestNormalizeScopeKey(t *testing.T) {
	if got := NormalizeScopeKey(""); got != "default" {
		t.Fatalf("unexpected empty scope key: %q", got)
	}
	if got := NormalizeScopeKey(`/tmp/demo\repo`); got != "/tmp/demo/repo" {
		t.Fatalf("unexpected normalized scope key: %q", got)
	}
}

func TestSafeName(t *testing.T) {
	if got := SafeName(` hello/world\demo `); got != "hello-world-demo" {
		t.Fatalf("unexpected safe name: %q", got)
	}
}

func TestPluginCapabilities(t *testing.T) {
	if PluginsSupported() && PluginLibraryExt() == "" {
		t.Fatalf("expected plugin extension when plugins are supported")
	}
	if !PluginsSupported() && PluginSupportMessage() == "" {
		t.Fatalf("expected plugin support message when unsupported")
	}
}

func TestCurrentCapabilities(t *testing.T) {
	got := CurrentCapabilities(os.Stdin)
	if got.DefaultShell == "" {
		t.Fatalf("expected default shell")
	}
	if got.OS == "" {
		t.Fatalf("expected OS name")
	}
}
