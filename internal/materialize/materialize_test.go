package materialize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	yaml "gopkg.in/yaml.v3"

	"github.com/capbus/capbus/internal/manifest"
)

func withHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CAPBUS_HOME", filepath.Join(dir, ".capbus"))
	return dir
}

func capSet() []manifest.Manifest {
	return []manifest.Manifest{{
		Name:   "github",
		Kind:   manifest.KindMCP,
		Scope:  manifest.ScopeGlobal,
		Source: manifest.Source{NPM: "@modelcontextprotocol/server-github"},
	}}
}

// verbOf reads args[0] out of a shim entry.
func verbOf(e map[string]any) string {
	args, _ := e["args"].([]any)
	if len(args) == 0 {
		return ""
	}
	v, _ := args[0].(string)
	return v
}

func TestEntryUsesShims(t *testing.T) {
	m := capSet()[0]
	if e := toEntryMap(m, Options{}, "/x/capbus"); e["command"] != "/x/capbus" || verbOf(e) != "exec" {
		t.Fatalf("non-shared mode must use the exec shim, got %+v", e)
	}
	if e := toEntryMap(m, Options{Shared: true}, "/x/capbus"); verbOf(e) != "bridge" {
		t.Fatalf("shared mode must use the bridge shim, got %+v", e)
	}
	ws := manifest.Manifest{Name: "browser", Kind: manifest.KindMCP, Scope: manifest.ScopeWorkspace, Stateful: true}
	if e := toEntryMap(ws, Options{Shared: true}, "/x/capbus"); verbOf(e) != "exec" {
		t.Fatalf("workspace-scoped capability must not use the shared bridge, got %+v", e)
	}
}

