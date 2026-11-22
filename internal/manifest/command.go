package manifest

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/capbus/capbus/internal/secret"
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

// WorkDir returns the directory the capability should start in, expanded.
// An empty value means "inherit", which is what most servers want.
func (m *Manifest) WorkDir() string {
	d := strings.TrimSpace(m.Source.Cwd)
	if d == "" {
		return ""
	}
	return os.ExpandEnv(d)
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

// ResolveEnv returns the complete environment a capability process should get:
// the inherited environment, the manifest's static env, and every secret it
// declares. Secrets come from the encrypted store, so no agent config ever
// holds a credential.
func (m *Manifest) ResolveEnv() ([]string, error) {
	vars := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			vars[kv[:i]] = kv[i+1:]
		}
	}
	// Manifest env must override the inherited value. Appending would not do
	// it: libc's getenv returns the first match, so the inherited one wins.
	for k, v := range m.Source.Env {
		vars[k] = v
	}

	if len(m.Permissions.Secrets) > 0 {
		st, err := secret.Open()
		if err != nil {
			return nil, err
		}
		vals, missing, err := st.Resolve(m.Permissions.Secrets)
		if err != nil {
			return nil, err
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("%s: missing secret(s) %s - set with `capbus secret set <NAME>`",
				m.Name, strings.Join(missing, ", "))
		}
		for k, v := range vals {
			vars[k] = v
		}
	}

	out := make([]string, 0, len(vars))
	for k, v := range vars {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out, nil
}

// Executable returns a runnable path for the capability, used by doctor output.
func (m *Manifest) Executable() string {
	name, args, err := m.Command()
	if err != nil {
		return "-"
	}
	return strings.Join(append([]string{name}, args...), " ")
}
