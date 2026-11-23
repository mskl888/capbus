package manifest

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind is what a capability is.
type Kind string

const (
	KindMCP   Kind = "mcp"
	KindSkill Kind = "skill"
	KindCLI   Kind = "cli"
)

// Scope decides how many copies of a capability may run.
type Scope string

const (
	// ScopeGlobal is shared by every agent on the machine.
	ScopeGlobal Scope = "global"
	// ScopeWorkspace is one copy per workspace; required for stateful capabilities.
	ScopeWorkspace Scope = "workspace"
	// ScopeSession is one copy per task.
	ScopeSession Scope = "session"
)

type Source struct {
	OCI     string            `yaml:"oci,omitempty"`
	NPM     string            `yaml:"npm,omitempty"`
	Git     string            `yaml:"git,omitempty"`
	Path    string            `yaml:"path,omitempty"`
	Command string            `yaml:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	// Cwd is the working directory the capability expects. Servers that load
	// assets by relative path fail without it, and the failure looks like a
	// missing file rather than a missing setting.
	Cwd string `yaml:"cwd,omitempty"`
}

type Permissions struct {
	Egress  []string `yaml:"egress,omitempty"`
	Secrets []string `yaml:"secrets,omitempty"`
}

type Health struct {
	Probe    string `yaml:"probe,omitempty"`
	Interval string `yaml:"interval,omitempty"`
}

// Manifest describes one capability. It is the single source of truth: agent
// configs are generated from it, never edited by hand.
type Manifest struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Kind        Kind   `yaml:"kind"`
	Scope       Scope  `yaml:"scope"`
	// Stateful marks capabilities that hold a live session (a browser, a
	// desktop, a shell). A stateful capability must never be global: two
	// agents sharing one browser session will fight over it.
	Stateful bool `yaml:"stateful,omitempty"`
	// Shared means the capability is useless unless every agent talks to the
	// same process: a memory store, a cache, a queue. capbus writes the bridge
	// shim even without --shared, and sync refuses to write per-agent copies
	// that would silently diverge.
	Shared bool `yaml:"shared,omitempty"`
	// Always keeps a capability switched on in "default" mode: the ones you
	// want available whatever else is turned off.
	Always      bool        `yaml:"always,omitempty"`
	Placement   string      `yaml:"placement,omitempty"` // host | workspace
	Source      Source      `yaml:"source"`
	Permissions Permissions `yaml:"permissions,omitempty"`
	Health      Health      `yaml:"health,omitempty"`

	// Origin is the file the manifest was loaded from; set by the registry.
	Origin string `yaml:"-"`
}

// Load reads and validates one manifest file.
func Load(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	m.Origin = path
	if err := m.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Validate enforces the rules that keep capabilities from stepping on each
// other. These are hard errors, not warnings: a manifest that breaks them
// produces colliding agent configs.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	// The name becomes a file name (<name>.yaml under the registry) and a key
	// in every agent's config, so its character set is a security boundary and
	// not a style choice: a separator would let a manifest write outside the
	// directory it belongs to.
	if !validName(m.Name) {
		return fmt.Errorf("name must be 1-63 characters of lower-case letters, digits, dot, "+
			"dash or underscore, starting with a letter or digit; got %q", m.Name)
	}
	switch m.Kind {
	case KindMCP, KindSkill, KindCLI:
	default:
		return fmt.Errorf("kind must be one of mcp|skill|cli, got %q", m.Kind)
	}
	switch m.Scope {
	case ScopeGlobal, ScopeWorkspace, ScopeSession:
	default:
		return fmt.Errorf("scope must be one of global|workspace|session, got %q", m.Scope)
	}
	if m.Stateful && m.Scope == ScopeGlobal {
		return fmt.Errorf(
			"%q is stateful and scope:global; stateful capabilities must be workspace or session scoped "+
				"(two agents would share one live session)", m.Name)
	}
	s := m.Source
	set := 0
	for _, v := range []string{s.OCI, s.NPM, s.Git, s.Path, s.Command} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		return fmt.Errorf("source must set exactly one of oci|npm|git|path|command, got %d", set)
	}
	m.Placement = strings.ToLower(strings.TrimSpace(m.Placement))
	if m.Placement == "" {
		m.Placement = "host"
	}
	if m.Placement != "host" && m.Placement != "workspace" {
		return fmt.Errorf("placement must be host or workspace, got %q", m.Placement)
	}
	return nil
}

// Sort orders manifests by name so output and generated configs are stable.
func Sort(ms []Manifest) {
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
}

// validName is the character set a capability name may use. It is deliberately
// narrow: the name is a file name and a config key, so anything that could be
// read as a path separator is out. Starting with a letter or digit also makes
// "." and ".." impossible.
func validName(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '.' || r == '-' || r == '_') && i > 0:
		default:
			return false
		}
	}
	return true
}