// Claude Code's config is JSON, may carry a UTF-8 BOM from a Windows editor,
// and holds keys we must not eat.
func TestClaudePreservesForeignKeysAndBOM(t *testing.T) {
	home := withHome(t)
	p := filepath.Join(home, ".claude.json")
	body := append(append([]byte{}, utf8BOM...), []byte(`{"theme":"dark","numStartups":42,"mcpServers":{"stale":{}}}`)...)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := claude{}.Render(capSet(), Options{})
	if err != nil {
		t.Fatalf("BOM should be tolerated, got %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(r.Files[""], &got); err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "dark" || got["numStartups"] != float64(42) {
		t.Fatal("foreign keys were dropped")
	}
	servers, _ := got["mcpServers"].(map[string]any)
	// The 2026-11-23 review found this assertion backwards: a server capbus did
	// not write belongs to the user and must survive a sync untouched.
	if _, ok := servers["stale"]; !ok {
		t.Fatal("a server capbus did not write was deleted")
	}
	if _, ok := servers["github"]; !ok {
		t.Fatal("capability missing")
	}
}

func TestClaudeRefusesToClobberInvalidJSON(t *testing.T) {
	home := withHome(t)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (claude{}).Render(capSet(), Options{}); err == nil {
		t.Fatal("invalid JSON must be an error, not a silent overwrite")
	}
}

// Codex uses TOML: [mcp_servers.<name>] with command and args.
func TestCodexMergesToml(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := codex{}.Render(capSet(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	files := r.Files
	var doc map[string]any
	if err := toml.Unmarshal(files[""], &doc); err != nil {
		t.Fatalf("generated TOML does not parse: %v", err)
	}
	if doc["model"] != "gpt-5" {
		t.Fatal("existing key was dropped")
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	gh, _ := servers["github"].(map[string]any)
	if gh["command"] == nil {
		t.Fatalf("mcp_servers.github.command missing: %v", servers)
	}
	args, _ := gh["args"].([]any)
	if len(args) != 2 || args[0] != "exec" {
		t.Fatalf("expected exec shim args, got %v", args)
	}
}

func TestCodexRefusesInvalidToml(t *testing.T) {
	home := withHome(t)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("not = [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (codex{}).Render(capSet(), Options{}); err == nil {
		t.Fatal("invalid TOML must be an error, not a silent overwrite")
	}
}

// DSH loads MCP servers from a bundle: package.json plus a cordis patch that
// inserts @deepseek-ai/dsh-mcp-client per server.
func TestDSHEmitsBundle(t *testing.T) {
	withHome(t)
	if err := os.MkdirAll(filepath.Join(userHome(), ".dsh"), 0o755); err != nil {
		t.Fatal(err)
	}

	r, err := dsh{}.Render(capSet(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	files := r.Files
	if _, ok := files["package.json"]; !ok {
		t.Fatal("package.json missing")
	}
	patch, ok := files["cordis.patch.yml"]
	if !ok {
		t.Fatal("cordis.patch.yml missing")
	}

	var pkg struct {
		Name string `json:"name"`
		DSH  struct {
			Bundle struct {
				Patch string `json:"patch"`
			} `json:"bundle"`
		} `json:"dsh"`
	}
	if err := json.Unmarshal(files["package.json"], &pkg); err != nil {
		t.Fatalf("package.json is not valid JSON: %v", err)
	}
	if pkg.DSH.Bundle.Patch == "" {
		t.Fatal("package.json does not point at the patch")
	}

	var doc []struct {
		Insert []struct {
			ID     string `yaml:"id"`
			Name   string `yaml:"name"`
			Config struct {
				ServerName string   `yaml:"serverName"`
				Transport  string   `yaml:"transport"`
				Command    string   `yaml:"command"`
				Args       []string `yaml:"args"`
			} `yaml:"config"`
		} `yaml:"insert"`
	}
	if err := yaml.Unmarshal(patch, &doc); err != nil {
		t.Fatalf("cordis.patch.yml is not valid YAML: %v\n%s", err, patch)
	}
	if len(doc) != 1 || len(doc[0].Insert) != 1 {
		t.Fatalf("expected one insert with one entry, got %+v", doc)
	}
	e := doc[0].Insert[0]
	if e.Name != "@deepseek-ai/dsh-mcp-client" {
		t.Fatalf("wrong plugin: %q", e.Name)
	}
	if e.Config.ServerName != "github" || e.Config.Transport != "stdio" {
		t.Fatalf("wrong config: %+v", e.Config)
	}
	if len(e.Config.Args) != 2 || e.Config.Args[0] != "exec" {
		t.Fatalf("expected exec shim, got %v", e.Config.Args)
	}
}

// One capability declaration, four agents, four dialects.
func TestSyncWritesEveryDetectedAgent(t *testing.T) {
	home := withHome(t)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".dsh"), 0o755); err != nil {
		t.Fatal(err)
	}

	wrote, _, errs := Sync(capSet(), Options{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	// claude + cursor + codex are single files; dsh writes two files.
	if len(wrote) != 5 {
		t.Fatalf("expected 5 files written, got %d: %v", len(wrote), wrote)
	}
	for _, p := range []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".cursor", "mcp.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".capbus", "dsh-bundle", "package.json"),
		filepath.Join(home, ".capbus", "dsh-bundle", "cordis.patch.yml"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was not written: %v", p, err)
		}
	}
}

func TestSyncDryRunWritesNothing(t *testing.T) {
	home := withHome(t)
	p := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(p, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote, _, errs := Sync(capSet(), Options{DryRun: true})
	if len(errs) > 0 || len(wrote) != 1 {
		t.Fatalf("wrote=%v errs=%v", wrote, errs)
	}
	b, _ := os.ReadFile(p)
	if string(b) != `{}` {
		t.Fatal("dry run modified the file")
	}
}

// A capability marked shared must reach one process even when the user ran a
// plain sync: a second copy would be a second, silently divergent store.
func TestSharedCapabilityAlwaysUsesBridge(t *testing.T) {
	m := capSet()[0]
	m.Shared = true
	if e := toEntryMap(m, Options{}, "/x/capbus"); verbOf(e) != "bridge" {
		t.Fatalf("shared capability must use the bridge without --shared, got %+v", e)
	}
	m.Shared = false
	if e := toEntryMap(m, Options{}, "/x/capbus"); verbOf(e) != "exec" {
		t.Fatalf("an unshared capability should spawn its own copy, got %+v", e)
	}
}

// --- the 2026-11-23 review, acceptance criteria 1 and 2 ---------------------

// A server the user added by hand must survive a sync byte for byte.
func TestSyncKeepsUserServers(t *testing.T) {
	home := withHome(t)
	p := filepath.Join(home, ".claude.json")
	user := `{"mcpServers":{"mine":{"command":"/usr/bin/mine","args":["--x"]}}}`
	if err := os.WriteFile(p, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, errs := Sync(capSet(), Options{}); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}

	var got map[string]any
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	servers, _ := got["mcpServers"].(map[string]any)
	mine, ok := servers["mine"].(map[string]any)
	if !ok {
		t.Fatal("the user's own server was deleted")
	}
	if mine["command"] != "/usr/bin/mine" {
		t.Fatalf("the user's own server was modified: %v", mine)
	}
	if _, ok := servers["github"]; !ok {
		t.Fatal("capability missing")
	}
}

// A shim capbus wrote, for a capability no longer declared, is removed.
func TestSyncRemovesOnlyItsOwn(t *testing.T) {
	home := withHome(t)
	p := filepath.Join(home, ".claude.json")
	body := `{"mcpServers":{` +
		`"old":{"command":"/x/capbus","args":["exec","old"]},` +
		`"mine":{"command":"/usr/bin/mine"}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, errs := Sync(capSet(), Options{}); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}

	var got map[string]any
	b, _ := os.ReadFile(p)
	_ = json.Unmarshal(b, &got)
	servers, _ := got["mcpServers"].(map[string]any)
	if _, ok := servers["old"]; ok {
		t.Fatal("capbus's own leftover was not removed")
	}
	if _, ok := servers["mine"]; !ok {
		t.Fatal("the user's server was removed along with capbus's")
	}
}

// A manifest whose name collides with a server capbus did not write loses: the
// user's entry is kept and the collision is reported.
func TestSyncSkipsNameCollision(t *testing.T) {
	home := withHome(t)
	p := filepath.Join(home, ".claude.json")
	user := `{"mcpServers":{"github":{"command":"/usr/bin/their-github"}}}`
	if err := os.WriteFile(p, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	_, changes, errs := Sync(capSet(), Options{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	var got map[string]any
	b, _ := os.ReadFile(p)
	_ = json.Unmarshal(b, &got)
	servers, _ := got["mcpServers"].(map[string]any)
	gh, _ := servers["github"].(map[string]any)
	if gh["command"] != "/usr/bin/their-github" {
		t.Fatalf("capbus overwrote a server it did not write: %v", gh)
	}
	found := false
	for _, c := range changes {
		if c.Name == "github" && c.Action == "conflict" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the collision was not reported: %+v", changes)
	}
}

// rollback restores what was there and removes what did not exist.
func TestRollbackRestores(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.json")
	fresh := filepath.Join(dir, "fresh.json")
	if err := os.WriteFile(old, []byte(`{"before":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	done := []write{
		{path: old, body: []byte(`{"after":true}`), prev: []byte(`{"before":true}`), had: true},
		{path: fresh, body: []byte(`{"new":true}`), had: false},
	}
	_ = os.WriteFile(old, done[0].body, 0o600)
	_ = os.WriteFile(fresh, done[1].body, 0o600)

	rollback(done)

	if b, _ := os.ReadFile(old); string(b) != `{"before":true}` {
		t.Fatalf("existing file not restored: %s", b)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatal("file that did not exist before was left behind")
	}
}
