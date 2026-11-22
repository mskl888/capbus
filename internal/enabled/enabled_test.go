package enabled

import (
	"path/filepath"
	"testing"

	"github.com/capbus/capbus/internal/manifest"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("CAPBUS_HOME", filepath.Join(t.TempDir(), ".capbus"))
}

func caps(names ...string) []manifest.Manifest {
	out := make([]manifest.Manifest, 0, len(names))
	for _, n := range names {
		out = append(out, manifest.Manifest{Name: n, Kind: manifest.KindMCP, Scope: manifest.ScopeGlobal})
	}
	return out
}

func TestEverythingIsOnByDefault(t *testing.T) {
	isolate(t)
	got := Filter(caps("a", "b"))
	if len(got) != 2 {
		t.Fatalf("expected both enabled, got %d", len(got))
	}
}

func TestDisabledIsNotFiltered(t *testing.T) {
	isolate(t)
	st, _ := Load()
	st.Set("b", false)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	got := Filter(caps("a", "b"))
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("expected only a, got %+v", got)
	}
}

func TestReEnableSurvivesReload(t *testing.T) {
	isolate(t)
	st, _ := Load()
	st.Set("b", false)
	_ = st.Save()

	st2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if st2.IsEnabled("b") {
		t.Fatal("b should be off after reload")
	}
	st2.Set("b", true)
	if err := st2.Save(); err != nil {
		t.Fatal(err)
	}
	if got := Filter(caps("a", "b")); len(got) != 2 {
		t.Fatalf("b should be back on, got %+v", got)
	}
}

func TestSetIsIdempotent(t *testing.T) {
	isolate(t)
	st, _ := Load()
	st.Set("a", false)
	st.Set("a", false)
	if len(st.Disabled) != 1 {
		t.Fatalf("duplicate entries: %v", st.Disabled)
	}
}

// A capability may be switched off before it is declared, and that must not
// leak into the file as a duplicate when it is switched off again later.
func TestFilterKeepsOrder(t *testing.T) {
	isolate(t)
	got := Filter(caps("z", "a", "m"))
	if got[0].Name != "z" || got[2].Name != "m" {
		t.Fatalf("order changed: %+v", got)
	}
}
