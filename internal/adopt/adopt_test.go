package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/capbus/capbus/internal/enabled"
)

// isolatedHome points every home()-style lookup at a temp directory, the same
// way the materialize tests do. On Windows UserHomeDir reads USERPROFILE, so
// both variables must be set.
func isolatedHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CAPBUS_HOME", filepath.Join(dir, ".capbus"))
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A native plugin in an isolated DSH profile, so Make and ProfileName resolve.
func withNative(t *testing.T) (home, patchPath string) {
	t.Helper()
	home = isolatedHome(t)
	patchPath = filepath.Join(home, ".dsh", "profiles", "desktop", "node_modules", "dsh-craft", "cordis.patch.yml")
	writeFile(t, patchPath, `
- insert:
    - id: craft-mcp
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: craft
        transport: stdio
        command: '/opt/craft-cli'
        args: ['mcp', '--clean-room']
`)
	// The active profile patch is what the old flow used to hand-edit.
	writeFile(t, filepath.Join(home, ".dsh", "profiles", "desktop", "cordis.patch.yml"), "# original profile patch\n")
	return home, patchPath
}

func TestMakeResolvesNativeAndProfile(t *testing.T) {
	withNative(t)
	p, err := Make("craft")
	if err != nil {
		t.Fatal(err)
	}
	if p.Plugin != "dsh-craft" || p.Server != "craft" || p.Profile != "desktop" {
		t.Fatalf("bad plan: %+v", p)
	}
	if p.Command != "/opt/craft-cli" || len(p.Args) != 2 {
		t.Fatalf("bad command: %+v", p)
	}
}

// The core regression: commands go to DSH's own plugin manager. The original
// plugin is removed only for adopt, never for inject, and the bundle add comes
// first so the capability never disappears in between.
func TestCommandsInstallBeforeRemove(t *testing.T) {
	withNative(t)
	adopt, err := Make("craft")
	if err != nil {
		t.Fatal(err)
	}
	add, remove := adopt.Commands()
	if !strings.Contains(add, "dsh plugin --profile desktop add link:") {
		t.Fatalf("add command is not a DSH plugin install: %q", add)
	}
	if !strings.Contains(add, string(filepath.Separator)+"dsh-bundle") {
		t.Fatalf("add command does not point at the bundle: %q", add)
	}
	if !strings.Contains(remove, "dsh plugin --profile desktop remove dsh-craft") {
		t.Fatalf("remove command does not name the original plugin: %q", remove)
	}

	inject := Plan{Server: "craft", Plugin: "capbus", Profile: "desktop", FromCapability: true,
		Command: "capbus", Args: []string{"exec", "craft"}}
	_, injectRemove := inject.Commands()
	if injectRemove != "" {
		t.Fatalf("inject must not remove any plugin: %q", injectRemove)
	}
}

// Apply must declare the capability, enable it, write the bundle - and leave
// every agent profile patch byte-for-byte untouched. That is the whole point
// of the rewrite away from hand-edited cordis.patch.yml.
func TestApplyNeverEditsAgentProfile(t *testing.T) {
	home, pluginPatch := withNative(t)
	profilePatch := filepath.Join(home, ".dsh", "profiles", "desktop", "cordis.patch.yml")
	beforeProfile, _ := os.ReadFile(profilePatch)
	beforePlugin, _ := os.ReadFile(pluginPatch)

	p, err := Make("craft")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Declared {
		t.Fatalf("expected a new declaration: %+v", res)
	}
	// A freshly declared capability is enabled by default, so Enabled reports
	// "changed" and is legitimately false here. What matters is the end state:
	// the capability is on and the bundle carries it.
	st, err := enabled.Load()
	if err != nil || !st.IsEnabled("craft") {
		t.Fatalf("capability must be enabled after apply: %v %+v", err, st)
	}
	if len(res.Synced) == 0 {
		t.Fatal("sync wrote nothing")
	}

	afterProfile, _ := os.ReadFile(profilePatch)
	afterPlugin, _ := os.ReadFile(pluginPatch)
	if string(afterProfile) != string(beforeProfile) {
		t.Fatalf("profile patch was edited:\n%q\n%q", beforeProfile, afterProfile)
	}
	if string(afterPlugin) != string(beforePlugin) {
		t.Fatalf("original plugin patch was edited:\n%q\n%q", beforePlugin, afterPlugin)
	}

	manifestPath := filepath.Join(home, ".capbus", "capabilities", "craft.yaml")
	if b, err := os.ReadFile(manifestPath); err != nil || !strings.Contains(string(b), "craft") {
		t.Fatalf("capability manifest not declared: %v %q", err, b)
	}
	bundlePatch := filepath.Join(home, ".capbus", "dsh-bundle", "cordis.patch.yml")
	if b, err := os.ReadFile(bundlePatch); err != nil || !strings.Contains(string(b), "craft") {
		t.Fatalf("bundle does not carry the capability: %v %q", err, b)
	}
}

// Applying twice must not declare twice or fail: the second run sees the
// manifest already present and simply re-syncs.
func TestApplyIsIdempotent(t *testing.T) {
	withNative(t)
	p, err := Make("craft")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(p); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Declared {
		t.Fatal("second apply should not redeclare the capability")
	}
}

// Undo switches the capability off so the regenerated bundle stops carrying
// it, and prints the DSH refresh command. It never claims to have restored a
// profile patch it never touched.
func TestUndoSwitchesOffAndPrintsDSHCommand(t *testing.T) {
	withNative(t)
	p, err := Make("craft")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(p); err != nil {
		t.Fatal(err)
	}
	steps, err := Undo()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(steps, "\n")
	if !strings.Contains(joined, "switched off: craft") {
		t.Fatalf("undo did not switch the capability off: %s", joined)
	}
	if !strings.Contains(joined, "dsh plugin --profile desktop add link:") {
		t.Fatalf("undo did not print the DSH refresh command: %s", joined)
	}
	if _, err := Undo(); err == nil {
		t.Fatal("undo on an empty ledger must report nothing to undo")
	}
}
