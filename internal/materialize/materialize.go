package materialize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/capbus/capbus/internal/manifest"
)

// utf8BOM is what Windows editors and PowerShell leave at the head of a config
// file. encoding/json rejects it, so strip it before parsing.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Options controls how agent configs are written.
type Options struct {
	// DryRun reports what would change without touching a file.
	DryRun bool
	// Shared points global capabilities at `capbus bridge` instead of spawning
	// a backend inside every agent. This is what turns N processes into one.
	Shared bool
}

// Change is what sync did to one server, in words a person can check.
type Change struct {
	Agent  string
	Name   string
	Action string // added | updated | removed | unchanged | conflict
	Detail string
}

// Rendered is one agent's plan: the bytes to write, and what changed.
type Rendered struct {
	Files   map[string][]byte
	Changes []Change
}

// Agent is one place capabilities have to be written. Every agent speaks its
// own dialect; an adapter translates for it.
type Agent interface {
	Name() string
	// Detect reports whether the agent is installed on this machine.
	Detect() bool
	// Location is the file, or the directory, this adapter writes.
	Location() string
	// Render returns the files to write. Key "" means Location itself is the
	// file; any other key is a path relative to Location.
	Render(ms []manifest.Manifest, opts Options) (Rendered, error)
}

// Agents returns every adapter this build knows about.
func Agents() []Agent { return []Agent{claude{}, cursor{}, codex{}, dsh{}} }

func selfExe() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "capbus"
}

// toEntryMap always produces a capbus shim rather than the capability's own
// command. That keeps credentials out of agent configs entirely: the shim
// resolves secrets at spawn time, in memory.
func toEntryMap(m manifest.Manifest, opts Options, exe string) map[string]any {
	verb := "exec"
	// A capability marked shared must reach one process even when the user ran
	// a plain sync: a second copy would be a second, silently divergent store.
	if m.Shared || (opts.Shared && m.Scope == manifest.ScopeGlobal) {
		verb = "bridge"
	}
	return map[string]any{"command": exe, "args": []any{verb, m.Name}}
}

// ours decides whether a server entry is a capbus shim by its shape rather than
// by a marker written into someone else's config.
//
// A marker would pollute a file we do not own, and a marker we failed to write
// would make capbus blind to its own leftovers. The shape is unambiguous: only
// capbus writes `command: <.../capbus> args: [exec|bridge, <name>]`.
func ours(e map[string]any) bool {
	cmd, _ := e["command"].(string)
	if cmd == "" {
		return false
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(cmd)), ".exe")
	if base != "capbus" {
		return false
	}
	switch args := e["args"].(type) {
	case []any:
		if len(args) == 0 {
			return false
		}
		verb, _ := args[0].(string)
		return verb == "exec" || verb == "bridge"
	case []string:
		if len(args) == 0 {
			return false
		}
		return args[0] == "exec" || args[0] == "bridge"
	}
	return false
}

// mergeServers applies the manifests to a server table already in the agent's
// config. The rules, in order of how much they protect the user:
//
//  1. An entry capbus did not write is never modified or removed, whatever its
//     name. If a manifest wants that name, the collision is reported and the
//     manifest loses.
//  2. An entry capbus did write is updated to match the manifest, or removed
//     when the capability is no longer declared.
//  3. Everything else is left byte for byte as it was.
func mergeServers(existing map[string]any, ms []manifest.Manifest, opts Options, exe string) (map[string]any, []Change) {
	want := make(map[string]manifest.Manifest, len(ms))
	for _, m := range ms {
		want[m.Name] = m
	}

	merged := make(map[string]any, len(existing)+len(ms))
	for name, v := range existing {
		merged[name] = v
	}

	var changes []Change

	// Remove what capbus wrote before but no longer declares.
	for name, v := range existing {
		if _, declared := want[name]; declared {
			continue
		}
		tbl, ok := v.(map[string]any)
		if !ok || !ours(tbl) {
			// Someone else's entry: it stays, and a dry run says so, because
			// "your own servers are still there" is the thing to check after a
			// sync, not the absence of an error.
			if opts.DryRun {
				changes = append(changes, Change{Name: name, Action: "kept",
					Detail: "not written by capbus; left untouched"})
			}
			continue
		}
		delete(merged, name)
		changes = append(changes, Change{Name: name, Action: "removed"})
	}

	// Apply the manifest.
	for _, m := range ms {
		next := toEntryMap(m, opts, exe)
		cur, exists := merged[m.Name]
		if !exists {
			merged[m.Name] = next
			changes = append(changes, Change{Name: m.Name, Action: "added"})
			continue
		}
		curTbl, ok := cur.(map[string]any)
		if !ok {
			changes = append(changes, Change{Name: m.Name, Action: "conflict",
				Detail: "同名条目不是 capbus 写的，且不是表结构；已跳过"})
			continue
		}
		if !ours(curTbl) {
			changes = append(changes, Change{Name: m.Name, Action: "conflict",
				Detail: "同名条目不是 capbus 写的；已保留用户的，跳过这份清单"})
			continue
		}
		if sameEntry(curTbl, next) {
			changes = append(changes, Change{Name: m.Name, Action: "unchanged"})
			continue
		}
		merged[m.Name] = next
		changes = append(changes, Change{Name: m.Name, Action: "updated"})
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return merged, changes
}

// sameEntry compares only the fields capbus owns, so an agent's own extra keys
// on a capbus entry do not make it look changed on every run.
func sameEntry(a, b map[string]any) bool {
	ac, _ := a["command"].(string)
	bc, _ := b["command"].(string)
	if ac != bc {
		return false
	}
	return fmt.Sprint(a["args"]) == fmt.Sprint(b["args"])
}

// home is ~/.capbus, or $CAPBUS_HOME.
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

func userHome() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return "."
	}
	return h
}

