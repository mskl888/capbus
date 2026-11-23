// Package adopt moves one MCP server from an agent's own plugin into capbus.
//
// The handover is deliberately two-sided and visible. capbus only writes its
// own files: the capability manifest and the generated DSH plugin bundle. The
// user runs DSH's own `dsh plugin` commands to install that bundle and remove
// the original plugin, so both edits show up in DSH's own plugin manager and
// can be reversed there. capbus never edits an agent profile patch.
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

	"github.com/capbus/capbus/internal/enabled"
	"github.com/capbus/capbus/internal/importscan"
	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/materialize"
	"github.com/capbus/capbus/internal/registry"
)

// Plan is everything the handover needs, computed before anything is written.
type Plan struct {
	Server string
	Plugin string // DSH plugin that currently provides this server (adopt only)
	RowID  string // kept for displaying what is being taken over
	Command string
	Args    []string
	Cwd     string
	// Profile is the DSH profile name used in `dsh plugin --profile <name>`.
	Profile string
	// FromCapability is true when the capability already lives in capbus's
	// registry (inject); false when it is adopted from an agent.
	FromCapability bool
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

// ProfilePatch finds the active profile's patch file. It is used to derive
// the profile name for the printed commands, never to edit the patch.
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

// ProfileName returns the DSH profile directory name, e.g. "desktop".
func ProfileName() (string, error) {
	p, err := ProfilePatch()
	if err != nil {
		return "", err
	}
	name := filepath.Base(filepath.Dir(p))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", fmt.Errorf("cannot derive a profile name from %s", p)
	}
	return name, nil
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
	profile, err := ProfileName()
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Server:  server,
		Plugin:  found.Plugin,
		RowID:   found.RowID,
		Command: found.Command,
		Args:    found.Args,
		Cwd:     found.Cwd,
		Profile: profile,
	}, nil
}

// BundleDir is where `capbus sync` writes the DSH plugin bundle. Keep this in
// step with materialize's dsh adapter location.
func BundleDir() string { return filepath.Join(home(), "dsh-bundle") }

// Commands returns the exact DSH commands the user must run, in the order that
// keeps the capability available throughout: install the capbus bundle first,
// then (for adopt) remove the original plugin.
func (p Plan) Commands() (add, remove string) {
	add = fmt.Sprintf("dsh plugin --profile %s add link:%s", p.Profile, BundleDir())
	if !p.FromCapability && p.Plugin != "" && p.Plugin != "capbus" {
		remove = fmt.Sprintf("dsh plugin --profile %s remove %s", p.Profile, p.Plugin)
	}
	return add, remove
}

// Manifest is the capbus capability that mirrors the adopted server.
func (p Plan) Manifest() manifest.Manifest {
	return manifest.Manifest{
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
}

// PlanFromCapability builds a plan for a capability that capbus already owns,
// with no native copy to disable. This is how a capability that was never
// installed inside an agent gets into that agent.
//
// A shared capability is injected through the bundle's bridge shim, never as
// its own command: letting the agent spawn the server itself would create the
// second instance that sharing exists to prevent.
func PlanFromCapability(name string) (Plan, error) {
	ms, _ := registry.Load()
	var found *manifest.Manifest
	for _, m := range ms {
		if m.Name == name {
			c := m
			found = &c
			break
		}
	}
	if found == nil {
		return Plan{}, fmt.Errorf("no capability named %q; declare it in %s first", name, registry.Dir())
	}
	profile, err := ProfileName()
	if err != nil {
		return Plan{}, err
	}

	p := Plan{Server: name, Plugin: "capbus", Profile: profile, FromCapability: true}
	if found.Shared {
		p.Command = selfExe()
		p.Args = []string{"bridge", name}
	} else {
		bin, args, err := found.Command()
		if err != nil {
			return Plan{}, err
		}
		p.Command, p.Args = bin, args
	}
	p.Cwd = found.WorkDir()
	return p, nil
}

func selfExe() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "capbus"
}

// Result is the visible outcome of a handover: what capbus wrote in its own
// home, and the commands the user must run in DSH's own plugin manager.
type Result struct {
	Declared bool     // a capability manifest was newly written
	Enabled  bool     // the capability was switched on
	Synced   []string // files sync wrote (bundle + other agents)
	Add      string   // dsh plugin add link:<bundle>
	Remove   string   // dsh plugin remove <original>, adopt only
}

