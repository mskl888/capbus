// Command capbus is the local capability bus: declare a capability once, and
// every agent on the machine gets it.
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/capbus/capbus/internal/enabled"
	"github.com/capbus/capbus/internal/importscan"
	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/materialize"
	"github.com/capbus/capbus/internal/mcpserver"
	"github.com/capbus/capbus/internal/probe"
	"github.com/capbus/capbus/internal/proto"
	"github.com/capbus/capbus/internal/registry"
	"github.com/capbus/capbus/internal/scan"
	"github.com/capbus/capbus/internal/secret"
	"github.com/capbus/capbus/internal/supervise"
	cost "github.com/capbus/capbus/internal/usage"
	yaml "gopkg.in/yaml.v3"
)

const version = "0.4.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Printf("capbus %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
	case "doctor":
		os.Exit(doctor())
	case "list":
		os.Exit(list())
	case "sync":
		os.Exit(syncCmd(os.Args[2:]))
	case "supervise":
		os.Exit(superviseCmd())
	case "down":
		os.Exit(downCmd())
	case "bridge":
		os.Exit(bridgeCmd(os.Args[2:]))
	case "exec":
		os.Exit(execCmd(os.Args[2:]))
	case "secret":
		os.Exit(secretCmd(os.Args[2:]))
	case "mcp":
		os.Exit(mcpCmd())
	case "init":
		os.Exit(initCmd())
	case "import":
		os.Exit(importCmd(os.Args[2:]))
	case "enable":
		os.Exit(toggleCmd(os.Args[2:], true))
	case "disable":
		os.Exit(toggleCmd(os.Args[2:], false))
	case "use":
		os.Exit(useCmd(os.Args[2:]))
	case "cost":
		os.Exit(costCmd(os.Args[2:]))
	case "scan":
		os.Exit(scanCmd())
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "capbus: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `capbus — 装一次，所有 Agent 都会用

usage:
  capbus doctor              check environment, agents and capability sources
  capbus list                list declared capabilities
  capbus sync [--shared]     write capabilities into every detected agent
  capbus supervise           run one shared backend per global capability
  capbus down                stop a running supervisor
  capbus bridge <name>       stdio shim used by agents (called by them)
  capbus exec <name>         run one capability with secrets injected
  capbus secret <cmd>        manage credentials: list|set|get|rm
  capbus mcp                 run capbus as an MCP server (agents drive it)
  capbus init                setup check with the exact next steps
  capbus import [--dry-run]  adopt MCP servers already configured in any agent
  capbus enable|disable <n>  switch one capability on or off
  capbus use <all|default|name>  which capabilities get injected at all
  capbus cost [--refresh]    measure what each capability costs per turn
  capbus scan                read-only diagnosis: what is wrong and what it costs
  capbus version

  --shared  point global capabilities at the shared backend instead of
            spawning a copy inside every agent
