package manifest

import (
	"fmt"
	"strings"
)

// Command resolves the manifest's source into a concrete process to spawn.
func (m *Manifest) Command() (string, []string, error) {
	s := m.Source
	switch {
	case s.NPM != "":
		return "npx", append([]string{"-y", s.NPM}, s.Args...), nil
	case s.OCI != "":
		return "docker", append([]string{"run", "-i", "--rm", s.OCI}, s.Args...), nil
	case s.Path != "":
		return s.Path, s.Args, nil
	case s.Command != "":
		return s.Command, s.Args, nil
	case s.Git != "":
		return "", nil, fmt.Errorf("%s: git sources are not supported yet", m.Name)
	}
	return "", nil, fmt.Errorf("%s: no source", m.Name)
}

// EnvList returns the extra environment a capability process needs, in
// os/exec form ("KEY=value").
func (m *Manifest) EnvList() []string {
	out := make([]string, 0, len(m.Source.Env))
	for k, v := range m.Source.Env {
		out = append(out, k+"="+v)
	}
	return out
}

// Executable returns a runnable path for the capability, used by doctor output.
func (m *Manifest) Executable() string {
	name, args, err := m.Command()
	if err != nil {
		return "-"
	}
	return strings.Join(append([]string{name}, args...), " ")
}
