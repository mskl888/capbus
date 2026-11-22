// Package enabled records which capabilities are switched on.
//
// The rule that makes this worth having: a disabled capability is not written
// into any agent's config at all, so its tool schemas never enter a model's
// context. Filtering the config after the fact would not save a single token.
package enabled

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/capbus/capbus/internal/manifest"
)

// State is the persistent on/off set. Keeping it separate from the manifests
// means regenerating or importing a manifest never silently re-enables it.
type State struct {
	Disabled []string `json:"disabled"`
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

func Path() string { return filepath.Join(home(), "enabled.json") }

// Load reads the state; a missing file means everything is on.
func Load() (*State, error) {
	b, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	sort.Strings(s.Disabled)
	return &s, nil
}

func (s *State) Save() error {
	if err := os.MkdirAll(home(), 0o700); err != nil {
		return err
	}
	sort.Strings(s.Disabled)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

func (s *State) IsEnabled(name string) bool {
	for _, d := range s.Disabled {
		if d == name {
			return false
		}
	}
	return true
}

// Set switches one capability and reports whether anything changed.
func (s *State) Set(name string, on bool) bool {
	had := s.IsEnabled(name)
	var out []string
	for _, d := range s.Disabled {
		if d != name {
			out = append(out, d)
		}
	}
	if !on {
		out = append(out, name)
	}
	s.Disabled = out
	return had != on
}

// Filter keeps only the capabilities that are switched on.
func Filter(ms []manifest.Manifest) []manifest.Manifest {
	st, err := Load()
	if err != nil {
		return ms
	}
	var out []manifest.Manifest
	for _, m := range ms {
		if st.IsEnabled(m.Name) {
			out = append(out, m)
		}
	}
	return out
}
