// Package scan produces a read-only diagnosis of this machine.
//
// It deliberately changes nothing. The point is to make the invisible visible:
// which MCP servers are running that capbus does not control, which declared
// capabilities cannot start, and what the whole set costs the model on every
// turn. Fixing is a separate step, with confirmation.
package scan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/capbus/capbus/internal/enabled"
	"github.com/capbus/capbus/internal/importscan"
	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/materialize"
	"github.com/capbus/capbus/internal/registry"
	"github.com/capbus/capbus/internal/secret"
	"github.com/capbus/capbus/internal/supervise"
	"github.com/capbus/capbus/internal/usage"
)

type Severity string

const (
	High   Severity = "HIGH"
	Medium Severity = "MEDIUM"
	Low    Severity = "LOW"
	Info   Severity = "INFO"
)

// Finding is one problem, with the action that would resolve it. Fix is a
// command the user can run today; an empty Fix means capbus cannot fix it.
type Finding struct {
	ID       string
	Severity Severity
	Title    string
	Detail   string
	Fix      string
	Items    []string
}

type Report struct {
	Packages []string
	Natives  []importscan.Native
	Declared int
	Enabled  int
	Findings []Finding
}

func (r Report) ResolvedNatives() int {
	n := 0
	for _, x := range r.Natives {
		if x.Resolved {
			n++
		}
	}
	return n
}

// Run performs every check. It never writes.
func Run() Report {
	home, _ := os.UserHomeDir()
	rep := Report{
		Packages: importscan.PluginPackages(home),
		Natives:  importscan.Natives(home),
	}

	ms, _ := registry.Load()
	rep.Declared = len(ms)

	on := enabled.Filter(ms)
	rep.Enabled = len(on)

	declared := map[string]bool{}
	for _, m := range ms {
		declared[m.Name] = true
	}

	rep.Findings = append(rep.Findings, checkNatives(rep.Natives, declared)...)
	rep.Findings = append(rep.Findings, checkStateful(ms)...)
	rep.Findings = append(rep.Findings, checkRuntimes(on)...)
	rep.Findings = append(rep.Findings, checkSecrets(on)...)
	rep.Findings = append(rep.Findings, checkStale(declared)...)
	rep.Findings = append(rep.Findings, checkCost(on)...)
	rep.Findings = append(rep.Findings, checkSupervisor(on)...)

	sort.SliceStable(rep.Findings, func(i, j int) bool {
		return rank(rep.Findings[i].Severity) < rank(rep.Findings[j].Severity)
	})
	return rep
}

func rank(s Severity) int {
	switch s {
	case High:
		return 0
	case Medium:
		return 1
	case Low:
		return 2
	}
	return 3
}

// checkNatives reports MCP servers installed inside an agent. These are the
// ones that cost tokens and that capbus cannot switch off.
func checkNatives(natives []importscan.Native, declared map[string]bool) []Finding {
	var unmanaged, duplicates, unreadable []string
	for _, n := range natives {
		label := n.Plugin
		if n.Server != "" {
			label += " (" + n.Server + ")"
		}
		already := declaredAs(n, declared)
		switch {
		case n.Broken:
			if already {
				duplicates = append(duplicates, label+" - also declared in capbus; the agent still loads its own copy")
			} else {
				unreadable = append(unreadable, label+" - its bundle could not be read")
			}
		case n.Resolved && !already:
			unmanaged = append(unmanaged, label)
		case already:
			// Declared in capbus *and* installed in the agent. The native copy
			// is what actually runs, so the capbus switch does nothing yet.
			duplicates = append(duplicates, label+" - also declared in capbus; the agent still loads its own copy")
		default:
			// Present, but its launch command is computed at runtime. It may
			// still be adoptable through a state file; only complain when the
			// name is unknown to capbus.
			unreadable = append(unreadable, label+" - launch command is computed, no plain command to copy")
		}
	}

	var out []Finding
	if len(unmanaged) > 0 {
		out = append(out, Finding{
			ID:       "adopt",
			Severity: High,
			Title:    fmt.Sprintf("%d MCP server(s) are installed inside an agent and cost tokens capbus cannot control", len(unmanaged)),
			Detail:   "Each one injects its tool descriptions into the model on every turn. Disabling them in capbus does nothing until capbus owns the entry.",
			Fix:      "capbus import  (then adopt: stop the plugin, install the capbus bundle)",
			Items:    unmanaged,
		})
	}
	if len(duplicates) > 0 {
		out = append(out, Finding{
			ID:       "duplicate",
			Severity: Medium,
			Title:    fmt.Sprintf("%d capability(ies) are installed twice: once natively, once in capbus", len(duplicates)),
			Detail:   "The native copy is what the agent actually loads, so the capbus switch has no effect yet.",
			Fix:      "capbus sync, then remove the native plugin",
			Items:    duplicates,
		})
	}
	if len(unreadable) > 0 {
		out = append(out, Finding{
			ID:       "adopt-manual",
			Severity: Low,
			Title:    fmt.Sprintf("%d MCP plugin(s) cannot be adopted automatically", len(unreadable)),
			Detail:   "Their launch command is built at runtime, so capbus cannot copy it. They need a hand-written manifest.",
			Fix:      "write a manifest by hand for these, or leave them alone",
			Items:    unreadable,
		})
	}
	return out
}

