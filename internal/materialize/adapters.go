package materialize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/materialize/dshbundle"
)

// --- JSON dialects: Claude Code, Cursor -------------------------------------

// servers returns the agent's server table, whatever concrete map type it
// happened to be decoded into.
func serversOf(root map[string]any, dialect string) map[string]any {
	if s, ok := root[dialect].(map[string]any); ok {
		return s
	}
	return map[string]any{}
}

type jsonAgent struct{ name, path, dialect string }

func (a jsonAgent) Name() string     { return a.name }
func (a jsonAgent) Location() string { return a.path }
func (a jsonAgent) Detect() bool {
	_, err := os.Stat(a.path)
	return err == nil
}

func (a jsonAgent) Render(ms []manifest.Manifest, opts Options) (Rendered, error) {
	root, err := readJSONObject(a.path)
	if err != nil {
		return Rendered{}, err
	}
	merged, changes, claimed := mergeServers(serversOf(root, a.dialect), ms, opts, selfExe(), ownedSet(loadLedger(), a.name))
	root[a.dialect] = merged

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{Files: map[string][]byte{"": out}, Changes: changes, Claimed: claimed}, nil
}

type claude struct{}

func (claude) Name() string     { return "claude-code" }
func (claude) Location() string { return filepath.Join(userHome(), ".claude.json") }
func (claude) Detect() bool {
	_, err := os.Stat(filepath.Join(userHome(), ".claude.json"))
	return err == nil
}
func (c claude) Render(ms []manifest.Manifest, opts Options) (Rendered, error) {
	return jsonAgent{name: c.Name(), path: c.Location(), dialect: "mcpServers"}.Render(ms, opts)
}

type cursor struct{}

func (cursor) Name() string     { return "cursor" }
func (cursor) Location() string { return filepath.Join(userHome(), ".cursor", "mcp.json") }
func (cursor) Detect() bool {
	_, err := os.Stat(filepath.Join(userHome(), ".cursor", "mcp.json"))
	return err == nil
}
func (c cursor) Render(ms []manifest.Manifest, opts Options) (Rendered, error) {
	return jsonAgent{name: c.Name(), path: c.Location(), dialect: "mcpServers"}.Render(ms, opts)
}

// --- TOML dialect: Codex CLI ------------------------------------------------

// codex writes `[mcp_servers.<name>]` tables in ~/.codex/config.toml.
type codex struct{}

func (codex) Name() string { return "codex" }
func (codex) Location() string {
	return filepath.Join(userHome(), ".codex", "config.toml")
}
func (codex) Detect() bool {
	_, err := os.Stat(filepath.Join(userHome(), ".codex"))
	return err == nil
}

func (codex) Render(ms []manifest.Manifest, opts Options) (Rendered, error) {
	path := filepath.Join(userHome(), ".codex", "config.toml")
	doc := map[string]any{}
	if b, err := os.ReadFile(path); err != nil {
		if !os.IsNotExist(err) {
			return Rendered{}, fmt.Errorf("%s: cannot read the existing config (refusing to overwrite blind): %w", path, err)
		}
	} else if len(b) > 0 {
		if err := toml.Unmarshal(b, &doc); err != nil {
			return Rendered{}, fmt.Errorf("%s: existing config is not valid TOML (refusing to overwrite): %w", path, err)
		}
	}

	existing, _ := doc["mcp_servers"].(map[string]any)
	merged, changes, claimed := mergeServers(existing, ms, opts, selfExe(), ownedSet(loadLedger(), "codex"))
	doc["mcp_servers"] = merged

	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.SetIndentTables(true)
	if err := enc.Encode(doc); err != nil {
		return Rendered{}, err
	}
	return Rendered{Files: map[string][]byte{"": buf.Bytes()}, Changes: changes, Claimed: claimed}, nil
}

// --- bundle dialect: DSH ----------------------------------------------------

// dsh loads MCP servers from a bundle: a package.json plus a cordis.patch.yml
// that inserts @deepseek-ai/dsh-mcp-client once per server. capbus generates
// the bundle; installing it is the user's call, because it changes a profile.
type dsh struct{}

func (dsh) Name() string     { return "dsh" }
func (dsh) Location() string { return filepath.Join(home(), "dsh-bundle") }
func (dsh) Detect() bool {
	_, err := os.Stat(filepath.Join(userHome(), ".dsh"))
	return err == nil
}

func (d dsh) Render(ms []manifest.Manifest, opts Options) (Rendered, error) {
	// The static half of the bundle - the manifest, the Host half that answers
	// /capbus/*, the sidebar panel, the icon and the detail document - ships
	// inside the capbus binary. Writing it from here keeps one package instead
	// of a config-only bundle plus a plugin installed some other way.
	files := map[string][]byte{}
	err := fs.WalkDir(dshbundle.FS, ".", func(name string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil || e.IsDir() {
			return walkErr
		}
		body, readErr := dshbundle.FS.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		files[name] = body
		return nil
	})
	if err != nil {
		return Rendered{}, err
	}

	var b strings.Builder
	b.WriteString("# Generated by capbus: the panel plugin, then one entry per capability.\n")
	b.WriteString("- insert:\n")
	b.WriteString("    - id: 'capbus'\n")
	b.WriteString("      name: '@local/capbus'\n")
	for _, m := range ms {
		verb := "exec"
		if m.Shared || (opts.Shared && m.Scope == manifest.ScopeGlobal) {
			verb = "bridge"
		}
		fmt.Fprintf(&b, "    - id: %s\n", yq("capbus-"+m.Name))
		fmt.Fprintf(&b, "      name: %s\n", yq("@deepseek-ai/dsh-mcp-client"))
		b.WriteString("      config:\n")
		fmt.Fprintf(&b, "        serverName: %s\n", yq(m.Name))
		b.WriteString("        transport: stdio\n")
		fmt.Fprintf(&b, "        command: %s\n", yq(selfExe()))
		b.WriteString("        args:\n")
		fmt.Fprintf(&b, "          - %s\n", yq(verb))
		fmt.Fprintf(&b, "          - %s\n", yq(m.Name))
		b.WriteString("        failOnStartupError: true\n")
	}

	files["cordis.patch.yml"] = []byte(b.String())

	// The bundle belongs to capbus, so a dry run should say whether it would
	// actually change: compare every file, not just the patch.
	action := "added"
	existing := 0
	same := true
	for name, body := range files {
		prev, readErr := os.ReadFile(filepath.Join(d.Location(), name))
		if readErr != nil {
			same = false
			continue
		}
		existing++
		if !bytes.Equal(prev, body) {
			same = false
		}
	}
	switch {
	case existing == 0:
		action = "added"
	case same:
		action = "unchanged"
	default:
		action = "updated"
	}

	var changes []Change
	claimed := make([]string, 0, len(ms))
	for _, m := range ms {
		changes = append(changes, Change{Name: m.Name, Action: action, Detail: "dsh bundle"})
		claimed = append(claimed, m.Name)
	}
	sort.Strings(claimed)

	return Rendered{Files: files, Changes: changes, Claimed: claimed}, nil
}

// yq single-quotes a YAML scalar. Quoting everything keeps capability names,
// paths and arguments safe without a special-case table.
func yq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
