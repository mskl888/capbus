package manifest

import (
	"strings"
	"testing"
)

// The name becomes a file name under the registry and a key in every agent's
// config, so the character set is a security boundary.
func TestNameCharset(t *testing.T) {
	good := []string{"github", "a", "my-server", "server_2", "v1.2.3", "a.b-c_d", "x9"}
	for _, n := range good {
		m := Manifest{Name: n, Kind: KindMCP, Scope: ScopeGlobal, Source: Source{Command: "/bin/true"}}
		if err := m.Validate(); err != nil {
			t.Errorf("%q should be accepted: %v", n, err)
		}
	}

	bad := []string{
		"", "..", "../etc/passwd", "a/b", `a\b`, "/abs",
		"GitHub", "has space", ".hidden", "-lead", "_lead", "a b",
		strings.Repeat("a", 64),
	}
	for _, n := range bad {
		m := Manifest{Name: n, Kind: KindMCP, Scope: ScopeGlobal, Source: Source{Command: "/bin/true"}}
		if err := m.Validate(); err == nil {
			t.Errorf("%q should be rejected", n)
		}
	}
}

// A capability process inherits what it needs to start, and nothing that looks
// like a credential, unless the manifest asks for it.
func TestResolveEnvInheritsOnlySafeNames(t *testing.T) {
	t.Setenv("CAPBUS_TEST_SECRET", "leak-me")
	t.Setenv("GITHUB_TOKEN", "also-leak")
	t.Setenv("PATH", "/usr/bin")

	m := Manifest{Name: "x", Kind: KindMCP, Scope: ScopeGlobal, Source: Source{Command: "/bin/true"}}
	env, err := m.ResolveEnv()
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "CAPBUS_TEST_SECRET=") || strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			t.Fatalf("an unrelated secret leaked into the child environment: %s", kv)
		}
	}
	if !hasEnv(env, "PATH") {
		t.Fatal("PATH must be inherited or nothing can start")
	}

	// Declaring it is how a capability gets anything else.
	m.Source.Env = map[string]string{"CAPBUS_TEST_SECRET": "wanted"}
	env, err = m.ResolveEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !hasEnv(env, "CAPBUS_TEST_SECRET") {
		t.Fatal("a declared variable must reach the child")
	}
}

func hasEnv(env []string, name string) bool {
	prefix := name + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}
