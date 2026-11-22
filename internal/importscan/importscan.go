// Package importscan finds MCP servers that already exist on this machine and
// turns them into capability declarations.
//
// This is what makes "declare once" practical instead of a rewrite: every agent
// you already use has a config full of servers, and each of those is a
// capability the other agents cannot see. Discovery fills the socket from the
// machine's current state, so the user does not hand-copy YAML.
package importscan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Finding is one MCP server discovered on disk.
type Finding struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	// Origin says which agent's config it came from, so the user can tell a
	// DSH plugin from a Claude one.
	Origin string
}

// Discover scans the current user's home directory.
func Discover() []Finding {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return DiscoverIn(home)
}

// DiscoverIn scans an explicit home directory, which tests rely on.
func DiscoverIn(home string) []Finding {
	var all []Finding
	all = append(all, fromDSH(home)...)
	// Plugins that have never been started have no state file, so read their
	// bundles too; otherwise import misses most of them.
	all = append(all, nativeFindings(home)...)
	all = append(all, fromJSONConfig(filepath.Join(home, ".claude.json"), "claude-code")...)
	all = append(all, fromJSONConfig(filepath.Join(home, ".cursor", "mcp.json"), "cursor")...)
	all = append(all, fromCodex(filepath.Join(home, ".codex", "config.toml"))...)
	return dedupe(all)
}

// fromDSH reads the state files DSH writes per plugin. They record the exact
// command and arguments the plugin runs, which is the only place a DSH plugin's
// underlying server is described plainly.
func fromDSH(home string) []Finding {
	pattern := filepath.Join(home, ".dsh", "plugins-data", "*", "mcp.state.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	var out []Finding
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var st struct {
			CLI  string   `json:"cli"`
			Args []string `json:"args"`
		}
		if err := json.Unmarshal(b, &st); err != nil || st.CLI == "" {
			continue
		}
		name := filepath.Base(filepath.Dir(p))
		out = append(out, Finding{
			Name:    name,
			Command: st.CLI,
			Args:    st.Args,
			Origin:  "dsh",
		})
	}
	return out
}

// fromJSONConfig reads the "mcpServers" map that Claude Code and Cursor share.
func fromJSONConfig(path, origin string) []Finding {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil
	}
	b = trimBOM(b)
	var root map[string]json.RawMessage
	if err := json.Unmarshal(b, &root); err != nil {
		return nil
	}
	raw, ok := root["mcpServers"]
	if !ok {
		return nil
	}
	var servers map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil
	}
	var out []Finding
	for name, s := range servers {
		// Remote entries (url/type) have no command to share; skip them rather
		// than invent one.
		if s.Command == "" {
			continue
		}
		out = append(out, Finding{Name: name, Command: s.Command, Args: s.Args, Env: s.Env, Origin: origin})
	}
	return out
}

func fromCodex(path string) []Finding {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil
	}
	var doc map[string]any
	if err := toml.Unmarshal(b, &doc); err != nil {
		return nil
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	var out []Finding
	for name, v := range servers {
		m, _ := v.(map[string]any)
		cmd, _ := m["command"].(string)
		if cmd == "" {
			continue
		}
		f := Finding{Name: name, Command: cmd, Origin: "codex"}
		switch args := m["args"].(type) {
		case []any:
			for _, a := range args {
				if s, ok := a.(string); ok {
					f.Args = append(f.Args, s)
				}
			}
		}
		switch env := m["env"].(type) {
		case map[string]any:
			f.Env = map[string]string{}
			for k, val := range env {
				if s, ok := val.(string); ok {
					f.Env[k] = s
				}
			}
		}
		out = append(out, f)
	}
	return out
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

// dedupe removes servers that are literally the same command (the common case:
// the same plugin installed in three agents), and makes the names unique.
func dedupe(in []Finding) []Finding {
	seen := map[string]bool{}
	usedName := map[string]bool{}
	var out []Finding

	// Stable order: by origin then name, so two runs produce the same result.
	sort.Slice(in, func(i, j int) bool {
		if in[i].Origin != in[j].Origin {
			return in[i].Origin < in[j].Origin
		}
		return in[i].Name < in[j].Name
	})

	for _, f := range in {
		key := f.Command + "\x00" + strings.Join(f.Args, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true

		f.Name = sanitize(f.Name)
		if f.Name == "" {
			f.Name = "imported"
		}
		base := f.Name
		for n := 2; usedName[f.Name]; n++ {
			f.Name = base + "-" + itoa(n)
		}
		usedName[f.Name] = true
		out = append(out, f)
	}
	return out
}

// sanitize makes a name that passes manifest validation: lower case, no spaces.
func sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
