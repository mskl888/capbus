// Package adopt moves one MCP server from an agent's own plugin into capbus.
//
// The hard part is not copying the command, it is the handover: until the
// agent stops loading its own copy, the capbus switch does nothing, and if the
// agent stops loading before capbus's entry is in place the capability
// disappears. Both edits therefore land before the agent next starts.
package adopt

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/capbus/capbus/internal/importscan"
	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/registry"
)

// Plan is everything the handover needs, computed before anything is written.
type Plan struct {
	Server       string
	Plugin       string
	RowID        string
	Command      string
	Args         []string
	Cwd          string
	ProfilePatch string
	PackagePatch string
}

func home() string {
	if d := os.Getenv("CAPBUS_HOME"); d != "" {
		return d
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return "."
	}
	return filepath.Join(h, ".capbus")
}

// ProfilePatch finds the active profile's patch file.
func ProfilePatch() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	matches, _ := filepath.Glob(filepath.Join(h, ".dsh", "profiles", "*", "cordis.patch.yml"))
	var live []string
	for _, m := range matches {
		if strings.Contains(m, ".bak") {
			continue
		}
		live = append(live, m)
	}
	switch len(live) {
	case 0:
		return "", errors.New("no DSH profile patch found; is DSH installed?")
	case 1:
		return live[0], nil
	}
	sort.Strings(live)
	return live[0], nil
}

// Make finds the plugin that currently provides this MCP server and works out
// what a handover would involve. It writes nothing.
func Make(server string) (Plan, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return Plan{}, err
	}
	var found *importscan.Native
	for _, n := range importscan.Natives(h) {
		if n.Server == server {
			c := n
			found = &c
			break
		}
	}
	if found == nil {
		return Plan{}, fmt.Errorf("no MCP server named %q is installed inside an agent; nothing to adopt", server)
	}
	if !found.Resolved {
		return Plan{}, fmt.Errorf(
			"%q is launched by a computed command, so capbus cannot copy it; write a manifest by hand instead", server)
	}
	profile, err := ProfilePatch()
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Server:       server,
		Plugin:       found.Plugin,
		RowID:        found.RowID,
		Command:      found.Command,
		Args:         found.Args,
		Cwd:          found.Cwd,
		ProfilePatch: profile,
		PackagePatch: found.PatchPath,
	}, nil
}

func q(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Block is the entry appended to the profile patch.
func (p Plan) Block() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n# >>> capbus: %s (managed; do not edit between the markers) >>>\n", p.Server)
	fmt.Fprintf(&b, "- id: %s\n", q("capbus-"+p.Server))
	fmt.Fprintf(&b, "  name: %s\n", q("@deepseek-ai/dsh-mcp-client"))
	b.WriteString("  config:\n")
	fmt.Fprintf(&b, "    serverName: %s\n", q(p.Server))
	b.WriteString("    transport: stdio\n")
	fmt.Fprintf(&b, "    command: %s\n", q(p.Command))
	if len(p.Args) > 0 {
		b.WriteString("    args:\n")
		for _, a := range p.Args {
			fmt.Fprintf(&b, "      - %s\n", q(a))
		}
	}
	if p.Cwd != "" {
		fmt.Fprintf(&b, "    cwd: %s\n", q(p.Cwd))
	}
	b.WriteString("    failOnStartupError: false\n")
	fmt.Fprintf(&b, "# <<< capbus: %s <<<\n", p.Server)
	return b.String()
}

// Manifest is the capbus capability that mirrors the adopted server.
func (p Plan) Manifest() manifest.Manifest {
	m := manifest.Manifest{
		Name:        p.Server,
		Description: "Adopted from " + p.Plugin,
		Kind:        manifest.KindMCP,
		Scope:       manifest.ScopeGlobal,
		Source: manifest.Source{
			Command: p.Command,
			Args:    p.Args,
			Cwd:     p.Cwd,
		},
	}
	return m
}

type undoRecord struct {
	Server        string `json:"server"`
	Profile       string `json:"profile"`
	ProfileBackup string `json:"profile_backup"`
	Package       string `json:"package"`
	PackageBackup string `json:"package_backup"`
	At            string `json:"at"`
}

type undoFile struct {
	Entries []undoRecord `json:"entries"`
}

func undoPath() string { return filepath.Join(home(), "adopt-undo.json") }

func loadUndo() (*undoFile, error) {
	f := &undoFile{}
	b, err := os.ReadFile(undoPath())
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, err
	}
	return f, nil
}

