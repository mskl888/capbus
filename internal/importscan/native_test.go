package importscan

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNativesReadsPlainCommand(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".dsh", "profiles", "desktop", "node_modules", "dsh-craft", "cordis.patch.yml"), `
- insert:
    - id: craft-mcp
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: craft
        transport: stdio
        command: '/opt/craft-cli'
        args: ['mcp', '--clean-room']
`)

	got := Natives(home)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d: %+v", len(got), got)
	}
	n := got[0]
	if n.Plugin != "dsh-craft" || n.Server != "craft" || n.Command != "/opt/craft-cli" || !n.Resolved {
		t.Fatalf("bad native: %+v", n)
	}
	if len(n.Args) != 2 {
		t.Fatalf("args: %v", n.Args)
	}
}

// DSH bundles may compute the command with a tag this parser cannot evaluate.
// The plugin must still be reported, with Resolved false, or the report lies
// about how many plugins are installed.
func TestNativesReportsUnreadableBundle(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".dsh", "profiles", "desktop", "node_modules", "dsh-rea", "cordis.patch.yml"), `
- insert:
    - id: rea-mcp
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: rea
        command: !!js process.execPath
`)

	got := Natives(home)
	if len(got) != 1 {
		t.Fatalf("expected the plugin to be reported, got %d", len(got))
	}
	if got[0].Resolved {
		t.Fatal("an unevaluable command must not be reported as resolved")
	}
}

// A patch that inserts some other plugin is not an MCP server and must not be
// counted as one.
func TestNativesIgnoresNonMCPPatches(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".dsh", "profiles", "desktop", "node_modules", "dsh-sidebar", "cordis.patch.yml"), `
- insert:
    - id: sidebar
      name: '@deepseek-ai/dsh-better-sidebar'
      config: {}
`)

	if got := Natives(home); len(got) != 0 {
		t.Fatalf("expected none, got %+v", got)
	}
}

func TestPluginPackagesCountsOncePerName(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, ".dsh", "profiles")
	for _, p := range []string{"desktop", "other"} {
		if err := os.MkdirAll(filepath.Join(base, p, "node_modules", "dsh-craft", "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := PluginPackages(home)
	if len(got) != 1 || got[0] != "dsh-craft" {
		t.Fatalf("expected the same package counted once, got %v", got)
	}
}

// A plugin that has never run has no state file; discovery must still find it
// from its bundle, or import misses most plugins on a fresh machine.
func TestDiscoverIncludesUnstartedPlugins(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".dsh", "profiles", "desktop", "node_modules", "dsh-craft", "cordis.patch.yml"), `
- insert:
    - id: craft-mcp
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: craft
        transport: stdio
        command: '/opt/craft-cli'
        args: ['mcp']
`)

	got := names(DiscoverIn(home))
	if _, ok := got["craft"]; !ok {
		t.Fatalf("bundle-only plugin not discovered: %+v", got)
	}
}
