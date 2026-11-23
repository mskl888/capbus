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
	vars := inheritedEnv()
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

// safeEnvNames is everything a capability process inherits: enough for an
// ordinary program to start and find its runtime, and nothing that looks like a
// credential. Anything else - an API key exported for another tool, a cloud
// session token - must be declared in the manifest's source.env or under
// permissions.secrets, where the user can see it.
//
// The review that prompted this (2026-11-23) put it plainly: inheriting
// os.Environ() hands every variable in the user's shell to every capability.
var safeEnvNames = map[string]bool{
	// POSIX
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"PWD": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TERM": true,
	"TZ": true, "TMPDIR": true,
	// Windows: without SystemRoot many programs cannot resolve DNS or load
	// system DLLs, so these are not optional.
	"SYSTEMROOT": true, "SYSTEMDRIVE": true, "WINDIR": true, "COMSPEC": true,
	"PATHEXT": true, "NUMBER_OF_PROCESSORS": true, "PROCESSOR_ARCHITECTURE": true,
	"OS": true, "USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true,
	"APPDATA": true, "LOCALAPPDATA": true, "PROGRAMDATA": true, "PROGRAMFILES": true,
	"PROGRAMFILES(X86)": true, "COMMONPROGRAMFILES": true, "TEMP": true, "TMP": true,
}

// inheritedEnv copies the safe subset of the current environment.
func inheritedEnv() map[string]string {
	vars := map[string]string{}
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k := kv[:i]
		if safeEnvNames[strings.ToUpper(k)] {
			vars[k] = kv[i+1:]
		}
	}
	return vars
}