`)
}

func doctor() int {
	ok := true
	fmt.Printf("capbus %s (%s/%s)\n\n", version, runtime.GOOS, runtime.GOARCH)

	fmt.Println("capability sources")
	for _, d := range registry.Dirs() {
		abs, _ := filepath.Abs(d)
		n := 0
		if entries, err := os.ReadDir(d); err == nil {
			for _, e := range entries {
				if ext := filepath.Ext(e.Name()); ext == ".yaml" || ext == ".yml" {
					n++
				}
			}
		}
		status := "missing"
		if n > 0 || dirExists(d) {
			status = fmt.Sprintf("%d manifest(s)", n)
		} else {
			ok = false
		}
		fmt.Printf("  %-56s %s\n", abs, status)
	}

	fmt.Println("\nagents")
	for _, a := range materialize.Agents() {
		detected := a.Detect()
		status := "not installed"
		if detected {
			status = "ok"
		} else {
			ok = false
		}
		fmt.Printf("  %-16s %-14s %s\n", a.Name(), status, a.Location())
	}

	fmt.Println("\nsecrets")
	if st, err := secret.Open(); err != nil {
		fmt.Printf("  ! %v\n", err)
		ok = false
	} else {
		infos, lerr := st.List()
		switch {
		case lerr != nil:
			fmt.Printf("  ! %v\n", lerr)
			ok = false
		case len(infos) == 0:
			fmt.Println("  none stored")
		default:
			fmt.Printf("  %d stored\n", len(infos))
			all, _ := registry.Load()
			for _, m := range all {
				if len(m.Permissions.Secrets) == 0 {
					continue
				}
				_, missing, rerr := st.Resolve(m.Permissions.Secrets)
				if rerr != nil {
					fmt.Printf("  ! %s: %v\n", m.Name, rerr)
					ok = false
					continue
				}
				if len(missing) > 0 {
					fmt.Printf("  ! %s needs %s (missing)\n", m.Name, strings.Join(missing, ", "))
					ok = false
				}
			}
		}
	}

	fmt.Println("\nsupervisor")
	if st, err := supervise.LoadState(); err == nil && len(st.Capabilities) > 0 {
		names := make([]string, 0, len(st.Capabilities))
		for n := range st.Capabilities {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Printf("  running (pid %d), %d capability(ies)\n", st.SupervisorPID, len(names))
		for _, n := range names {
			cs := st.Capabilities[n]
			fmt.Printf("  %-16s %s pid=%d gen=%d\n", n, cs.Address, cs.PID, cs.Generation)
		}
	} else {
		fmt.Println("  not running")
	}

	ms, errs := registry.Load()
	fmt.Printf("\n%d capability manifest(s) loaded, %d error(s)\n", len(ms), len(errs))
	for _, e := range errs {
		fmt.Printf("  ! %v\n", e)
		ok = false
	}
	if !ok {
		fmt.Println("\nresult: not ready")
		return 1
	}
	fmt.Println("\nresult: ready")
	return 0
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func list() int {
	ms, errs := registry.Load()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "! %v\n", e)
	}
	if len(ms) == 0 {
		fmt.Println("no capabilities declared")
		fmt.Printf("add one to %s\n", registry.Dir())
		return 0
	}
	fmt.Printf("%-16s %-6s %-10s %-9s %s\n", "NAME", "KIND", "SCOPE", "STATEFUL", "SOURCE")
	st, _ := enabled.Load()
	for _, m := range ms {
		mark := ""
		if !st.IsEnabled(m.Name) {
			mark = "   [off]"
		}
		fmt.Printf("%-16s %-6s %-10s %-9v %s%s\n",
			m.Name, m.Kind, m.Scope, m.Stateful, sourceString(m), mark)
	}
	return 0
}

func sourceString(m manifest.Manifest) string {
	s := m.Source
	switch {
	case s.NPM != "":
		return "npm:" + s.NPM
	case s.OCI != "":
		return "oci:" + s.OCI
	case s.Git != "":
		return "git:" + s.Git
	case s.Path != "":
		return "path:" + s.Path
	case s.Command != "":
		return "cmd:" + s.Command
	}
	return "-"
}

func syncCmd(args []string) int {
	opts := materialize.Options{}
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			opts.DryRun = true
		case "--shared":
			opts.Shared = true
		default:
			fmt.Fprintf(os.Stderr, "capbus sync: unknown flag %q\n", a)
			return 2
		}
	}
	if opts.Shared {
		if st, err := supervise.LoadState(); err != nil || len(st.Capabilities) == 0 {
			fmt.Fprintln(os.Stderr, "capbus sync --shared: no supervisor running; start `capbus supervise` first, "+
				"or drop --shared to spawn a copy inside each agent")
			return 1
		}
	}

	ms, errs := registry.Load()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "! %v\n", e)
	}
	// A disabled capability is not written at all, so its tool schemas never
	// reach a model's context. Filtering after writing would save nothing.
	ms = enabled.Filter(ms)
	wrote, syncErrs := materialize.Sync(ms, opts)
	for _, e := range syncErrs {
		fmt.Fprintf(os.Stderr, "! %v\n", e)
	}
	if len(wrote) == 0 {
		if len(syncErrs) == 0 {
			fmt.Println("no agent config found — install an agent, or run `capbus doctor`")
		}
		return 1
	}
	for _, w := range wrote {
		fmt.Println("wrote " + w)
	}
	if len(ms) > 0 {
		names := make([]string, 0, len(ms))
		for _, m := range ms {
			names = append(names, m.Name)
		}
		tokens, measured, missing := cost.Total(names)
		fmt.Printf("\n%d capability(ies) injected", len(ms))
		if measured > 0 {
			fmt.Printf(", about %d tokens on every turn", tokens)
		}
		if missing > 0 {
			fmt.Printf(" (%d unmeasured: run `capbus cost`)", missing)
		}
		fmt.Println()
		if tokens > 20000 {
			fmt.Println("that is a large slice of the context window: `capbus use <name>` narrows it")
		}
	}
	if len(syncErrs) > 0 {
		return 1
	}
	return 0
}

// superviseCmd runs the shared backends in the foreground. Ctrl-C stops it.
func superviseCmd() int {
	ms, errs := registry.Load()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "! %v\n", e)
	}
	lg := log.New(os.Stderr, "", log.LstdFlags)
	d := supervise.New(ms, lg)
	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "capbus supervise: %v\n", err)
		return 1
	}

	publish := func() {
		st := d.State()
		st.SupervisorPID = os.Getpid()
		if err := st.Save(); err != nil {
			lg.Printf("state: %v", err)
		}
	}
	publish()

	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				publish()
			}
		}
	}()

	fmt.Printf("supervising %d capability(ies); Ctrl-C to stop\n", len(d.State().Capabilities))
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	close(stop)
	d.Close()
	_ = supervise.Clear()
	fmt.Println("\nstopped")
	return 0
}

func downCmd() int {
	st, err := supervise.LoadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus down: %v\n", err)
		return 1
	}
	if st.SupervisorPID == 0 && len(st.Capabilities) == 0 {
		fmt.Println("no supervisor running")
		return 0
	}
	if st.SupervisorPID > 0 {
		if p, err := os.FindProcess(st.SupervisorPID); err == nil {
			_ = p.Kill()
		}
	}
	time.Sleep(300 * time.Millisecond)
	for _, cs := range st.Capabilities {
		if cs.PID > 0 {
			if p, err := os.FindProcess(cs.PID); err == nil {
				_ = p.Kill()
			}
		}
	}
	_ = supervise.Clear()
	fmt.Println("stopped")
	return 0
}

// bridgeCmd is the stdio shim an agent spawns. It forwards bytes to the shared
// backend, so the agent believes it owns an MCP server of its own.
func bridgeCmd(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: capbus bridge <capability>")
		return 2
	}
	name := args[0]
	st, err := supervise.LoadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus bridge: %v\n", err)
		return 1
	}
	cs, ok := st.Capabilities[name]
	if !ok || cs.Address == "" {
		fmt.Fprintf(os.Stderr, "capbus bridge: %q is not running. Start `capbus supervise` first.\n", name)
		return 1
	}
	conn, err := net.Dial("tcp", cs.Address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus bridge: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(conn, "%s\n", cs.Token); err != nil {
		fmt.Fprintf(os.Stderr, "capbus bridge: %v\n", err)
		conn.Close()
		return 1
	}

	doneIn := make(chan struct{})
	doneOut := make(chan struct{})
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		// Half-close, so the supervisor sees end-of-requests but we can still
		// read the answers already in flight.
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		close(doneIn)
	}()
	go func() {
		_, _ = io.Copy(os.Stdout, conn)
		close(doneOut)
	}()

	<-doneIn
	// Wait for the supervisor to finish and close, but never hang an agent:
	// if the far side stalls, give up after a bounded grace period.
	select {
	case <-doneOut:
	case <-time.After(10 * time.Second):
	}
	conn.Close()
	return 0
}

// execCmd runs one capability as the agent's child process, with its secrets
// injected into the environment. Agent configs stay credential-free.
func execCmd(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: capbus exec <capability>")
		return 2
	}
	name := args[0]

	ms, errs := registry.Load()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "! %v\n", e)
	}
	var spec *manifest.Manifest
	for i := range ms {
		if ms[i].Name == name {
			spec = &ms[i]
			break
		}
	}
	if spec == nil {
		fmt.Fprintf(os.Stderr, "capbus exec: no capability named %q\n", name)
		return 1
	}

	bin, binArgs, err := spec.Command()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus exec: %v\n", err)
		return 1
	}
	env, err := spec.ResolveEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus exec: %v\n", err)
		return 1
	}

	cmd := exec.Command(bin, binArgs...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "capbus exec: %v\n", err)
		return 1
	}
	return 0
}

func usageSecret() {
	fmt.Fprint(os.Stderr, `usage:
  capbus secret list
  capbus secret set NAME [--from-file PATH]   (value is read from stdin)
  capbus secret get NAME                      (prints the value)
  capbus secret rm NAME
