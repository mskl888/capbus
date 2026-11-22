package manifest

import "testing"

func valid() Manifest {
	return Manifest{
		Name:   "github",
		Kind:   KindMCP,
		Scope:  ScopeGlobal,
		Source: Source{NPM: "@modelcontextprotocol/server-github"},
	}
}

func TestValidManifestPasses(t *testing.T) {
	m := valid()
	if err := m.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if m.Placement != "host" {
		t.Fatalf("placement should default to host, got %q", m.Placement)
	}
}

// The whole point of the scope field: a capability that holds a live session
// must not be shared machine-wide, or two agents fight over one browser.
func TestStatefulGlobalIsRejected(t *testing.T) {
	m := valid()
	m.Name = "browser"
	m.Stateful = true
	m.Scope = ScopeGlobal
	if err := m.Validate(); err == nil {
		t.Fatal("stateful + scope:global must be rejected")
	}
}

func TestStatefulWorkspaceIsAccepted(t *testing.T) {
	m := valid()
	m.Name = "browser"
	m.Stateful = true
	m.Scope = ScopeWorkspace
	if err := m.Validate(); err != nil {
		t.Fatalf("stateful + scope:workspace should pass, got %v", err)
	}
}

func TestSourceMustBeUnambiguous(t *testing.T) {
	m := valid()
	m.Source = Source{NPM: "a", OCI: "b"}
	if err := m.Validate(); err == nil {
		t.Fatal("two sources must be rejected")
	}
	m.Source = Source{}
	if err := m.Validate(); err == nil {
		t.Fatal("no source must be rejected")
	}
}

func TestKindAndScopeEnums(t *testing.T) {
	m := valid()
	m.Kind = "daemon"
	if err := m.Validate(); err == nil {
		t.Fatal("unknown kind must be rejected")
	}
	m = valid()
	m.Scope = "machine"
	if err := m.Validate(); err == nil {
		t.Fatal("unknown scope must be rejected")
	}
}
