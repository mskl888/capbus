// Package trim shrinks oversized tool results before they reach the model.
//
// A capability can answer a one-line question with a megabyte of JSON. The
// model usually needs three fields of it, but the whole payload enters the
// context and is paid for on every turn after. Trimming happens in the proxy,
// needs no model, and is purely structural.
package trim

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/capbus/capbus/internal/proto"
)

// DefaultLimit is the size above which a tool result is eligible for trimming.
// Chosen to sit well above ordinary answers so normal work is untouched.
const DefaultLimit = 64 << 10

// Result shrinks a tools/call response in place and reports how many bytes were
// dropped. It never touches a response it cannot parse: an unparseable payload
// is more likely to be something the client understands and we do not.
func Result(m *proto.Message, limit int) int {
	if limit <= 0 || len(m.Result) <= limit {
		return 0
	}
	var root map[string]any
	if err := json.Unmarshal(m.Result, &root); err != nil {
		return 0
	}
	items, ok := root["content"].([]any)
	if !ok || len(items) == 0 {
		return 0
	}

	original := len(m.Result)
	perItem := limit / len(items)
	if perItem < 1024 {
		perItem = 1024
	}

	dropped := 0
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		text, ok := m["text"].(string)
		if !ok || len(text) <= perItem {
			continue
		}
		// Cut on a rune boundary: slicing bytes would split a multi-byte
		// character and leave invalid UTF-8 in the JSON string.
		at := perItem
		for at > 0 && !utf8.RuneStart(text[at]) {
			at--
		}
		cut := len(text) - at
		dropped += cut
		note := fmt.Sprintf(
			"\n[capbus trimmed %d of %d bytes from this result; ask for less, or a narrower range]", cut, len(text))
		if trimmedJSON(text[:at]) {
			note += "\n[this text was JSON and is now incomplete]"
		}
		m["text"] = text[:at] + note
	}

	if dropped == 0 {
		return 0
	}
	// A note the model can act on, appended so it is not buried mid-list.
	items = append(items, map[string]any{
		"type": "text",
		"text": fmt.Sprintf("[capbus: this result was %d bytes and was trimmed to fit %d]", original, limit),
	})
	root["content"] = items

	b, err := json.Marshal(root)
	if err != nil || len(b) >= original {
		return 0 // no benefit, so leave the original untouched
	}
	m.Result = b
	return original - len(b)
}

// trimmedJSON reports whether what is left of the text is the beginning of a
// JSON document, which the trim has just made unparseable. Saying so is better
// than handing the model a broken object it will try to read anyway.
func trimmedJSON(s string) bool {
	s = strings.TrimLeft(s, " \t\r\n")
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}
