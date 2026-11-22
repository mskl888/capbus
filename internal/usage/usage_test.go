package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("CAPBUS_HOME", filepath.Join(t.TempDir(), ".capbus"))
}

func TestPutGetRoundTrip(t *testing.T) {
	isolate(t)
	want := Cost{Tools: 30, Bytes: 29488, Tokens: 8425, MeasuredAt: time.Now().Truncate(time.Second)}
	if err := Put("craft-anything", want); err != nil {
		t.Fatal(err)
	}
	got, ok := Get("craft-anything")
	if !ok {
		t.Fatal("not found")
	}
	if got.Tools != 30 || got.Tokens != 8425 {
		t.Fatalf("got %+v", got)
	}
}

func TestUnknownIsNotAnError(t *testing.T) {
	isolate(t)
	if _, ok := Get("nope"); ok {
		t.Fatal("unknown capability should report not measured")
	}
}

// Total must say how many were never measured rather than quietly summing to a
// number that looks complete.
func TestTotalReportsMissing(t *testing.T) {
	isolate(t)
	_ = Put("a", Cost{Tokens: 1000})
	_ = Put("b", Cost{Tokens: 24})

	tokens, measured, missing := Total([]string{"a", "b", "c"})
	if tokens != 1024 || measured != 2 || missing != 1 {
		t.Fatalf("tokens=%d measured=%d missing=%d", tokens, measured, missing)
	}
}

func TestTotalWithNoMeasurements(t *testing.T) {
	isolate(t)
	tokens, measured, missing := Total([]string{"a", "b"})
	if tokens != 0 || measured != 0 || missing != 2 {
		t.Fatalf("tokens=%d measured=%d missing=%d", tokens, measured, missing)
	}
}

func TestEstimateTokens(t *testing.T) {
	// 3.5 bytes per token, rounded.
	if got := EstimateTokens(0); got != 0 {
		t.Fatalf("0 bytes -> %d", got)
	}
	if got := EstimateTokens(35); got != 10 {
		t.Fatalf("35 bytes -> %d, want 10", got)
	}
	if got := EstimateTokens(29488); got < 8400 || got > 8450 {
		t.Fatalf("29488 bytes -> %d, want ~8425", got)
	}
}

func TestOverwrite(t *testing.T) {
	isolate(t)
	_ = Put("a", Cost{Tokens: 1})
	_ = Put("a", Cost{Tokens: 2})
	if c, _ := Get("a"); c.Tokens != 2 {
		t.Fatalf("got %d", c.Tokens)
	}
}
