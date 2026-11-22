// Package activity records what capabilities were actually called.
//
// Only metadata: capability, tool name, sizes, timing. Never arguments, never
// results. That is the line that makes a usage panel acceptable at all.
package activity

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// rotateAt keeps the file small enough to read on every panel refresh.
	rotateAt = 8 << 20
)

// Event is one observed fact.
type Event struct {
	Time       time.Time `json:"ts"`
	Capability string    `json:"cap"`
	// Kind is "call" (request) or "result" (response).
	Kind string `json:"kind"`
	// Tool is the MCP tool name on a call, empty on a result.
	Tool string `json:"tool,omitempty"`
	// Bytes is the payload size in the direction the event describes.
	Bytes int `json:"bytes"`
	// Trimmed is how many bytes trimming removed, when it removed any.
	Trimmed int `json:"trimmed,omitempty"`
}

func home() string {
	if d := os.Getenv("CAPBUS_HOME"); d != "" {
		return d
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return "."
	}
	return filepath.Join(h, ".capbus")
}

// Path is the activity log.
func Path() string { return filepath.Join(home(), "activity.jsonl") }

var mu sync.Mutex

// Append writes one event. Recording must never break a capability, so every
// caller can ignore the error.
func Append(e Event) error {
	mu.Lock()
	defer mu.Unlock()

	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if err := os.MkdirAll(home(), 0o700); err != nil {
		return err
	}
	if st, err := os.Stat(Path()); err == nil && st.Size() > rotateAt {
		_ = os.Rename(Path(), Path()+".1")
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(Path(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Tail returns the last n events, oldest first.
func Tail(n int) ([]Event, error) {
	f, err := os.Open(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	ring := make([]Event, 0, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 256<<10), 4<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, e)
	}
	return ring, sc.Err()
}

// Summary aggregates what a panel needs: per capability, per tool, when last
// called and how much it returned.
type Summary struct {
	Capability string
	Calls      int
	LastCall   time.Time
	Tools      map[string]int
	BytesOut   int
	Trimmed    int
}

// Summarize folds the log into per-capability totals.
func Summarize(events []Event) map[string]*Summary {
	out := map[string]*Summary{}
	for _, e := range events {
		s, ok := out[e.Capability]
		if !ok {
			s = &Summary{Capability: e.Capability, Tools: map[string]int{}}
			out[e.Capability] = s
		}
		switch e.Kind {
		case "call":
			s.Calls++
			if e.Time.After(s.LastCall) {
				s.LastCall = e.Time
			}
			if e.Tool != "" {
				s.Tools[e.Tool]++
			}
		case "result":
			s.BytesOut += e.Bytes
			s.Trimmed += e.Trimmed
		}
	}
	return out
}
