package trim

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/capbus/capbus/internal/proto"
)

func response(payload string) *proto.Message {
	return &proto.Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(payload)}
}

func TestSmallResultIsUntouched(t *testing.T) {
	body := `{"content":[{"type":"text","text":"ok"}]}`
	m := response(body)
	if saved := Result(m, DefaultLimit); saved != 0 {
		t.Fatalf("saved %d on a small result", saved)
	}
	if string(m.Result) != body {
		t.Fatal("small result was modified")
	}
}

func TestLargeResultIsShrunkAndStaysValid(t *testing.T) {
	huge := strings.Repeat("x", 500_000)
	body, _ := json.Marshal(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": huge}},
	})
	m := response(string(body))
	saved := Result(m, 64<<10)
	if saved <= 0 {
		t.Fatal("expected the result to be trimmed")
	}
	if len(m.Result) >= len(body) {
		t.Fatalf("not actually smaller: %d -> %d", len(body), len(m.Result))
	}

	var check map[string]any
	if err := json.Unmarshal(m.Result, &check); err != nil {
		t.Fatalf("trimmed result is not valid JSON: %v", err)
	}
	items, ok := check["content"].([]any)
	if !ok || len(items) == 0 {
		t.Fatal("content array was lost")
	}
	joined, _ := json.Marshal(items)
	if !strings.Contains(string(joined), "capbus trimmed") {
		t.Fatal("no marker telling the model what happened")
	}
	if !strings.Contains(string(joined), "capbus: this result was") {
		t.Fatal("no summary note appended")
	}
}

// A payload we do not understand is more likely to be something the client
// understands and we do not, so it must pass through untouched.
func TestUnparseableResultIsUntouched(t *testing.T) {
	m := response(`<html>` + strings.Repeat("y", 500_000) + `</html>`)
	before := string(m.Result)
	if saved := Result(m, 1024); saved != 0 {
		t.Fatal("unparseable payload must not be trimmed")
	}
	if string(m.Result) != before {
		t.Fatal("unparseable payload was modified")
	}
}

func TestZeroLimitDisables(t *testing.T) {
	huge := strings.Repeat("z", 200_000)
	body, _ := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": huge}}})
	m := response(string(body))
	if saved := Result(m, 0); saved != 0 {
		t.Fatal("limit 0 should disable trimming")
	}
}
