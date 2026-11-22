package importscan

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

// Native is an MCP server installed *inside* an agent, rather than declared in
// capbus. It is the thing a scan has to surface: these run whether or not
// capbus knows about them, and capbus cannot switch them off.
type Native struct {
	// Plugin is the agent's package name, e.g. dsh-craft-anything.
	Plugin string
	// Server is the MCP serverName the plugin registers, e.g. craft.
	Server string
	// Command and Args are how the plugin starts its server.
	Command string
	Args    []string
	// Resolved is false when the plugin builds its command from an expression
	// capbus cannot evaluate (DSH's !!js), so it cannot be adopted verbatim.
	Resolved bool
	// Broken records that the package declared an MCP client but capbus could
	// not read it at all.
	Broken bool
}

// PluginPackages lists every plugin package installed in an agent profile,
// whether or not it is MCP. The count matters: it is what a user sees as "I
// have too many plugins".
func PluginPackages(home string) []string {
	pattern := filepath.Join(home, ".dsh", "profiles", "*", "node_modules", "dsh-*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || !st.IsDir() {
			continue
		}
		name := filepath.Base(m)
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Natives returns every MCP server bundled inside an agent profile.
func Natives(home string) []Native {
	pattern := filepath.Join(home, ".dsh", "profiles", "*", "node_modules", "dsh-*", "cordis.patch.yml")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	var out []Native
	for _, p := range paths {
		plugin := filepath.Base(filepath.Dir(p))
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		// yaml.v3 tolerates the !!js tag and hands back the expression as a
		// string, so a computed command would look adoptable. Notice it in the
		// raw text instead.
		computed := bytes.Contains(b, []byte("!!js"))

		var doc []struct {
			Insert []struct {
				Name     string `yaml:"name"`
				Disabled bool   `yaml:"disabled"`
				Config   struct {
					ServerName string   `yaml:"serverName"`
					Command    string   `yaml:"command"`
					Args       []string `yaml:"args"`
				} `yaml:"config"`
			} `yaml:"insert"`
		}
		if err := yaml.Unmarshal(b, &doc); err != nil {
			// DSH patches may carry expressions this parser cannot evaluate.
			// Report the plugin as present-but-unreadable instead of silently
			// dropping it.
			out = append(out, Native{Plugin: plugin, Broken: true})
			continue
		}
		for _, row := range doc {
			for _, ins := range row.Insert {
				if ins.Name != "@deepseek-ai/dsh-mcp-client" {
					continue
				}
				// A row switched off in the patch costs nothing, so it must not
				// be reported as a problem.
				if ins.Disabled {
					continue
				}
				cmd := ins.Config.Command
				out = append(out, Native{
					Plugin:   plugin,
					Server:   ins.Config.ServerName,
					Command:  cmd,
					Args:     ins.Config.Args,
					Resolved: cmd != "" && !computed && !looksComputed(cmd),
				})
			}
		}
	}
	return out
}

// looksComputed catches a launch command that is really code.
func looksComputed(cmd string) bool {
	return strings.Contains(cmd, "process.") ||
		strings.Contains(cmd, "require(") ||
		strings.HasPrefix(cmd, "(")
}

// NativeFindings converts the resolved natives into findings an import can use,
// so a plugin that was never started once is still discoverable.
func nativeFindings(home string) []Finding {
	var out []Finding
	for _, n := range Natives(home) {
		if !n.Resolved {
			continue
		}
		name := n.Server
		if name == "" {
			name = strings.TrimPrefix(n.Plugin, "dsh-")
		}
		out = append(out, Finding{
			Name:    name,
			Command: n.Command,
			Args:    n.Args,
			Origin:  "dsh:" + n.Plugin,
		})
	}
	return out
}