func serverOr(n importscan.Native) string {
	if n.Server != "" {
		return n.Server
	}
	return strings.TrimPrefix(n.Plugin, "dsh-")
}

// declaredAs matches a plugin against capbus capability names. The names rarely
// line up exactly: a plugin ships `serverName: craft` while capbus imported it
// as `craft-anything` from the plugin's directory, so the package suffix counts
// as a match too.
func declaredAs(n importscan.Native, declared map[string]bool) bool {
	if declared[serverOr(n)] {
		return true
	}
	return declared[strings.TrimPrefix(n.Plugin, "dsh-")]
}

// statefulMarkers are tool-name fragments that suggest a server keeps a live
// session. Two or more distinct hits is treated as evidence.
var statefulMarkers = []string{
	"session", "target", "current", "attach",
	"mode", "begin", "commit", "lock", "acquire", "release",
}

// statefulScore counts how many distinct markers appear across the tool names.
func statefulScore(names []string) (int, []string) {
	seen := map[string]bool{}
	var hits []string
	for _, n := range names {
		l := strings.ToLower(n)
		for _, m := range statefulMarkers {
			if !seen[m] && strings.Contains(l, m) {
				seen[m] = true
				hits = append(hits, m)
			}
		}
	}
	sort.Strings(hits)
	return len(hits), hits
}

// checkStateful reports capabilities that look like they hold live state but
// are shared machine-wide. Two agents would silently overwrite each other's
// target or open document; no error is raised, which is what makes it worse
// than a crash.
func checkStateful(ms []manifest.Manifest) []Finding {
	var items []string
	for _, m := range ms {
		if m.Scope != manifest.ScopeGlobal || m.Stateful {
			continue
		}
		c, ok := usage.Get(m.Name)
		if !ok {
			continue // unmeasured: no evidence, so no claim
		}
		score, hits := statefulScore(c.ToolNames)
		if score < 2 {
			continue
		}
		items = append(items, fmt.Sprintf("%s exposes %s-style tools but is declared scope: global",
			m.Name, strings.Join(hits, "/")))
	}
	if len(items) == 0 {
		return nil
	}
	return []Finding{{
		ID:       "stateful-global",
		Severity: High,
		Title:    fmt.Sprintf("%d shared capability(ies) look stateful", len(items)),
		Detail: "Two agents driving one live session overwrite each other's target, cursor or open document, " +
			"and nothing reports an error. Isolation - one process per agent - is the fix, not a lock.",
		Fix:   "re-declare with scope: workspace, then capbus sync",
		Items: items,
	}}
}

// checkRuntimes reports enabled capabilities whose command does not exist.
func checkRuntimes(ms []manifest.Manifest) []Finding {
	var items []string
	for _, m := range ms {
		bin, _, err := m.Command()
		if err != nil {
			items = append(items, m.Name+" - "+err.Error())
			continue
		}
		if _, err := exec.LookPath(bin); err != nil {
			items = append(items, fmt.Sprintf("%s needs %q, which is not installed", m.Name, bin))
		}
	}
	if len(items) == 0 {
		return nil
	}
	return []Finding{{
		ID:       "install-runtime",
		Severity: Medium,
		Title:    fmt.Sprintf("%d enabled capability(ies) cannot start: missing runtime", len(items)),
		Detail:   "A capability that cannot start is still declared, so your agents may report a broken plugin.",
		Fix:      "install the missing runtime, or disable the capability",
		Items:    items,
	}}
}

