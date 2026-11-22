package materialize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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

// Agent is one place capabilities have to be written. Every agent speaks its
// own dialect; an adapter translates for it.
type Agent interface {
	Name() string
	// Detect reports whether the agent is installed on this machine.
	Detect() bool
	// Location is the file, or the directory, this adapter writes.
	Location() string
	// Render returns the files to write. Key "" means Location itself is the
	// file; any other key is a path relative to Location. Multi-file agents
	// (DSH bundles) use this; single-file agents return exactly one entry.
	Render(ms []manifest.Manifest, opts Options) (map[string][]byte, error)
}

// Agents returns every adapter this build knows about.
func Agents() []Agent { return []Agent{claude{}, cursor{}, codex{}, dsh{}} }

// entry is the per-agent MCP descriptor used by the "mcpServers" dialects.
type entry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func selfExe() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "capbus"
}

// toEntry always produces a capbus shim rather than the capability's own
// command. That keeps credentials out of agent configs entirely: the shim
// resolves secrets at spawn time, in memory.
func toEntry(m manifest.Manifest, opts Options, exe string) entry {
	// A capability marked shared must reach one process even when the user ran
	// a plain sync: a second copy would be a second, silently divergent store.
	if m.Shared || (opts.Shared && m.Scope == manifest.ScopeGlobal) {
		return entry{Command: exe, Args: []string{"bridge", m.Name}}
	}
	return entry{Command: exe, Args: []string{"exec", m.Name}}
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

// Sync writes every detected agent's config, then reports what it wrote.
func Sync(ms []manifest.Manifest, opts Options) ([]string, []error) {
	var wrote []string
	var errs []error

	for _, a := range Agents() {
		if !a.Detect() {
			continue
		}
		files, err := a.Render(ms, opts)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
			continue
		}
		keys := make([]string, 0, len(files))
		for k := range files {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, rel := range keys {
			body := files[rel]
			p := a.Location()
			if rel != "" {
				p = filepath.Join(p, rel)
			}
			if opts.DryRun {
				wrote = append(wrote, fmt.Sprintf("%s (%s, %d bytes, dry-run)", a.Name(), p, len(body)))
				continue
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := os.WriteFile(p, body, 0o600); err != nil {
				errs = append(errs, err)
				continue
			}
			wrote = append(wrote, fmt.Sprintf("%s -> %s", a.Name(), p))
		}
	}
	sort.Strings(wrote)
	return wrote, errs
}

// readJSONObject merges into an existing JSON config, preserving every key the
// agent wrote for itself. A file we cannot parse is never overwritten.
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
