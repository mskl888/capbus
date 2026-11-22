package materialize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/capbus/capbus/internal/manifest"
)

// fakeAgent points a materializer at an arbitrary file so tests do not touch
// the real agent configs.
type fakeAgent struct{ path string }

func (f fakeAgent) Name() string         { return "fake" }
func (f fakeAgent) Dialect() string      { return "mcpServers" }
func (f fakeAgent) Path() (string, bool) { return f.path, true }

func capSet() []manifest.Manifest {
	return []manifest.Manifest{{
		Name:   "github",
		Kind:   manifest.KindMCP,
		Scope:  manifest.ScopeGlobal,
		Source: manifest.Source{NPM: "@modelcontextprotocol/server-github"},
	}}
}

// Windows editors and PowerShell prepend a UTF-8 BOM. A config file that
// carries one is still valid JSON to the user, so it must parse here.
func TestRenderToleratesBOM(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	body := append(append([]byte{}, utf8BOM...), []byte(`{"theme":"dark"}`)...)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Render(fakeAgent{p}, capSet(), Options{})
	if err != nil {
		t.Fatalf("BOM should be tolerated, got %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "dark" {
		t.Fatal("existing key was dropped")
	}
	servers, _ := got["mcpServers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Fatal("capability was not written")
	}
}

// Keys the agent wrote for itself must survive a sync.
func TestRenderPreservesForeignKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(p, []byte(`{"numStartups":42,"mcpServers":{"old":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Render(fakeAgent{p}, capSet(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["numStartups"] != float64(42) {
		t.Fatal("foreign key lost")
	}
	servers, _ := got["mcpServers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Fatal("capability missing")
	}
	if _, ok := servers["old"]; ok {
		t.Fatal("stale capability should be replaced, not merged")
	}
}

// A config we cannot parse must never be overwritten.
func TestRenderRefusesToClobberInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(p, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(fakeAgent{p}, capSet(), Options{}); err == nil {
		t.Fatal("invalid JSON must be an error, not a silent overwrite")
	}
}

// Shared mode must point the agent at the bridge shim, not at npx: that is the
// difference between four backend processes and one.
func TestSharedModeUsesBridge(t *testing.T) {
	out, err := Render(fakeAgent{filepath.Join(t.TempDir(), "cfg.json")}, capSet(),
		Options{Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	servers, _ := got["mcpServers"].(map[string]any)
	gh, _ := servers["github"].(map[string]any)
	if gh["command"] == "npx" {
		t.Fatal("shared mode should not spawn npx inside the agent")
	}
	args, _ := gh["args"].([]any)
	if len(args) != 2 || args[0] != "bridge" || args[1] != "github" {
		t.Fatalf("expected bridge shim args, got %v", args)
	}
}

func TestEntriesUseShims(t *testing.T) {
	m := manifest.Manifest{
		Name: "github", Kind: manifest.KindMCP, Scope: manifest.ScopeGlobal,
		Source: manifest.Source{NPM: "@x/y"},
	}
	if e := toEntry(m, Options{}, "/x/capbus"); e.Command != "/x/capbus" || len(e.Args) != 2 || e.Args[0] != "exec" {
		t.Fatalf("non-shared mode must use the exec shim, got %+v", e)
	}
	if e := toEntry(m, Options{Shared: true}, "/x/capbus"); e.Args[0] != "bridge" {
		t.Fatalf("shared mode must use the bridge shim, got %+v", e)
	}

	// A workspace-scoped capability holds live state, so it must never be
	// pointed at the machine-wide shared backend.
	ws := manifest.Manifest{Name: "browser", Kind: manifest.KindMCP, Scope: manifest.ScopeWorkspace, Stateful: true}
	if e := toEntry(ws, Options{Shared: true}, "/x/capbus"); e.Args[0] != "exec" {
		t.Fatalf("workspace-scoped capability must not use the shared bridge, got %+v", e)
	}
}
