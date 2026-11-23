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
	Action string // added | updated | removed | unchanged | kept | conflict
	Detail string
}

// Rendered is one agent's plan: the bytes to write, and what changed.
type Rendered struct {
	Files   map[string][]byte
	Changes []Change
	// Claimed is the set of server names capbus may own in this agent once the
	// write lands. It is what the ledger records next time, so a name that
	// collided - and was therefore never written - must not appear here.
	Claimed []string
}

// Agent is one place capabilities have to be written.
type Agent interface {
	Name() string
	Detect() bool
	Location() string
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
// command. That keeps credentials out of agent configs entirely.
func toEntryMap(m manifest.Manifest, opts Options, exe string) map[string]any {
	verb := "exec"
	if m.Shared || (opts.Shared && m.Scope == manifest.ScopeGlobal) {
		verb = "bridge"
	}
	return map[string]any{"command": exe, "args": []any{verb, m.Name}}
}

// looksOurs decides whether a server entry has the shape this capbus writes:
// the same executable, and an exec or bridge verb. Comparing against the
// running binary rather than a hard-coded name keeps the check honest when
// capbus is built under another name.
func looksOurs(e map[string]any, exe string) bool {
	cmd, _ := e["command"].(string)
	if cmd == "" || exe == "" {
		return false
	}
	if !strings.EqualFold(filepath.Base(cmd), filepath.Base(exe)) {
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

// --- ledger: what capbus itself wrote ---------------------------------------

// The shape alone is not enough to decide ownership: a user can write a capbus
// shaped entry by hand, and deleting it because some manifest stopped declaring
// that name would be a surprise. The ledger records the names capbus actually
// wrote, per agent, so removal is exact.
//
// A missing ledger means "we have no evidence": nothing is removed, which
// leaves an orphan rather than deleting something we cannot account for.
func ledgerPath() string { return filepath.Join(home(), "written.json") }

func loadLedger() map[string][]string {
	out := map[string][]string{}
	b, err := os.ReadFile(ledgerPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func saveLedger(l map[string][]string) error {
	if err := os.MkdirAll(home(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	// Same rule as an agent config: never leave the ledger half written, or a
	// later run reads ownership that was never recorded.
	return writeAtomic(ledgerPath(), append(b, '\n'))
}

func ownedSet(l map[string][]string, agent string) map[string]bool {
	set := map[string]bool{}
	for _, n := range l[agent] {
		set[n] = true
	}
	return set
}

// mergeServers applies the manifests to a server table already in the agent's
// config. The rules, in order of how much they protect the user:
//
//  1. An entry capbus did not write is never modified or removed, whatever its
//     name. If a manifest wants that name, the collision is reported and the
//     manifest loses.
//  2. An entry capbus did write (shape and ledger) is updated to match the
//     manifest, or removed when the capability is no longer declared.
//  3. Everything else is left as it was.
func mergeServers(existing map[string]any, ms []manifest.Manifest, opts Options, exe string, owned map[string]bool) (map[string]any, []Change, []string) {
	want := make(map[string]manifest.Manifest, len(ms))
	for _, m := range ms {
		want[m.Name] = m
	}

	merged := make(map[string]any, len(existing)+len(ms))
	for name, v := range existing {
		merged[name] = v
	}

	var changes []Change
	var claimed []string

	for name, v := range existing {
		if _, declared := want[name]; declared {
			continue
		}
		tbl, ok := v.(map[string]any)
		if !ok || !looksOurs(tbl, exe) {
			if opts.DryRun {
				changes = append(changes, Change{Name: name, Action: "kept",
					Detail: "not written by capbus; left untouched"})
			}
			continue
		}
		if !owned[name] {
			// Shaped like ours, but capbus has no record of writing it: a hand
			// written entry, or an orphan from a capbus that kept no ledger.
			changes = append(changes, Change{Name: name, Action: "kept",
				Detail: "looks like a capbus entry but is not in capbus's ledger; left untouched"})
			continue
		}
		delete(merged, name)
		changes = append(changes, Change{Name: name, Action: "removed"})
	}

	for _, m := range ms {
		next := toEntryMap(m, opts, exe)
		cur, exists := merged[m.Name]
		if !exists {
			merged[m.Name] = next
			changes = append(changes, Change{Name: m.Name, Action: "added"})
			claimed = append(claimed, m.Name)
			continue
		}
		curTbl, ok := cur.(map[string]any)
		if !ok {
			changes = append(changes, Change{Name: m.Name, Action: "conflict",
				Detail: "an entry with this name is not a table; left as it is, capability not written"})
			continue
		}
		// Both conditions, not either: the entry must look like ours AND the
		// ledger must say capbus wrote it. Shape alone would adopt a user's
		// hand-written entry of the same name and overwrite its arguments.
		if !looksOurs(curTbl, exe) || !owned[m.Name] {
			changes = append(changes, Change{Name: m.Name, Action: "conflict",
				Detail: "an entry with this name was not written by capbus; kept, capability not written"})
			continue
		}
		if sameEntry(curTbl, next) {
			changes = append(changes, Change{Name: m.Name, Action: "unchanged"})
			claimed = append(claimed, m.Name)
			continue
		}
		merged[m.Name] = next
		changes = append(changes, Change{Name: m.Name, Action: "updated"})
		claimed = append(claimed, m.Name)
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	sort.Strings(claimed)
	return merged, changes, claimed
}

// sameEntry compares the fields capbus owns, so an agent's own extra keys on a
// capbus entry do not make it look changed on every run.
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
	prev  []byte
	had   bool
}

// Outcome is what a sync did, in the four states a caller needs to tell apart:
// whether any agent was found at all, which files were written, what changed,
// and what failed.
type Outcome struct {
	Detected int
	Wrote    []string
	Changes  []Change
	Errs     []error
}

// Failed reports whether the sync could not finish. A sync that planned fine
// and found nothing to change is not a failure.
func (o Outcome) Failed() bool { return len(o.Errs) > 0 }

// Conflicts lists the names whose manifest could not be written because
// something else already owns that name.
func (o Outcome) Conflicts() []string {
	var out []string
	for _, c := range o.Changes {
		if c.Action == "conflict" {
			out = append(out, c.Agent+"/"+c.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Sync writes every detected agent's config.
//
// It plans every agent first and writes only if the whole plan succeeded: a
// single manifest or render error leaves every agent's config exactly as it
// was. Each file goes through a temporary file and a rename, and a failure
// part-way restores every file already touched.
func Sync(ms []manifest.Manifest, opts Options) Outcome {
	out := Outcome{}

	var writes []write
	claimed := map[string][]string{}

	for _, a := range Agents() {
		if !a.Detect() {
			continue
		}
		out.Detected++

		r, err := a.Render(ms, opts)
		if err != nil {
			out.Errs = append(out.Errs, fmt.Errorf("%s: %w", a.Name(), err))
			continue
		}
		for _, c := range r.Changes {
			c.Agent = a.Name()
			out.Changes = append(out.Changes, c)
		}
		claimed[a.Name()] = r.Claimed
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

	sort.Slice(out.Changes, func(i, j int) bool {
		if out.Changes[i].Agent != out.Changes[j].Agent {
			return out.Changes[i].Agent < out.Changes[j].Agent
		}
		return out.Changes[i].Name < out.Changes[j].Name
	})

	// A plan that failed writes nothing. Writing the agents that happened to
	// render would leave the machine in a state no single decision produced.
	if len(out.Errs) > 0 {
		return out
	}

	// A name collision is a plan failure too: the capability cannot be written,
	// and writing the others would make the exit status a lie about how much of
	// the manifest is actually in effect.
	if c := out.Conflicts(); len(c) > 0 {
		out.Errs = append(out.Errs, fmt.Errorf(
			"name collision(s), nothing was written: %s - rename the capability, or remove the server "+
				"the user already has under that name", strings.Join(c, ", ")))
		return out
	}

	if opts.DryRun {
		for _, w := range writes {
			out.Wrote = append(out.Wrote, fmt.Sprintf("%s -> %s (dry-run)", w.agent, w.path))
		}
		sort.Strings(out.Wrote)
		return out
	}

	for i := range writes {
		w := &writes[i]
		prev, err := os.ReadFile(w.path)
		switch {
		case err == nil:
			w.prev, w.had = prev, true
		case os.IsNotExist(err):
			// A new file: nothing to preserve.
		default:
			// We cannot read what is there, so we cannot promise to restore it.
			// Stop before touching anything.
			out.Errs = append(out.Errs, fmt.Errorf("%s: cannot read %s (refusing to overwrite it blind): %w",
				w.agent, w.path, err))
			rollback(writes[:i])
			out.Wrote = nil
			return out
		}
		if w.had && bytes.Equal(w.prev, w.body) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			out.Errs = append(out.Errs, fmt.Errorf("%s: %w", w.agent, err))
			rollback(writes[:i])
			out.Wrote = nil
			return out
		}
		if err := writeAtomic(w.path, w.body); err != nil {
			out.Errs = append(out.Errs, fmt.Errorf("%s: %w", w.agent, err))
			rollback(writes[:i])
			out.Wrote = nil
			return out
		}
		out.Wrote = append(out.Wrote, fmt.Sprintf("%s -> %s", w.agent, w.path))
	}

	// Only now is it true that capbus wrote these names - and only the names it
	// actually claimed. A conflicted name was never written, so recording it
	// would give a later run a false reason to delete the user's entry.
	if err := saveLedger(claimed); err != nil {
		// Config entries without their ownership records would make the next sync
		// reject capbus's own just-written entries as user-owned collisions. Roll
		// the config transaction back so fixing the ledger destination is enough
		// for a clean retry.
		rollback(writes)
		out.Wrote = nil
		out.Errs = append(out.Errs, fmt.Errorf(
			"the ownership ledger could not be saved, so the agent config changes were rolled back: %w "+
				"(fix the ledger destination and run `capbus sync` again)", err))
	}

	sort.Strings(out.Wrote)
	return out
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

// rollback puts every file in done back the way it was.
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
	switch {
	case os.IsNotExist(err):
		return root, nil
	case err != nil:
		// Readable is a precondition for replacing it: an unreadable file is
		// not an empty one, and overwriting it would lose whatever is inside.
		return nil, fmt.Errorf("%s: cannot read the existing config (refusing to overwrite blind): %w", path, err)
	case len(b) == 0:
		return root, nil
	}
	b = bytes.TrimPrefix(b, utf8BOM)
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("%s: existing config is not valid JSON (refusing to overwrite): %w", path, err)
	}
	return root, nil
}
