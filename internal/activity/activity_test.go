package activity

import (
	"path/filepath"
	"testing"
	"time"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("CAPBUS_HOME", filepath.Join(t.TempDir(), ".capbus"))
}

func TestAppendAndTail(t *testing.T) {
	isolate(t)
	if err := Append(Event{Capability: "a", Kind: "call", Tool: "t1", Bytes: 10}); err != nil {
		t.Fatal(err)
	}
	if err := Append(Event{Capability: "a", Kind: "result", Bytes: 100, Trimmed: 40}); err != nil {
		t.Fatal(err)
	}

	events, err := Tail(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events", len(events))
	}
	if events[0].Tool != "t1" || events[1].Trimmed != 40 {
		t.Fatalf("bad events: %+v", events)
	}
}

func TestTailKeepsOnlyTheLastN(t *testing.T) {
	isolate(t)
	for i := 0; i < 10; i++ {
		_ = Append(Event{Capability: "a", Kind: "call", Tool: "t"})
	}
	events, err := Tail(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d", len(events))
	}
}

func TestSummarize(t *testing.T) {
	now := time.Now()
	events := []Event{
		{Time: now.Add(-time.Hour), Capability: "a", Kind: "call", Tool: "read"},
		{Time: now, Capability: "a", Kind: "call", Tool: "read"},
		{Time: now, Capability: "a", Kind: "call", Tool: "write"},
		{Time: now, Capability: "a", Kind: "result", Bytes: 500, Trimmed: 200},
	}
	sum := Summarize(events)
	a := sum["a"]
	if a == nil {
		t.Fatal("missing capability")
	}
	if a.Calls != 3 || a.Tools["read"] != 2 || a.Tools["write"] != 1 {
		t.Fatalf("bad summary: %+v", a)
	}
	if a.BytesOut != 500 || a.Trimmed != 200 {
		t.Fatalf("bad byte totals: %+v", a)
	}
	if !a.LastCall.Equal(now) {
		t.Fatalf("last call %v, want %v", a.LastCall, now)
	}
}

func TestTailOnMissingFile(t *testing.T) {
	isolate(t)
	events, err := Tail(5)
	if err != nil {
		t.Fatalf("a missing log is not an error: %v", err)
	}
	if len(events) != 0 {
		t.Fatal("expected nothing")
	}
}
