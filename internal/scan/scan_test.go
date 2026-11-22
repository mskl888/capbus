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

// craft-anything's own tool names are the evidence: it holds a target and a
// session, so sharing one instance across agents corrupts both.
func TestStatefulScoreOnSessionTools(t *testing.T) {
	names := []string{
		"core_target_close", "core_session_show", "core_targets_list",
		"core_target_select", "core_evidence_show", "core_unknowns_show",
	}
	score, hits := statefulScore(names)
	if score < 2 {
		t.Fatalf("expected stateful evidence, got %d %v", score, hits)
	}
}

// A plain file server must not be flagged, or the warning becomes noise.
func TestStatefulScoreOnStatelessTools(t *testing.T) {
	names := []string{"read_file", "write_file", "list_directory", "search_files", "move_file"}
	score, _ := statefulScore(names)
	if score >= 2 {
		t.Fatalf("false positive, score %d", score)
	}
}

// One incidental marker is not evidence.
func TestSingleMarkerIsNotEnough(t *testing.T) {
	score, _ := statefulScore([]string{"current_time", "get_weather"})
	if score >= 2 {
		t.Fatalf("one marker should not trip the threshold, got %d", score)
	}
}