// write is one file sync intends to replace.
type write struct {
	agent string
	path  string
	body  []byte
	prev  []byte // nil when the file did not exist
	had   bool
}

// Sync writes every detected agent's config.
//
// It plans every agent first and only then writes, so a render error in one
// agent cannot leave another half-updated. Each file goes through a temporary
// file and a rename, and a failure part-way restores every file already
// touched — that is acceptance criteria 1 and 2 of the 2026-11-23 review.
func Sync(ms []manifest.Manifest, opts Options) ([]string, []Change, []error) {
	var errs []error
	var changes []Change
	var writes []write

	for _, a := range Agents() {
		if !a.Detect() {
			continue
		}
		r, err := a.Render(ms, opts)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
			continue
		}
		for _, c := range r.Changes {
			c.Agent = a.Name()
			changes = append(changes, c)
		}
		keys := make([]string, 0, len(r.Files))
		for k := range r.Files {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, rel := range keys {
			p := a.Location()
			if rel != "" {
				p = filepath.Join(p, rel)
			}
			writes = append(writes, write{agent: a.Name(), path: p, body: r.Files[rel]})
		}
	}

	if opts.DryRun {
		var planned []string
		for _, w := range writes {
			planned = append(planned, fmt.Sprintf("%s -> %s (dry-run)", w.agent, w.path))
		}
		sort.Strings(planned)
		sort.Slice(changes, func(i, j int) bool {
			if changes[i].Agent != changes[j].Agent {
				return changes[i].Agent < changes[j].Agent
			}
			return changes[i].Name < changes[j].Name
		})
		return planned, changes, errs
	}

	var wrote []string
	for i := range writes {
		w := &writes[i]
		if prev, err := os.ReadFile(w.path); err == nil {
			w.prev, w.had = prev, true
		}
		if bytes.Equal(w.prev, w.body) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", w.agent, err))
			rollback(writes[:i])
			return wrote, changes, errs
		}
		if err := writeAtomic(w.path, w.body); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", w.agent, err))
			rollback(writes[:i])
			return wrote, changes, errs
		}
		wrote = append(wrote, fmt.Sprintf("%s -> %s", w.agent, w.path))
	}

	sort.Strings(wrote)
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Agent != changes[j].Agent {
			return changes[i].Agent < changes[j].Agent
		}
		return changes[i].Name < changes[j].Name
	})
	return wrote, changes, errs
}

// writeAtomic replaces path in one step: a reader sees either the old file or
// the new one, never a half-written one.
func writeAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".capbus-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename succeeds

	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// rollback puts every file in done back the way it was. It is best effort: the
// caller is already returning an error, and a rollback that fails would only
// hide the original one.
func rollback(done []write) {
	for i := len(done) - 1; i >= 0; i-- {
		w := done[i]
		if w.had {
			_ = writeAtomic(w.path, w.prev)
			continue
		}
		_ = os.Remove(w.path)
	}
}

// readJSONObject reads an existing JSON config, preserving every key the agent
// wrote for itself. A file we cannot parse is never overwritten.
func readJSONObject(path string) (map[string]any, error) {
	root := map[string]any{}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return root, nil
	}
	b = bytes.TrimPrefix(b, utf8BOM)
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("%s: existing config is not valid JSON (refusing to overwrite): %w", path, err)
	}
	return root, nil
}
