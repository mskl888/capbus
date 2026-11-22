package supervise

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CapState is what a bridge needs in order to reach a running capability.
type CapState struct {
	Address    string    `json:"address"`
	Token      string    `json:"token"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"started_at"`
	Generation int       `json:"generation"`
}

// State is the supervisor's published state, shared through one small file.
// Bridges read it to find their endpoint; that keeps agents' configs static.
type State struct {
	SupervisorPID int                 `json:"supervisor_pid"`
	UpdatedAt     time.Time           `json:"updated_at"`
	Capabilities  map[string]CapState `json:"capabilities"`
}

// StatePath is ~/.capbus/state.json.
func StatePath() string {
	if d := os.Getenv("CAPBUS_HOME"); d != "" {
		return filepath.Join(d, "state.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "state.json"
	}
	return filepath.Join(home, ".capbus", "state.json")
}

// LoadState reads the published state. A missing file is not an error: it just
// means no supervisor is running.
func LoadState() (*State, error) {
	b, err := os.ReadFile(StatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &State{Capabilities: map[string]CapState{}}, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Capabilities == nil {
		s.Capabilities = map[string]CapState{}
	}
	return &s, nil
}

// Save writes the state atomically, so a bridge never reads a half-written file.
func (s *State) Save() error {
	p := StatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	s.UpdatedAt = time.Now()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Clear removes the state file.
func Clear() error {
	err := os.Remove(StatePath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
