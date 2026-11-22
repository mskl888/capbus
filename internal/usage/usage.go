// Package usage caches how much context each capability costs.
//
// A capability's tool schemas are re-sent to the model on every turn, so their
// size is a recurring cost, not a one-time one. Measuring it requires starting
// the server, which can take a minute for a cold npx, so the numbers are cached
// and refreshed on demand.
package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Cost is what one capability costs per turn.
type Cost struct {
	Tools      int       `json:"tools"`
	Bytes      int       `json:"bytes"`
	Tokens     int       `json:"tokens"`
	MeasuredAt time.Time `json:"measured_at"`
}

type file struct {
	Capabilities map[string]Cost `json:"capabilities"`
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

func Path() string { return filepath.Join(home(), "usage.json") }

func load() (*file, error) {
	f := &file{Capabilities: map[string]Cost{}}
	b, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, err
	}
	if f.Capabilities == nil {
		f.Capabilities = map[string]Cost{}
	}
	return f, nil
}

func save(f *file) error {
	if err := os.MkdirAll(home(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

// Get returns the cached cost for a capability.
func Get(name string) (Cost, bool) {
	f, err := load()
	if err != nil {
		return Cost{}, false
	}
	c, ok := f.Capabilities[name]
	return c, ok
}

// Put caches a measurement.
func Put(name string, c Cost) error {
	f, err := load()
	if err != nil {
		return err
	}
	f.Capabilities[name] = c
	return save(f)
}

// All returns every cached cost.
func All() map[string]Cost {
	f, err := load()
	if err != nil {
		return nil
	}
	return f.Capabilities
}

// Total sums the cached cost of the named capabilities and reports how many
// were never measured, so a caller can say "N of M measured" instead of
// presenting a confidently wrong number.
func Total(names []string) (tokens int, measured int, missing int) {
	f, err := load()
	if err != nil {
		return 0, 0, len(names)
	}
	for _, n := range names {
		if c, ok := f.Capabilities[n]; ok {
			tokens += c.Tokens
			measured++
		} else {
			missing++
		}
	}
	return tokens, measured, missing
}

// Names returns the measured capability names, sorted.
func Names() []string {
	f, err := load()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(f.Capabilities))
	for n := range f.Capabilities {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// EstimateTokens turns a tools/list payload size into a token count. JSON with
// English keys and descriptions runs about 3.5 bytes per token.
func EstimateTokens(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes*2 + 3) / 7
}