func saveUndo(f *undoFile) error {
	if err := os.MkdirAll(home(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(undoPath(), b, 0o600)
}

// Apply performs the handover. Every file it touches is backed up first, and
// the record needed to reverse it is written before the second edit lands.
func Apply(p Plan) ([]string, error) {
	stamp := time.Now().Format("20060102-150405")

	profileBody, err := os.ReadFile(p.ProfilePatch)
	if err != nil {
		return nil, fmt.Errorf("read profile patch: %w", err)
	}
	pkgBody, err := os.ReadFile(p.PackagePatch)
	if err != nil {
		return nil, fmt.Errorf("read plugin patch: %w", err)
	}

	profileBackup := p.ProfilePatch + ".capbus-backup-" + stamp
	pkgBackup := p.PackagePatch + ".capbus-backup-" + stamp
	if err := os.WriteFile(profileBackup, profileBody, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(pkgBackup, pkgBody, 0o600); err != nil {
		return nil, err
	}

	var steps []string
	steps = append(steps, "backed up "+profileBackup, "backed up "+pkgBackup)

	// 1. capbus's entry goes in first: the capability must exist before the
	//    original stops being loaded.
	updated := stripBlock(string(profileBody), p.Server) + p.Block()
	if err := os.WriteFile(p.ProfilePatch, []byte(updated), 0o600); err != nil {
		return nil, err
	}
	steps = append(steps, "added capbus entry to "+p.ProfilePatch)

	// 2. then the original row is switched off.
	disabled, changed := disableRow(string(pkgBody), p.RowID)
	if !changed {
		steps = append(steps, "warning: could not find row "+p.RowID+" to disable; do it by hand")
	} else if err := os.WriteFile(p.PackagePatch, []byte(disabled), 0o600); err != nil {
		return nil, err
	} else {
		steps = append(steps, "disabled the original row in "+p.PackagePatch)
	}

	// 3. capbus's own capability, so exec and the panel can see it.
	m := p.Manifest()
	if err := m.Validate(); err == nil {
		if body, err := yaml.Marshal(m); err == nil {
			if err := os.MkdirAll(registry.Dir(), 0o755); err == nil {
				if err := os.WriteFile(filepath.Join(registry.Dir(), m.Name+".yaml"), body, 0o600); err == nil {
					steps = append(steps, "declared capability "+m.Name)
				}
			}
		}
	}

	uf, err := loadUndo()
	if err == nil {
		uf.Entries = append(uf.Entries, undoRecord{
			Server: p.Server, Profile: p.ProfilePatch, ProfileBackup: profileBackup,
			Package: p.PackagePatch, PackageBackup: pkgBackup, At: stamp,
		})
		_ = saveUndo(uf)
	}
	return steps, nil
}

// stripBlock removes a previously added capbus block so Apply is idempotent.
func stripBlock(body, server string) string {
	start := fmt.Sprintf("# >>> capbus: %s (managed", server)
	end := fmt.Sprintf("# <<< capbus: %s <<<", server)
	lines := strings.Split(body, "\n")
	var out []string
	skipping := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, start) {
			skipping = true
			continue
		}
		if skipping {
			if trimmed == end {
				skipping = false
			}
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// disableRow inserts `disabled: true` under the given row id, preserving every
// other byte of the file including its comments.
func disableRow(body, rowID string) (string, bool) {
	if rowID == "" {
		return body, false
	}
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if !strings.HasPrefix(trimmed, "- id:") {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(trimmed, "- id:"))
		id = strings.Trim(id, "'\"")
		if id != rowID {
			continue
		}
		indent := l[:len(l)-len(strings.TrimLeft(l, " "))]
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "disabled: true" {
				return body, true // already off
			}
			if strings.HasPrefix(strings.TrimSpace(next), "- id:") {
				break
			}
		}
		out := append([]string{}, lines[:i+1]...)
		out = append(out, indent+"  disabled: true")
		out = append(out, lines[i+1:]...)
		return strings.Join(out, "\n"), true
	}
	return body, false
}

// Undo restores every file the last adopt changed, newest first.
func Undo() ([]string, error) {
	uf, err := loadUndo()
	if err != nil {
		return nil, err
	}
	if len(uf.Entries) == 0 {
		return nil, errors.New("nothing to undo")
	}
	var steps []string
	var keep []undoRecord
	for i := len(uf.Entries) - 1; i >= 0; i-- {
		e := uf.Entries[i]
		if b, err := os.ReadFile(e.ProfileBackup); err == nil {
			if err := os.WriteFile(e.Profile, b, 0o600); err == nil {
				steps = append(steps, "restored "+e.Profile)
			}
		}
		if b, err := os.ReadFile(e.PackageBackup); err == nil {
			if err := os.WriteFile(e.Package, b, 0o600); err == nil {
				steps = append(steps, "restored "+e.Package)
			}
		}
		keep = append(keep, e)
	}
	uf.Entries = nil
	_ = saveUndo(uf)
	return steps, nil
}
