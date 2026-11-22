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

// Agent is one place capabilities have to be written. Every agent speaks its
// own dialect; a materializer is the adapter that translates for it.
type Agent interface {
	Name() string
	// Path returns the config file to write, or false when the agent is not
	// installed on this machine.
	Path() (string, bool)
	// Dialect is the config format: "mcpServers" (Claude/Cursor family).
	Dialect() string
}

type claude struct{}

func (claude) Name() string    { return "claude-code" }
func (claude) Dialect() string { return "mcpServers" }
func (claude) Path() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	p := filepath.Join(home, ".claude.json")
	if _, err := os.Stat(p); err != nil {
		return p, false
	}
	return p, true
}

type cursor struct{}

func (cursor) Name() string    { return "cursor" }
func (cursor) Dialect() string { return "mcpServers" }
func (cursor) Path() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	p := filepath.Join(home, ".cursor", "mcp.json")
	if _, err := os.Stat(p); err != nil {
		return p, false
	}
	return p, true
}

// Agents returns every materializer this build knows about.
func Agents() []Agent { return []Agent{claude{}, cursor{}} }

// entry is the per-agent MCP descriptor shared by the "mcpServers" dialect.
type entry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Options controls how agent configs are written.
type Options struct {
	// DryRun reports what would change without touching a file.
	DryRun bool
	// Shared points global capabilities at `capbus bridge` instead of spawning
	// a backend inside every agent. This is what turns N processes into one.
	Shared bool
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
	if opts.Shared && m.Scope == manifest.ScopeGlobal {
		return entry{Command: exe, Args: []string{"bridge", m.Name}}
	}
	return entry{Command: exe, Args: []string{"exec", m.Name}}
}

// Render produces the config body for one agent. Existing keys in the config
// file are preserved; only the capability map it owns is rewritten. That keeps
// capbus from eating settings the agent wrote for itself.
func Render(a Agent, ms []manifest.Manifest, opts Options) ([]byte, error) {
	exe := selfExe()
	servers := map[string]entry{}
	for _, m := range ms {
		servers[m.Name] = toEntry(m, opts, exe)
	}

	root := map[string]any{}
	if p, ok := a.Path(); ok {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			b = bytes.TrimPrefix(b, utf8BOM)
			if err := json.Unmarshal(b, &root); err != nil {
				return nil, fmt.Errorf("%s: existing config is not valid JSON (refusing to overwrite): %w", p, err)
			}
		}
	}
	root[a.Dialect()] = servers
	return json.MarshalIndent(root, "", "  ")
}

// Sync writes every detected agent's config.
func Sync(ms []manifest.Manifest, opts Options) ([]string, []error) {
	var wrote []string
	var errs []error
	for _, a := range Agents() {
		p, ok := a.Path()
		if !ok {
			continue
		}
		body, err := Render(a, ms, opts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if opts.DryRun {
			wrote = append(wrote, fmt.Sprintf("%s (%s, %d bytes, dry-run)", a.Name(), p, len(body)))
			continue
		}
		if err := os.WriteFile(p, body, 0o600); err != nil {
			errs = append(errs, err)
			continue
		}
		wrote = append(wrote, fmt.Sprintf("%s -> %s", a.Name(), p))
	}
	sort.Strings(wrote)
	return wrote, errs
}
