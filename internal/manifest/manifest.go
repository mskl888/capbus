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
	Stateful    bool        `yaml:"stateful,omitempty"`
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
	if m.Name != strings.ToLower(m.Name) || strings.ContainsAny(m.Name, " \t") {
		return fmt.Errorf("name must be lower-case with no spaces, got %q", m.Name)
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