// Apply performs the handover through capbus's own visible files. It declares
// the capability, enables it, regenerates the DSH bundle (and the other
// agents' configs), and returns the DSH commands for the user to run. It never
// edits an agent profile patch or another plugin's patch.
func Apply(p Plan) (Result, error) {
	r := Result{}

	ms, errs := registry.Load()
	if len(errs) > 0 {
		return r, fmt.Errorf("cannot read capability manifests: %s", strings.Join(errTexts(errs), "; "))
	}
	declared := false
	for _, m := range ms {
		if m.Name == p.Server {
			declared = true
			break
		}
	}
	if !declared {
		m := p.Manifest()
		if err := m.Validate(); err != nil {
			return r, err
		}
		body, err := yaml.Marshal(m)
		if err != nil {
			return r, err
		}
		if err := os.MkdirAll(registry.Dir(), 0o755); err != nil {
			return r, err
		}
		if err := os.WriteFile(filepath.Join(registry.Dir(), m.Name+".yaml"), body, 0o600); err != nil {
			return r, err
		}
		r.Declared = true
		ms = append(ms, m)
	}

	st, err := enabled.Load()
	if err != nil {
		return r, err
	}
	if st.Set(p.Server, true) {
		if err := st.Save(); err != nil {
			return r, err
		}
		r.Enabled = true
	}

	out := materialize.Sync(enabled.Filter(ms), materialize.Options{})
	if out.Failed() {
		texts := make([]string, 0, len(out.Errs))
		for _, e := range out.Errs {
			texts = append(texts, e.Error())
		}
		return r, fmt.Errorf("capbus sync failed after declaring the capability: %s", strings.Join(texts, "; "))
	}
	r.Synced = out.Wrote
	r.Add, r.Remove = p.Commands()

	record := undoRecord{
		Server:  p.Server,
		Plugin:  p.Plugin,
		Profile: p.Profile,
		From:    p.FromCapability,
		At:      time.Now().Format("20060102-150405"),
	}
	uf, err := loadUndo()
	if err == nil {
		uf.Entries = append(uf.Entries, record)
		_ = saveUndo(uf)
	}
	return r, nil
}

func errTexts(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

// --- undo ledger -----------------------------------------------------------

type undoRecord struct {
	Server  string `json:"server"`
	Plugin  string `json:"plugin"`
	Profile string `json:"profile"`
	From    bool   `json:"from_capability"`
	At      string `json:"at"`
	// Backward compatibility: old hand-edit adopts stored file backups here.
	ProfileBackup string `json:"profile_backup,omitempty"`
	Package       string `json:"package,omitempty"`
	PackageBackup string `json:"package_backup,omitempty"`
	LegacyProfile string `json:"legacy_profile,omitempty"`
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

// Undo reverses the last handover. For the new visible flow it switches the
// capability off and regenerates the bundle, then returns the DSH command that
// refreshes the installed package. Legacy records with file backups are still
// restored for adopts made by older capbus versions.
func Undo() ([]string, error) {
	uf, err := loadUndo()
	if err != nil {
		return nil, err
	}
	if len(uf.Entries) == 0 {
		return nil, errors.New("nothing to undo")
	}

	var steps []string
	var disable []string
	var lastProfile string
	for i := len(uf.Entries) - 1; i >= 0; i-- {
		e := uf.Entries[i]
		if lastProfile == "" && e.Profile != "" {
			lastProfile = e.Profile
		}
		// Old hand-edit record: restore the files it backed up.
		if e.ProfileBackup != "" && e.LegacyProfile != "" {
			if b, err := os.ReadFile(e.ProfileBackup); err == nil {
				if err := os.WriteFile(e.LegacyProfile, b, 0o600); err == nil {
					steps = append(steps, "restored "+e.LegacyProfile)
				}
			}
		}
		if e.PackageBackup != "" && e.Package != "" {
			if b, err := os.ReadFile(e.PackageBackup); err == nil {
				if err := os.WriteFile(e.Package, b, 0o600); err == nil {
					steps = append(steps, "restored "+e.Package)
				}
			}
		}
		disable = append(disable, e.Server)
	}

	st, err := enabled.Load()
	if err != nil {
		return nil, err
	}
	changed := false
	for _, name := range disable {
		if st.Set(name, false) {
			changed = true
		}
	}
	if changed {
		if err := st.Save(); err != nil {
			return nil, err
		}
		steps = append(steps, "switched off: "+strings.Join(disable, ", "))

		ms, errs := registry.Load()
		if len(errs) == 0 {
			out := materialize.Sync(enabled.Filter(ms), materialize.Options{})
			steps = append(steps, out.Wrote...)
		}
	}
	uf.Entries = nil
	_ = saveUndo(uf)

	// One bundle serves every capability, so the user must refresh it in DSH
	// for the switch-off to reach that agent. Printing the command keeps the
	// change visible and reversible in DSH's own plugin manager.
	if len(disable) > 0 {
		profile := lastProfile
		if profile == "" {
			if name, err := ProfileName(); err == nil {
				profile = name
			}
		}
		if profile != "" {
			p := Plan{Server: disable[len(disable)-1], Profile: profile, FromCapability: true}
			add, _ := p.Commands()
			steps = append(steps, "run in DSH to finish: "+add)
		}
	}
	return steps, nil
}
