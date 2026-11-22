package importscan

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(fs []Finding) map[string]Finding {
	m := map[string]Finding{}
	for _, f := range fs {
		m[f.Name] = f
	}
	return m
}

func TestFindsDSHPluginState(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".dsh", "plugins-data", "craft-anything", "mcp.state.json"),
		`{"cli":"C:\\x\\craft-anything-cli.exe","args":["mcp","--clean-room"],"pid":1}`)

	got := names(DiscoverIn(home))
	f, ok := got["craft-anything"]
	if !ok {
		t.Fatalf("not found: %+v", got)
	}
	if f.Command != `C:\x\craft-anything-cli.exe` || len(f.Args) != 2 {
		t.Fatalf("bad finding: %+v", f)
	}
	if f.Origin != "dsh" {
		t.Fatalf("origin %q", f.Origin)
	}
}

func TestFindsClaudeAndCursor(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude.json"),
		`{"theme":"dark","mcpServers":{"fs":{"command":"npx","args":["-y","server-filesystem","/tmp"]}}}`)
	write(t, filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers":{"other":{"command":"node","args":["other.js"]}}}`)

	got := names(DiscoverIn(home))
	if _, ok := got["fs"]; !ok {
		t.Fatal("claude entry not found")
	}
	if _, ok := got["other"]; !ok {
		t.Fatal("cursor entry not found")
	}
}

func TestFindsCodexTOML(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".codex", "config.toml"), `
model = 'gpt-5'
[mcp_servers.github]
command = 'npx'
args = ['-y', '@modelcontextprotocol/server-github']
`)

	got := names(DiscoverIn(home))
	f, ok := got["github"]
	if !ok {
		t.Fatalf("not found: %+v", got)
	}
	if f.Command != "npx" || len(f.Args) != 2 || f.Origin != "codex" {
		t.Fatalf("bad finding: %+v", f)
	}
}

// The whole point of discovery: the same plugin installed in three agents is one
// capability, not three.
func TestDuplicatesCollapseToOne(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".dsh", "plugins-data", "craft", "mcp.state.json"),
		`{"cli":"C:\\x\\craft.exe","args":["mcp"]}`)
	write(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"craft":{"command":"C:\\x\\craft.exe","args":["mcp"]}}}`)
	write(t, filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers":{"craft":{"command":"C:\\x\\craft.exe","args":["mcp"]}}}`)

	got := DiscoverIn(home)
	if len(got) != 1 {
		t.Fatalf("expected one capability, got %d: %+v", len(got), got)
	}
}

// Two different servers that happen to share a name must both survive.
func TestSameNameDifferentCommandBothKept(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"tool":{"command":"a.exe"}}}`)
	write(t, filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers":{"tool":{"command":"b.exe"}}}`)

	got := DiscoverIn(home)
	if len(got) != 2 {
		t.Fatalf("expected two, got %d: %+v", len(got), got)
	}
	if got[0].Name == got[1].Name {
		t.Fatalf("names collide: %+v", got)
	}
}

// Remote-only entries have no command to share, so they are skipped instead of
// producing a capability that cannot start.
func TestRemoteEntriesSkipped(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"remote":{"type":"http","url":"https://example.com/mcp"}}}`)

	if got := DiscoverIn(home); len(got) != 0 {
		t.Fatalf("expected none, got %+v", got)
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"Craft Anything": "craft-anything",
		"a//b":           "a-b",
		// Underscores are legal in a manifest name, so they are left alone
		// rather than rewritten into a different identity.
		"__x__": "__x__",
		"":      "",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Fatalf("sanitize(%q)=%q want %q", in, got, want)
		}
	}
}

func TestMissingSourcesAreNotErrors(t *testing.T) {
	if got := DiscoverIn(t.TempDir()); len(got) != 0 {
		t.Fatalf("empty home should find nothing, got %+v", got)
	}
}
