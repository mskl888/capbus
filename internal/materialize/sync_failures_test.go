package materialize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/capbus/capbus/internal/manifest"
)

// The failure paths the 2026-11-23 follow-up review named. Each one is a state
// a user can actually reach, and each one used to write something anyway.

// Criterion 1: one agent's config cannot be planned, so no agent is written.
func TestPlanFailureWritesNothing(t *testing.T) {
	home := withHome(t)
	// Claude is present but its file is not JSON.
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	cursorPath := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.WriteFile(cursorPath, []byte(`{"mcpServers":{"mine":{"command":"/x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cursorPath)

	out := Sync(capSet(), Options{})
	if !out.Failed() {
		t.Fatal("a broken claude config should fail the sync")
	}
	if len(out.Wrote) != 0 {
		t.Fatalf("nothing may be written when the plan failed, wrote %v", out.Wrote)
	}
	after, _ := os.ReadFile(cursorPath)
	if string(before) != string(after) {
		t.Fatalf("cursor config changed despite a failed plan:\n%s", after)
	}
}

// Criterion 3: a config we cannot read is not an empty one; stop instead of
// overwriting it, and do not delete it on the way out.
func TestUnreadableConfigAborts(t *testing.T) {
	home := withHome(t)
	// A directory where the file should be: ReadFile fails with something that
	// is not IsNotExist, which is the case the old code treated as "no file".
	blocked := filepath.Join(home, ".claude.json")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}

	out := Sync(capSet(), Options{})
	if !out.Failed() {
		t.Fatal("an unreadable config should stop the sync")
	}
	if _, err := os.Stat(blocked); err != nil {
		t.Fatalf("the unreadable path was removed: %v", err)
	}
}

// Criterion 4: a second sync has nothing to do, and that is success.
func TestSecondSyncReportsNoChange(t *testing.T) {
	home := withHome(t)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := Sync(capSet(), Options{}); out.Failed() {
		t.Fatalf("first sync failed: %v", out.Errs)
	} else if len(out.Wrote) == 0 {
		t.Fatal("first sync wrote nothing")
	}

	out := Sync(capSet(), Options{})
	if out.Failed() {
		t.Fatalf("second sync must not fail: %v", out.Errs)
	}
	if len(out.Wrote) != 0 {
		t.Fatalf("second sync should write nothing, wrote %v", out.Wrote)
	}
	for _, c := range out.Changes {
		if c.Action != "unchanged" && c.Action != "kept" {
			t.Fatalf("second sync reported a change: %+v", c)
		}
	}
}

// Criterion 6: an entry that merely has capbus's shape, but which capbus never
// wrote, is left alone.
func TestHandWrittenCapbusShapedEntrySurvives(t *testing.T) {
	home := withHome(t)
	p := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(p, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// First sync records in the ledger that capbus wrote "github".
	if out := Sync(capSet(), Options{}); out.Failed() {
		t.Fatalf("first sync failed: %v", out.Errs)
	}

	// The user adds a capbus-shaped entry by hand. It is not in the ledger.
	var got map[string]any
	b, _ := os.ReadFile(p)
	_ = json.Unmarshal(b, &got)
	servers, _ := got["mcpServers"].(map[string]any)
	servers["handmade"] = map[string]any{
		"command": filepath.Join(home, "capbus"),
		"args":    []any{"exec", "handmade"},
	}
	b, _ = json.Marshal(got)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}

	// The capability it names is not declared, which is exactly when the old
	// code would have deleted it.
	if out := Sync(capSet(), Options{}); out.Failed() {
		t.Fatalf("second sync failed: %v", out.Errs)
	}
	b, _ = os.ReadFile(p)
	_ = json.Unmarshal(b, &got)
	servers, _ = got["mcpServers"].(map[string]any)
	if _, ok := servers["handmade"]; !ok {
		t.Fatal("a hand written capbus-shaped entry was deleted")
	}
}

// A name collision is reported and stops the whole sync.
func TestConflictIsReported(t *testing.T) {
	home := withHome(t)
	p := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"github":{"command":"/their/github"}}}`
	if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	out := Sync(capSet(), Options{})
	if !out.Failed() {
		t.Fatal("a collision must fail the sync")
	}
	c := out.Conflicts()
	if len(c) != 1 || c[0] != "claude-code/github" {
		t.Fatalf("the collision was not reported: %v", c)
	}
	if b, _ := os.ReadFile(p); string(b) != original {
		t.Fatalf("the file changed despite a collision:\n%s", b)
	}
}

// Same name, same arguments: still a collision. Identical bytes are not
// permission to claim someone else's entry.
func TestIdenticalEntriesStillConflict(t *testing.T) {
	home := withHome(t)
	p := filepath.Join(home, ".claude.json")
	// Build an entry byte-identical to what capbus would write for "github".
	m := capSet()[0]
	entry := toEntryMap(m, Options{}, selfExe())
	servers := map[string]any{"github": entry}
	root := map[string]any{"mcpServers": servers}
	b, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}

	out := Sync(capSet(), Options{})
	if !out.Failed() {
		t.Fatal("an identical entry capbus did not write must still conflict")
	}
	b, _ = os.ReadFile(p)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	sv, _ := got["mcpServers"].(map[string]any)
	if _, ok := sv["github"]; !ok {
		t.Fatal("the user's identical entry was removed")
	}
}

// A conflicted name never reaches the ledger.
func TestConflictNeverEntersLedger(t *testing.T) {
	home := withHome(t)
	p := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(p, []byte(`{"mcpServers":{"github":{"command":"/their/github"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := Sync(capSet(), Options{}); !out.Failed() {
		t.Fatal("test premise: this should collide")
	}
	l := loadLedger()
	for agent, names := range l {
		for _, n := range names {
			if n == "github" {
				t.Fatalf("the conflicted name entered the ledger for %s: %v", agent, names)
			}
		}
	}
}

// A ledger that cannot be written is not a silent success.
func TestLedgerWriteFailureIsReported(t *testing.T) {
	home := withHome(t)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Put a file where the ledger's directory would be, so creating it fails.
	blocked := filepath.Join(home, "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPBUS_HOME", filepath.Join(blocked, "capbus"))

	out := Sync(capSet(), Options{})
	if !out.Failed() {
		t.Fatal("a ledger that cannot be saved must be reported")
	}
	joined := ""
	for _, e := range out.Errs {
		joined += e.Error()
	}
	if !strings.Contains(joined, "ledger") {
		t.Fatalf("the error does not mention the ledger: %s", joined)
	}
}

// Registry errors are the caller's to catch: Sync must not be reached with a
// manifest set it cannot trust. This documents the contract the CLI honours.
func TestSyncIsNotCalledWithInvalidManifests(t *testing.T) {
	m := capSet()[0]
	m.Name = "../evil"
	if err := m.Validate(); err == nil {
		t.Fatal("test premise: this name should be invalid")
	}
	var _ = manifest.KindMCP // keep the import honest
}
