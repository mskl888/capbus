package proxy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/capbus/capbus/internal/proto"
)

func TestNonJSONLinesPassThrough(t *testing.T) {
	in := "not json at all\n{\"jsonrpc\":\"2.0\"}\n"
	var out bytes.Buffer
	if err := Pump(&out, strings.NewReader(in), nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != in {
		t.Fatalf("passthrough changed bytes:\n%q\n%q", in, out.String())
	}
}

// A hook that changes nothing must leave the frame byte-identical: re-encoding
// would reorder keys and rewrite escapes for no reason.
func TestUnmodifiedFrameIsByteIdentical(t *testing.T) {
	// Deliberately unusual spacing, which json.Marshal would not reproduce.
	in := "{\"jsonrpc\": \"2.0\", \"id\":1, \"method\":\"ping\"}\n"
	var out bytes.Buffer
	hook := func(*proto.Message) bool { return false }
	if err := Pump(&out, strings.NewReader(in), hook); err != nil {
		t.Fatal(err)
	}
	if out.String() != in {
		t.Fatalf("frame was rewritten:\n%q\n%q", in, out.String())
	}
}

func TestModifiedFrameIsReencoded(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"` + strings.Repeat("a", 100) + `"}]}}` + "\n"
	var out bytes.Buffer
	hook := func(m *proto.Message) bool {
		m.Result = []byte(`{"content":[{"type":"text","text":"short"}]}`)
		return true
	}
	if err := Pump(&out, strings.NewReader(in), hook); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "short") {
		t.Fatalf("modification lost: %q", out.String())
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatal("framing broken: expected exactly one newline")
	}
}

func TestEmptyInput(t *testing.T) {
	var out bytes.Buffer
	if err := Pump(&out, strings.NewReader(""), nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("wrote %d bytes for empty input", out.Len())
	}
}
