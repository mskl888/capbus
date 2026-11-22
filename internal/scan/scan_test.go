package scan

import (
	"strings"
	"testing"

	"github.com/capbus/capbus/internal/manifest"
)

func mcp(name, command string) manifest.Manifest {
	return manifest.Manifest{
		Name: name, Kind: manifest.KindMCP, Scope: manifest.ScopeGlobal,
		Source: manifest.Source{Command: command},
	}
}

// A capability whose runtime is missing cannot start, and the report must say
// so rather than showing a green list.
func TestMissingRuntimeIsReported(t *testing.T) {
	got := checkRuntimes([]manifest.Manifest{mcp("broken", "capbus-command-that-does-not-exist-xyz")})
	if len(got) != 1 {
		t.Fatalf("expected one finding, got %+v", got)
	}
	if got[0].ID != "install-runtime" {
		t.Fatalf("id %q", got[0].ID)
	}
	if !strings.Contains(got[0].Items[0], "not installed") {
		t.Fatalf("detail: %v", got[0].Items)
	}
}

func TestPresentRuntimeIsQuiet(t *testing.T) {
	// `go` must exist here: the test binary was started by it or by go test.
	got := checkRuntimes([]manifest.Manifest{mcp("ok", "go")})
	if len(got) != 0 {
		t.Fatalf("unexpected finding: %+v", got)
	}
}

func TestFindingsAreSortedBySeverity(t *testing.T) {
	ordered := []Severity{High, Medium, Low, Info}
	for i := 0; i < len(ordered)-1; i++ {
		if rank(ordered[i]) >= rank(ordered[i+1]) {
			t.Fatalf("%s should rank before %s", ordered[i], ordered[i+1])
		}
	}
}
