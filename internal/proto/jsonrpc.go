// Package proto implements the MCP stdio framing: one JSON-RPC 2.0 object per
// line. It is deliberately the only place that knows about wire format, so the
// supervisor can multiplex messages without re-encoding them.
package proto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// MaxMessage bounds one frame. Tool results can carry screenshots and file
// contents, so the ceiling is generous rather than tight.
const MaxMessage = 32 << 20

// Error is a JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Message is any JSON-RPC 2.0 message: request, notification or response.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (m *Message) IsRequest() bool      { return m.Method != "" && len(m.ID) > 0 }
func (m *Message) IsNotification() bool { return m.Method != "" && len(m.ID) == 0 }
func (m *Message) IsResponse() bool     { return m.Method == "" && len(m.ID) > 0 }

// IDString normalises an id into a map key. JSON-RPC permits string or number.
func (m *Message) IDString() string {
	if len(m.ID) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.ID, &s); err == nil {
		return s
	}
	return string(m.ID)
}

// SetID replaces the id with a numeric one.
func (m *Message) SetID(v int64) {
	b, _ := json.Marshal(v)
	m.ID = b
}

// Request builds a request message.
func Request(id int64, method string, params string) *Message {
	m := &Message{JSONRPC: "2.0", Method: method}
	m.SetID(id)
	if params != "" {
		m.Params = json.RawMessage(params)
	}
	return m
}

// Notification builds a notification (no id, no response expected).
func Notification(method, params string) *Message {
	m := &Message{JSONRPC: "2.0", Method: method}
	if params != "" {
		m.Params = json.RawMessage(params)
	}
	return m
}

// Response builds a success response.
func Response(id json.RawMessage, result string) *Message {
	return &Message{JSONRPC: "2.0", ID: id, Result: json.RawMessage(result)}
}

// ErrorResponse builds an error response.
func ErrorResponse(id json.RawMessage, code int, msg string) *Message {
	return &Message{JSONRPC: "2.0", ID: id, Error: &Error{Code: code, Message: msg}}
}

// Reader decodes newline-delimited JSON-RPC messages.
type Reader struct {
	r *bufio.Reader
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 64<<10)}
}

// NewReaderFrom wraps an existing buffered reader without dropping bytes that
// are already in its buffer. Useful when a handshake line was read first.
func NewReaderFrom(r *bufio.Reader) *Reader { return &Reader{r: r} }

func (r *Reader) Read() (*Message, error) {
	for {
		line, err := r.r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if len(trimmed) > MaxMessage {
				return nil, errors.New("json-rpc message too large")
			}
			var m Message
			if e := json.Unmarshal(trimmed, &m); e != nil {
				return nil, fmt.Errorf("invalid json-rpc frame: %w", e)
			}
			return &m, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// Writer encodes messages, one per line, safe for concurrent use.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

func (w *Writer) Write(m *Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.w.Write(b)
	return err
}