`)
}

// secretCmd manages the encrypted credential store.
func secretCmd(args []string) int {
	if len(args) == 0 {
		usageSecret()
		return 2
	}
	st, err := secret.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus secret: %v\n", err)
		return 1
	}

	switch args[0] {
	case "list":
		infos, err := st.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "capbus secret: %v\n", err)
			return 1
		}
		if len(infos) == 0 {
			fmt.Println("no secrets stored")
			return 0
		}
		fmt.Printf("%-28s %s\n", "NAME", "UPDATED")
		for _, i := range infos {
			fmt.Printf("%-28s %s\n", i.Name, i.Updated.Format("2006-01-02 15:04"))
		}
		return 0

	case "set":
		if len(args) < 2 {
			usageSecret()
			return 2
		}
		name := args[1]
		var value string
		if len(args) >= 4 && args[2] == "--from-file" {
			b, err := os.ReadFile(args[3])
			if err != nil {
				fmt.Fprintf(os.Stderr, "capbus secret: %v\n", err)
				return 1
			}
			value = strings.TrimRight(string(b), "\r\n")
		} else {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "capbus secret: %v\n", err)
				return 1
			}
			value = strings.TrimRight(string(b), "\r\n")
		}
		if value == "" {
			fmt.Fprintln(os.Stderr, "capbus secret set: empty value; pipe it in: `echo VALUE | capbus secret set NAME`")
			return 2
		}
		if err := st.Set(name, value); err != nil {
			fmt.Fprintf(os.Stderr, "capbus secret: %v\n", err)
			return 1
		}
		fmt.Printf("stored %s\n", name)
		return 0

	case "get":
		if len(args) < 2 {
			usageSecret()
			return 2
		}
		v, ok, err := st.Get(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "capbus secret: %v\n", err)
			return 1
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "capbus secret: %q is not stored\n", args[1])
			return 1
		}
		fmt.Println(v)
		return 0

	case "rm", "remove", "delete":
		if len(args) < 2 {
			usageSecret()
			return 2
		}
		if err := st.Delete(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "capbus secret: %v\n", err)
			return 1
		}
		fmt.Printf("removed %s\n", args[1])
		return 0

	default:
		usageSecret()
		return 2
	}
}

// mcpCmd runs capbus as an MCP server, so an agent can manage capabilities by
// calling tools instead of asking the user to type shell commands.
func mcpCmd() int {
	srv := mcpserver.New(proto.NewReader(os.Stdin), proto.NewWriter(os.Stdout), version)
	if err := srv.Serve(); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "capbus mcp: %v\n", err)
		return 1
	}
	return 0
}

// initCmd is the "what do I do next" command: it inspects this machine and
// prints only the steps that are actually missing.
func initCmd() int {
	fmt.Printf("capbus %s — setup check\n\n", version)

	dir := registry.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "capbus init: %v\n", err)
		return 1
	}

	ms, errs := registry.Load()
	fmt.Printf("capabilities   %d declared in %s\n", len(ms), dir)
	for _, e := range errs {
		fmt.Printf("               ! %v\n", e)
	}

	var detected []string
	for _, a := range materialize.Agents() {
		if a.Detect() {
			detected = append(detected, a.Name())
		}
	}
	sort.Strings(detected)
	if len(detected) == 0 {
		fmt.Println("agents         none detected")
	} else {
		fmt.Printf("agents         %s\n", strings.Join(detected, ", "))
	}

	var missing []string
	if st, err := secret.Open(); err == nil {
		for _, m := range ms {
			if len(m.Permissions.Secrets) == 0 {
				continue
			}
			if _, miss, err := st.Resolve(m.Permissions.Secrets); err == nil {
				missing = append(missing, miss...)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) == 0 {
		fmt.Println("secrets        none missing")
	} else {
		fmt.Printf("secrets        missing %s\n", strings.Join(missing, ", "))
	}

	sharing := false
	if st, err := supervise.LoadState(); err == nil && len(st.Capabilities) > 0 {
		sharing = true
		fmt.Printf("shared backend running (pid %d), %d capability(ies)\n", st.SupervisorPID, len(st.Capabilities))
	} else {
		fmt.Println("shared backend not running")
	}

	var steps []string
	for _, s := range missing {
		if runtime.GOOS == "windows" {
			steps = append(steps, fmt.Sprintf("$env:%s | capbus secret set %s", s, s))
		} else {
			steps = append(steps, fmt.Sprintf("echo \"$%s\" | capbus secret set %s", s, s))
		}
	}
	if !sharing {
		steps = append(steps, "capbus supervise       # one backend shared by every agent; leave it running")
	}
	switch {
	case len(detected) > 0 && sharing:
		steps = append(steps, "capbus sync --shared   # point every agent at the shared backend")
	case len(detected) > 0:
		steps = append(steps, "capbus sync            # write capabilities into every agent")
	default:
		steps = append(steps, "install an agent (Claude Code, Cursor, Codex or DSH), then run capbus sync")
	}
	if len(ms) == 0 {
		steps = append(steps, fmt.Sprintf("declare a capability in %s", dir))
	}

	fmt.Println("\nnext:")
	for i, s := range steps {
		fmt.Printf("  %d. %s\n", i+1, s)
	}
	// Nothing here is fatal, so this is guidance and exits 0.
	return 0
}

// importCmd adopts MCP servers that already exist in some agent's config, so
// the user does not hand-copy YAML for plugins they installed months ago.
func importCmd(args []string) int {
	dryRun := false
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			dryRun = true
		default:
			fmt.Fprintf(os.Stderr, "capbus import: unknown flag %q\n", a)
			return 2
		}
	}

	findings := importscan.Discover()
	if len(findings) == 0 {
		fmt.Println("nothing found: no MCP servers in any agent config on this machine")
		return 0
	}

	existing, _ := registry.Load()
	declared := map[string]bool{}
	for _, m := range existing {
		declared[m.Name] = true
	}

	fmt.Printf("found %d MCP server(s) already on this machine\n\n", len(findings))
	fmt.Printf("%-22s %-10s %s\n", "NAME", "FROM", "COMMAND")
	var added, skipped []string
	for _, f := range findings {
		cmdline := strings.TrimSpace(f.Command + " " + strings.Join(f.Args, " "))
		mark := "new"
		if declared[f.Name] {
			mark = "declared"
			skipped = append(skipped, f.Name)
		}
		fmt.Printf("%-22s %-10s %s\n", f.Name, f.Origin, truncate(cmdline, 70))
		if mark == "declared" {
			continue
		}

		m := manifest.Manifest{
			Name:        f.Name,
			Description: "Imported from " + f.Origin,
			Kind:        manifest.KindMCP,
			Scope:       manifest.ScopeGlobal,
			Source:      manifest.Source{Command: f.Command, Args: f.Args, Env: f.Env},
		}
		if err := m.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", f.Name, err)
			continue
		}
		body, err := yaml.Marshal(m)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", f.Name, err)
			continue
		}
		if !dryRun {
			if err := os.MkdirAll(registry.Dir(), 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "capbus import: %v\n", err)
				return 1
			}
			p := filepath.Join(registry.Dir(), f.Name+".yaml")
			if err := os.WriteFile(p, body, 0o600); err != nil {
				fmt.Fprintf(os.Stderr, "capbus import: %v\n", err)
				return 1
			}
		}
		added = append(added, f.Name)
	}

	if dryRun {
		fmt.Printf("\ndry run: would import %d, skip %d already declared\n", len(added), len(skipped))
		return 0
	}
	fmt.Printf("\nimported %d, skipped %d already declared\n", len(added), len(skipped))
	if len(added) > 0 {
		fmt.Println("\nnext:")
		fmt.Println("  capbus sync     # make every agent see all of them")
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// scanCmd prints a read-only diagnosis. It changes nothing, on purpose: the
// report has to be trusted before anything is allowed to act on it.
func scanCmd() int {
	rep := scan.Run()

	fmt.Println("capbus scan — read only, nothing has been changed")
	fmt.Println()
	fmt.Printf("%d plugin package(s) in agent profiles, %d resolvable MCP server(s) among them\n",
		len(rep.Packages), rep.ResolvedNatives())
	fmt.Printf("%d capability(ies) declared, %d enabled\n", rep.Declared, rep.Enabled)
	if ags := scan.Agents(); len(ags) > 0 {
		fmt.Printf("agents: %s\n", strings.Join(ags, ", "))
	} else {
		fmt.Println("agents: none detected")
	}

	if len(rep.Findings) == 0 {
		fmt.Println("\nnothing to report")
		return 0
	}
	fmt.Printf("\n%d finding(s)\n", len(rep.Findings))
	for _, f := range rep.Findings {
		fmt.Printf("\n[%s] %s\n", f.Severity, f.Title)
		if f.Detail != "" {
			fmt.Println("       " + f.Detail)
		}
		for _, it := range f.Items {
			fmt.Println("       - " + it)
		}
		if f.Fix != "" {
			fmt.Println("       fix: " + f.Fix)
		}
	}
	return 0
}

// toggleCmd switches one capability on or off.
func toggleCmd(args []string, on bool) int {
	verb := "disable"
	if on {
		verb = "enable"
	}
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: capbus %s <capability>\n", verb)
		return 2
	}
	name := args[0]

	ms, _ := registry.Load()
	found := false
	for _, m := range ms {
		if m.Name == name {
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "no capability named %q — run `capbus list`\n", name)
		return 1
	}

	st, err := enabled.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus %s: %v\n", verb, err)
		return 1
	}
	st.Set(name, on)
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "capbus %s: %v\n", verb, err)
		return 1
	}
	if on {
		fmt.Printf("%s is on; run `capbus sync` to write it into your agents\n", name)
	} else {
		fmt.Printf("%s is off — it will not be written into any agent.\nrun `capbus sync` to remove it from the configs that already have it\n", name)
	}
	return 0
}

// useCmd switches a whole set at once: everything, only the always-on ones, or
// exactly one capability.
func useCmd(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: capbus use <all|default|capability>")
		return 2
	}
	ms, errs := registry.Load()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "! %v\n", e)
	}
	st, err := enabled.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus use: %v\n", err)
		return 1
	}

	switch args[0] {
	case "all":
		st.Disabled = nil
	case "default":
		var disabled []string
		always := 0
		for _, m := range ms {
			if m.Always {
				always++
				continue
			}
			disabled = append(disabled, m.Name)
		}
		st.Disabled = disabled
		if always == 0 && len(ms) > 0 {
			fmt.Println("note: no capability is marked `always: true`, so default mode switches everything off")
		}
	default:
		name := args[0]
		found := false
		var disabled []string
		for _, m := range ms {
			if m.Name == name {
				found = true
				continue
			}
			if m.Always {
				continue
			}
			disabled = append(disabled, m.Name)
		}
		if !found {
			fmt.Fprintf(os.Stderr, "no capability named %q — run `capbus list`\n", name)
			return 1
		}
		st.Disabled = disabled
	}

	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "capbus use: %v\n", err)
		return 1
	}

	injected := enabled.Filter(ms)
	fmt.Printf("%d of %d capability(ies) will be injected\n", len(injected), len(ms))
	names := make([]string, 0, len(injected))
	for _, m := range injected {
		names = append(names, m.Name)
	}
	if tokens, measured, missing := cost.Total(names); measured > 0 {
		fmt.Printf("about %d tokens on every turn", tokens)
		if missing > 0 {
			fmt.Printf(" (%d unmeasured)", missing)
		}
		fmt.Println()
	}
	fmt.Println("\nrun `capbus sync` to apply")
	return 0
}

// costCmd measures what each capability costs the model per turn.
func costCmd(args []string) int {
	refresh := false
	for _, a := range args {
		switch a {
		case "--refresh", "-r":
			refresh = true
		default:
			fmt.Fprintf(os.Stderr, "capbus cost: unknown flag %q\n", a)
			return 2
		}
	}

	ms, _ := registry.Load()
	if len(ms) == 0 {
		fmt.Println("no capabilities declared")
		return 0
	}
	st, _ := enabled.Load()

	fmt.Printf("%-22s %6s %9s %8s  %s\n", "NAME", "TOOLS", "BYTES", "TOKENS", "STATE")
	var total, measured, failed int
	for _, m := range ms {
		c, cached := cost.Get(m.Name)
		if refresh || !cached {
			r, err := probe.Measure(m, 120*time.Second)
			if err != nil {
				fmt.Printf("%-22s %6s %9s %8s  %s\n", m.Name, "?", "?", "?", "probe failed: "+truncate(err.Error(), 40))
				failed++
				continue
			}
			c = cost.Cost{
				Tools:      r.Tools,
				Bytes:      r.Bytes,
				Tokens:     cost.EstimateTokens(r.Bytes),
				MeasuredAt: time.Now(),
				ToolNames:  r.Names,
			}
			_ = cost.Put(m.Name, c)
		}
		state := "on"
		if !st.IsEnabled(m.Name) {
			state = "off"
		}
		fmt.Printf("%-22s %6d %9d %8d  %s\n", m.Name, c.Tools, c.Bytes, c.Tokens, state)
		total += c.Tokens
		measured++
	}

	fmt.Printf("\n~%d tokens on every turn, over %d measured", total, measured)
	if failed > 0 {
		fmt.Printf(" (+%d failed to probe)", failed)
	}
	fmt.Println()
	if total > 20000 {
		fmt.Println("that is a large slice of the context window: `capbus use <name>` narrows it")
	}
	return 0
}