// checkSecrets reports declared secrets that were never stored. Values are
// never read here; only presence.
func checkSecrets(ms []manifest.Manifest) []Finding {
	st, err := secret.Open()
	if err != nil {
		return []Finding{{
			ID: "secret-store", Severity: Medium,
			Title:  "the credential store cannot be opened",
			Detail: err.Error(),
		}}
	}
	var items []string
	for _, m := range ms {
		if len(m.Permissions.Secrets) == 0 {
			continue
		}
		_, missing, err := st.Resolve(m.Permissions.Secrets)
		if err != nil {
			continue
		}
		for _, name := range missing {
			items = append(items, fmt.Sprintf("%s needs %s", m.Name, name))
		}
	}
	if len(items) == 0 {
		return nil
	}
	return []Finding{{
		ID:       "set-secret",
		Severity: Medium,
		Title:    fmt.Sprintf("%d credential(s) are missing", len(items)),
		Detail:   "These capabilities will fail at startup. The value never needs to pass through an agent.",
		Fix:      "capbus secret set <NAME>",
		Items:    items,
	}}
}

// checkStale reports capbus entries left in an agent config after the
// capability was removed.
func checkStale(declared map[string]bool) []Finding {
	var items []string
	for _, f := range importscan.Discover() {
		if !strings.Contains(strings.ToLower(filepath.Base(f.Command)), "capbus") {
			continue
		}
		if !declared[f.Name] {
			items = append(items, fmt.Sprintf("%s is still configured in an agent but no longer declared", f.Name))
		}
	}
	if len(items) == 0 {
		return nil
	}
	return []Finding{{
		ID:       "stale-config",
		Severity: Low,
		Title:    fmt.Sprintf("%d stale entr(ies) left in agent configs", len(items)),
		Detail:   "capbus writes the whole capability block, so a sync removes them.",
		Fix:      "capbus sync",
		Items:    items,
	}}
}

// checkCost reports the per-turn price of the currently enabled set.
func checkCost(ms []manifest.Manifest) []Finding {
	names := make([]string, 0, len(ms))
	for _, m := range ms {
		names = append(names, m.Name)
	}
	tokens, measured, missing := usage.Total(names)
	if measured == 0 {
		return []Finding{{
			ID: "measure", Severity: Info,
			Title:  "no capability costs have been measured yet",
			Detail: "Without a measurement the per-turn cost is unknown.",
			Fix:    "capbus cost",
		}}
	}
	if tokens <= 20000 && missing == 0 {
		return nil
	}
	detail := fmt.Sprintf("%d enabled capability(ies) cost about %d tokens on every turn", len(names), tokens)
	if missing > 0 {
		detail += fmt.Sprintf(", and %d are unmeasured", missing)
	}
	return []Finding{{
		ID:       "cost",
		Severity: Medium,
		Title:    "the enabled set is expensive",
		Detail:   detail,
		Fix:      "capbus use <name>  (keep only what this session needs)",
	}}
}

// checkSupervisor reports that enabled capabilities will each start their own
// copy inside every agent.
func checkSupervisor(ms []manifest.Manifest) []Finding {
	global := 0
	for _, m := range ms {
		if m.Scope == manifest.ScopeGlobal {
			global++
		}
	}
	if global < 2 {
		return nil
	}
	if st, err := supervise.LoadState(); err == nil && len(st.Capabilities) > 0 {
		return nil
	}
	return []Finding{{
		ID:       "supervise",
		Severity: Info,
		Title:    fmt.Sprintf("%d global capabilities will each start a separate copy in every agent", global),
		Detail:   "One shared backend would start once and serve every agent.",
		Fix:      "capbus supervise, then capbus sync --shared",
	}}
}

// Agents lists the agent adapters detected on this machine.
func Agents() []string {
	var out []string
	for _, a := range materialize.Agents() {
		if a.Detect() {
			out = append(out, a.Name())
		}
	}
	sort.Strings(out)
	return out
}
