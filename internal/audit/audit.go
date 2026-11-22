// Package audit is capbus's record of what it did to this machine.
//
// The project's first principle is that nothing may happen invisibly. Activity
// is what the *capabilities* did; this is what *capbus* did — which files it
// rewrote, which capability it switched, when it started hosting backends.
// Without it, "what has this thing changed on my computer" has no answer.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one thing capbus did.
type Entry struct {
	Time   time.Time `json:"ts"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
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

func Path() string { return filepath.Join(home(), "actions.jsonl") }

var mu sync.Mutex

// Append records one action. Recording must never break the action itself, so
// callers ignore the error.
func Append(kind, detail string) error {
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(home(), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(Entry{Time: time.Now(), Kind: kind, Detail: detail})
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

// Tail returns the last n entries, oldest first.
func Tail(n int) ([]Entry, error) {
	f, err := os.Open(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	ring := make([]Entry, 0, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Entry
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

// Clear removes the log. A record the user cannot clear is surveillance.
func Clear() error {
	err := os.Remove(Path())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
