package mcpserver

import (
	"strings"
	"testing"
)

// The 2026-11-23 review: a model holding this server could register a
// capability and write it into every agent's config by itself. Read-only is the
// default; the user opts in with --allow-writes.
func TestWritesAreGatedByDefault(t *testing.T) {
	t.Setenv("CAPBUS_HOME", t.TempDir())
	good := "name: x\nkind: mcp\nscope: global\nsource:\n  command: /bin/true\n"

	s := &Server{}
	if msg, ok := s.register(good); ok || !strings.Contains(msg, "--allow-writes") {
		t.Fatalf("read-only server allowed a register: %q ok=%v", msg, ok)
	}
	if msg, ok := s.sync(false); ok || !strings.Contains(msg, "--allow-writes") {
		t.Fatalf("read-only server allowed a sync: %q ok=%v", msg, ok)
	}

	s.writable = true
	if msg, ok := s.register(good); !ok {
		t.Fatalf("writable server refused a valid manifest: %s", msg)
	}
}
