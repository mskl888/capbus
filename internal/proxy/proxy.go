// Package proxy forwards stdio byte-for-byte while letting a hook inspect each
// JSON-RPC line on the way through.
//
// The invariant that matters: a line the hook does not modify is written
// unchanged, and a line that is not JSON-RPC is passed through untouched. A
// proxy that rewrites traffic it does not understand breaks capabilities in
// ways that look like the capability's own bug.
package proxy

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/capbus/capbus/internal/proto"
)

// maxLine bounds one frame; a line longer than this is streaming garbage, not
// a message, and is forwarded without inspection.
const maxLine = 64 << 20

// Hook inspects a message. It may mutate it and must report whether it did; a
// false return means the original bytes are forwarded as they arrived.
type Hook func(*proto.Message) bool

// Pump copies src to dst until src ends. Errors from writing are returned;
// a malformed line is forwarded, not fatal.
func Pump(dst io.Writer, src io.Reader, hook Hook) error {
	r := bufio.NewReaderSize(src, 256<<10)
	w := bufio.NewWriterSize(dst, 256<<10)
	defer w.Flush()

	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			out := line
			if len(line) <= maxLine {
				if modified := inspect(line, hook); modified != nil {
					out = append(modified, '\n')
				}
			}
			if _, werr := w.Write(out); werr != nil {
				return werr
			}
			if err == nil {
				w.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				w.Flush()
				return nil
			}
			return err
		}
	}
}

// inspect returns a re-encoded frame when the hook changed the message, else nil.
func inspect(line []byte, hook Hook) []byte {
	if hook == nil {
		return nil
	}
	var m proto.Message
	if err := json.Unmarshal(line, &m); err != nil {
		return nil // not ours to touch
	}
	if !hook(&m) {
		return nil
	}
	b, err := json.Marshal(&m)
	if err != nil {
		return nil
	}
	return b
}
