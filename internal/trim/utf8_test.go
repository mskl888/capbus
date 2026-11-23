package trim

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/capbus/capbus/internal/proto"
)

func wrap(t *testing.T, text string) *proto.Message {
	t.Helper()
	res := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return &proto.Message{Result: b}
}

// A byte slice cuts a multi-byte character in half and leaves invalid UTF-8.
// 30k copies of a 3-byte rune makes a cut that is not a multiple of 3 nearly
// certain, which is what the old code did every time.
func TestTrimCutsOnRuneBoundary(t *testing.T) {
	m := wrap(t, strings.Repeat("中", 30000))
	if n := Result(m, DefaultLimit); n == 0 {
		t.Fatal("a 90 KB result should have been trimmed")
	}

	var out map[string]any
	if err := json.Unmarshal(m.Result, &out); err != nil {
		t.Fatalf("trim produced invalid JSON: %v", err)
	}
	items := out["content"].([]any)
	first := items[0].(map[string]any)["text"].(string)
	if !utf8.ValidString(first) {
		t.Fatal("trim produced invalid UTF-8")
	}
	head, _, ok := strings.Cut(first, "\n[capbus trimmed")
	if !ok {
		t.Fatalf("no marker on the trimmed item: %.80q", first)
	}
	if len(head)%3 != 0 {
		t.Fatalf("the cut landed inside a rune: kept %d bytes, not a multiple of 3", len(head))
	}
}

// Trimming structured text makes it unparseable; the model should be told that
// rather than handed a broken object.
func TestTrimSaysWhenItBrokeJSON(t *testing.T) {
	body := `{"rows":[` + strings.Repeat(`{"id":1,"name":"a"},`, 5000) + `{"id":0}]}`
	m := wrap(t, body)
	if n := Result(m, DefaultLimit); n == 0 {
		t.Fatal("nothing trimmed")
	}
	var out map[string]any
	if err := json.Unmarshal(m.Result, &out); err != nil {
		t.Fatalf("trim produced invalid JSON: %v", err)
	}
	first := out["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(first, "was JSON and is now incomplete") {
		t.Fatalf("no warning that the JSON was cut: %.120q", first)
	}
}
