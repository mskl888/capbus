// Command capbus is the local capability bus: declare a capability once, and
// every agent on the machine gets it.
package main

import (
	"encoding/json"
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
	"strconv"
	"strings"
	"time"

	"github.com/capbus/capbus/internal/activity"
	"github.com/capbus/capbus/internal/adopt"
	"github.com/capbus/capbus/internal/audit"
	"github.com/capbus/capbus/internal/enabled"
	"github.com/capbus/capbus/internal/i18n"
	"github.com/capbus/capbus/internal/importscan"
	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/materialize"
	"github.com/capbus/capbus/internal/mcpserver"
	"github.com/capbus/capbus/internal/panel"
	"github.com/capbus/capbus/internal/probe"
	"github.com/capbus/capbus/internal/proto"
	"github.com/capbus/capbus/internal/proxy"
	"github.com/capbus/capbus/internal/registry"
	"github.com/capbus/capbus/internal/scan"
	"github.com/capbus/capbus/internal/secret"
	"github.com/capbus/capbus/internal/supervise"
	"github.com/capbus/capbus/internal/trim"
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
	case "activity":
		os.Exit(activityCmd(os.Args[2:]))
	case "log":
		os.Exit(logCmd(os.Args[2:]))
	case "adopt":
		os.Exit(adoptCmd(os.Args[2:]))
	case "undo":
		os.Exit(undoCmd())
	case "ui":
		os.Exit(uiCmd(os.Args[2:]))
	case "inject":
		os.Exit(injectCmd(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "capbus: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `capbus - declare once, every agent gets it

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
  capbus activity [--tail N] what capabilities were actually called
  capbus log [--tail N]      what capbus itself changed on this machine
  capbus adopt [--apply] <name>  take over an MCP server an agent installed
  capbus undo                reverse the last adopt
  capbus ui [--no-open]      open the local dashboard
  capbus inject [--apply] <name>  put a capbus capability into the agent profile
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
	fmt.Println(pad(i18n.T("NAME"), 18) + pad(i18n.T("KIND"), 7) + pad(i18n.T("SCOPE"), 11) + pad(i18n.T("STATEFUL"), 9) + i18n.T("SOURCE"))
	st, _ := enabled.Load()
	for _, m := range ms {
		mark := ""
		if !st.IsEnabled(m.Name) {
			mark = "   " + i18n.T("[off]")
		}
		fmt.Println(pad(m.Name, 18) + pad(string(m.Kind), 7) + pad(string(m.Scope), 11) +
			pad(fmt.Sprint(m.Stateful), 9) + sourceString(m) + mark)
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
	// A manifest we cannot read is a manifest we cannot reason about: writing
	// the ones that did load would let a typo delete the others' entries.
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, i18n.T("refusing to sync: fix the manifest error(s) above first; nothing was written"))
		return 1
	}
	// A disabled capability is not written at all, so its tool schemas never
	// reach a model's context. Filtering after writing would save nothing.
	ms = enabled.Filter(ms)
	// A shared capability has no meaning without one process behind it: each
	// agent would keep its own copy and the two would drift apart with no error.
	if shared := requiredShared(ms); len(shared) > 0 {
		if st, err := supervise.LoadState(); err != nil || len(st.Capabilities) == 0 {
			fmt.Fprintf(os.Stderr,
				"capbus sync: these capabilities must be shared, but no shared backend is running:\n  %s\n"+
					"Start `capbus supervise` first. Writing per-agent copies would let their state diverge silently.\n",
				strings.Join(shared, "\n  "))
			return 1
		}
	}
	out := materialize.Sync(ms, opts)
	for _, e := range out.Errs {
		fmt.Fprintf(os.Stderr, "! %v\n", e)
	}
	printChanges(out.Changes, opts.DryRun)
	if out.Detected == 0 {
		if !out.Failed() {
			fmt.Println(i18n.T("no agent config found - install an agent, or run `capbus doctor`"))
		}
		return 1
	}
	if out.Failed() {
		return 1
	}
	switch {
	case opts.DryRun:
		fmt.Println()
		fmt.Println(i18n.T("dry run: nothing was written; drop --dry-run to apply"))
	case len(out.Wrote) == 0:
		fmt.Println(i18n.T("already up to date: every agent already matches the manifest"))
	default:
		for _, w := range out.Wrote {
			fmt.Println(i18n.T("wrote") + " " + w)
		}
	}
	if conflicts := out.Conflicts(); len(conflicts) > 0 {
		fmt.Fprintln(os.Stderr, i18n.T("name collision(s): these capabilities were NOT written"))
		for _, c := range conflicts {
			fmt.Fprintln(os.Stderr, "  "+c)
		}
		return 1
	}
	_ = audit.Append("sync", fmt.Sprintf("%d capabilities -> %d files", len(ms), len(out.Wrote)))
	if len(ms) > 0 {
		names := make([]string, 0, len(ms))
		for _, m := range ms {
			names = append(names, m.Name)
		}
		tokens, measured, missing := cost.Total(names)
		out := "\n" + i18n.T("%d capability(ies) injected", len(ms))
		if measured > 0 {
			out += i18n.T(", about %d tokens on every turn", tokens)
		}
		if missing > 0 {
			out += i18n.T(" (%d unmeasured: run `capbus cost`)", missing)
		}
		fmt.Println(out)
		if tokens > 20000 {
			fmt.Println(i18n.T("that is a large slice of the context window: `capbus use <name>` narrows it"))
		}
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
	fmt.Println(i18n.T("recording tool calls (metadata only) to") + " " + activity.Path())
	fmt.Println(i18n.T("clear it any time with: capbus activity --clear"))
	_ = audit.Append("supervise", fmt.Sprintf("%d capabilities", len(d.State().Capabilities)))
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
	cmd.Dir = spec.WorkDir()
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus exec: %v\n", err)
		return 1
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus exec: %v\n", err)
		return 1
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "capbus exec: %v\n", err)
		return 1
	}

	// Requests go out untouched; we only observe them.
	go func() {
		_ = proxy.Pump(stdin, os.Stdin, func(m *proto.Message) bool {
			if m.Method == "tools/call" {
				var p struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(m.Params, &p)
				_ = activity.Append(activity.Event{
					Capability: name, Kind: "call", Tool: p.Name, Bytes: len(m.Params),
				})
			}
			return false
		})
		_ = stdin.Close()
	}()

	// Responses are where the tokens go, so they are trimmed on the way back.
	_ = proxy.Pump(os.Stdout, stdout, func(m *proto.Message) bool {
		if !m.IsResponse() {
			return false
		}
		saved := trim.Result(m, trim.DefaultLimit)
		_ = activity.Append(activity.Event{
			Capability: name, Kind: "result", Bytes: len(m.Result), Trimmed: saved,
		})
		return saved > 0
	})

	if err := cmd.Wait(); err != nil {
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
	// Read-only unless the user says otherwise: these tools can rewrite every
	// agent's config, and a model should not be able to do that by itself.
	writable := false
	for _, a := range os.Args[2:] {
		if a == "--allow-writes" {
			writable = true
		}
	}
	srv := mcpserver.New(proto.NewReader(os.Stdin), proto.NewWriter(os.Stdout), version, writable)
	if err := srv.Serve(); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "capbus mcp: %v\n", err)
		return 1
	}
	return 0
}

// initCmd is the "what do I do next" command: it inspects this machine and
// prints only the steps that are actually missing.
func initCmd() int {
	fmt.Printf("capbus %s - setup check\n\n", version)

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
	return s[:n-1] + "..."
}

// displayWidth counts terminal columns, not bytes: a CJK rune occupies two, so
// padding by len() collapses every Chinese column.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWide(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6:
		return true
	}
	return false
}

// pad right-pads to n display columns.
func pad(s string, n int) string {
	if d := displayWidth(s); d < n {
		return s + strings.Repeat(" ", n-d)
	}
	return s
}

// padl right-aligns within n display columns.
func padl(s string, n int) string {
	if d := displayWidth(s); d < n {
		return strings.Repeat(" ", n-d) + s
	}
	return s
}

// activityCmd shows what agents actually called. Only metadata was recorded:
// never arguments, never results.
func activityCmd(args []string) int {
	n := 50
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--clear":
			if err := activity.Clear(); err != nil {
				fmt.Fprintf(os.Stderr, "capbus activity: %v\n", err)
				return 1
			}
			_ = audit.Append("activity-clear", "user cleared the call log")
			fmt.Println(i18n.T("activity log cleared"))
			return 0
		case "--tail":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "capbus activity: --tail needs a number")
				return 2
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v <= 0 {
				fmt.Fprintf(os.Stderr, "capbus activity: bad tail %q\n", args[i])
				return 2
			}
			n = v
		default:
			fmt.Fprintf(os.Stderr, "capbus activity: unknown flag %q\n", args[i])
			return 2
		}
	}

	events, err := activity.Tail(n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus activity: %v\n", err)
		return 1
	}
	if len(events) == 0 {
		fmt.Println(i18n.T("nothing recorded yet"))
		fmt.Println(i18n.T("calls are recorded when an agent runs a capability through capbus exec, or through the shared backend"))
		return 0
	}

	sum := activity.Summarize(events)
	names := make([]string, 0, len(sum))
	for n := range sum {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return sum[names[i]].Calls > sum[names[j]].Calls })

	fmt.Println(pad(i18n.T("CAPABILITY"), 20) + padl(i18n.T("CALLS"), 8) + "  " +
		pad(i18n.T("LAST CALL"), 16) + "  " + pad(i18n.T("TOP TOOL"), 22) + " " +
		padl(i18n.T("BYTES OUT"), 10) + " " + padl(i18n.T("TRIMMED"), 9))
	for _, n := range names {
		s := sum[n]
		top := "-"
		best := 0
		for tool, c := range s.Tools {
			if c > best {
				best, top = c, tool
			}
		}
		last := "-"
		if !s.LastCall.IsZero() {
			last = s.LastCall.Format("2006-01-02 15:04")
		}
		fmt.Println(pad(n, 20) + padl(fmt.Sprintf("%d", s.Calls), 8) + "  " +
			pad(last, 16) + "  " + pad(truncate(top, 22), 22) + " " +
			padl(humanBytes(s.BytesOut), 10) + " " + padl(humanBytes(s.Trimmed), 9))
	}
	fmt.Println()
	fmt.Println(i18n.T("%d event(s) from the last %d of %s", len(events), n, activity.Path()))
	return 0
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// scanCmd prints a read-only diagnosis. It changes nothing, on purpose: the
// report has to be trusted before anything is allowed to act on it.
func scanCmd() int {
	rep := scan.Run()

	fmt.Println(i18n.T("capbus scan - read only, nothing has been changed"))
	fmt.Println()
	fmt.Println(i18n.T("%d plugin package(s) in agent profiles, %d resolvable MCP server(s) among them",
		len(rep.Packages), rep.ResolvedNatives()))
	fmt.Println(i18n.T("%d capability(ies) declared, %d enabled", rep.Declared, rep.Enabled))
	if ags := scan.Agents(); len(ags) > 0 {
		fmt.Println(i18n.T("agents: %s", strings.Join(ags, ", ")))
	} else {
		fmt.Println(i18n.T("agents: none detected"))
	}

	if len(rep.Findings) == 0 {
		fmt.Println("\n" + i18n.T("nothing to report"))
		return 0
	}
	fmt.Println()
	fmt.Println(i18n.T("%d finding(s)", len(rep.Findings)))
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
		fmt.Fprintf(os.Stderr, "no capability named %q - run `capbus list`\n", name)
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
	_ = audit.Append("toggle", fmt.Sprintf("%s=%v", name, on))
	if on {
		fmt.Printf("%s is on; run `capbus sync` to write it into your agents\n", name)
	} else {
		fmt.Printf("%s is off - it will not be written into any agent.\nrun `capbus sync` to remove it from the configs that already have it\n", name)
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
			fmt.Fprintf(os.Stderr, "no capability named %q - run `capbus list`\n", name)
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

	fmt.Println(pad(i18n.T("NAME"), 22) + padl(i18n.T("TOOLS"), 7) + padl(i18n.T("BYTES"), 10) +
		padl(i18n.T("TOKENS"), 8) + "  " + i18n.T("STATE"))
	var total, measured, failed int
	for _, m := range ms {
		c, cached := cost.Get(m.Name)
		if refresh || !cached {
			r, err := probe.Measure(m, 120*time.Second)
			if err != nil {
				fmt.Println(pad(m.Name, 22) + padl("?", 7) + padl("?", 10) + padl("?", 8) + "  " +
					i18n.T("probe failed: ") + truncate(err.Error(), 40))
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
		state := i18n.T("on")
		if !st.IsEnabled(m.Name) {
			state = i18n.T("off")
		}
		fmt.Println(pad(m.Name, 22) + padl(fmt.Sprintf("%d", c.Tools), 7) +
			padl(fmt.Sprintf("%d", c.Bytes), 10) + padl(fmt.Sprintf("%d", c.Tokens), 8) + "  " + state)
		total += c.Tokens
		measured++
	}

	fmt.Println()
	fmt.Println(i18n.T("~%d tokens on every turn, over %d measured", total, measured) +
		func() string {
			if failed > 0 {
				return i18n.T(" (+%d failed to probe)", failed)
			}
			return ""
		}())
	if total > 20000 {
		fmt.Println(i18n.T("that is a large slice of the context window: `capbus use <name>` narrows it"))
	}
	return 0
}

// adoptCmd takes over an MCP server that an agent installed for itself.
//
// Without --apply it only prints the plan and the exact YAML it would append,
// because the change lands in someone else's profile and the user has to
// restart the agent to see the result.
func adoptCmd(args []string) int {
	apply := false
	server := ""
	for _, a := range args {
		switch {
		case a == "--apply":
			apply = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "capbus adopt: unknown flag %q\n", a)
			return 2
		default:
			if server != "" {
				fmt.Fprintln(os.Stderr, "capbus adopt: one capability at a time")
				return 2
			}
			server = a
		}
	}
	if server == "" {
		fmt.Fprintln(os.Stderr, "usage: capbus adopt [--apply] <serverName>")
		return 2
	}

	p, err := adopt.Make(server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus adopt: %v\n", err)
		return 1
	}

	fmt.Printf("adopt plan for %s\n\n", server)
	fmt.Printf("  plugin       %s\n", p.Plugin)
	fmt.Printf("  row          %s\n", p.RowID)
	fmt.Printf("  command      %s %s\n", p.Command, strings.Join(p.Args, " "))
	if p.Cwd != "" {
		fmt.Printf("  workdir      %s\n", p.Cwd)
	}
	fmt.Printf("  profile      %s\n", p.ProfilePatch)
	fmt.Printf("  plugin file  %s\n\n", p.PackagePatch)
	fmt.Println("this entry will be appended to the profile patch:")
	fmt.Println(p.Block())

	if !apply {
		fmt.Println("dry run: nothing was written. add --apply to perform the handover.")
		return 0
	}

	steps, err := adopt.Apply(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus adopt: %v\n", err)
		return 1
	}
	fmt.Println("done:")
	for _, s := range steps {
		fmt.Println("  " + s)
	}
	fmt.Println("\nrestart the agent for this to take effect, then check that its tools still appear.")
	fmt.Println("to reverse everything: capbus undo")
	return 0
}

// undoCmd restores the files the last adopt changed.
func undoCmd() int {
	steps, err := adopt.Undo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus undo: %v\n", err)
		return 1
	}
	for _, s := range steps {
		fmt.Println(s)
	}
	fmt.Println("\nrestart the agent for the restored files to take effect.")
	return 0
}

// uiCmd serves the local dashboard. It is a foreground program by design:
// bind 127.0.0.1, open a browser, stop on Ctrl-C. Nothing is exposed.
func uiCmd(args []string) int {
	noOpen := false
	port := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-open":
			noOpen = true
		case "--port":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "capbus ui: --port needs a number")
				return 2
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 0 || v > 65535 {
				fmt.Fprintf(os.Stderr, "capbus ui: bad port %q\n", args[i])
				return 2
			}
			port = v
		default:
			fmt.Fprintf(os.Stderr, "capbus ui: unknown flag %q\n", args[i])
			return 2
		}
	}

	srv, err := panel.New(port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus ui: %v\n", err)
		return 1
	}
	fmt.Println(i18n.T("capbus panel: ") + srv.URL())
	fmt.Println(i18n.T("Ctrl-C to stop. Closing this window closes the panel; it is not a service."))
	if !noOpen {
		if err := panel.OpenBrowser(srv.URL()); err != nil {
			fmt.Println(i18n.T("(could not open a browser; paste the address above)"))
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		_ = srv.Close()
	}()

	if err := srv.Serve(); err != nil {
		// Closing the listener is how Ctrl-C ends this, so an error here is the
		// normal exit path.
	}
	fmt.Println("\n" + i18n.T("panel closed"))
	return 0
}

// requiredShared lists enabled capabilities that only work when every agent
// talks to one process.
func requiredShared(ms []manifest.Manifest) []string {
	var out []string
	for _, m := range ms {
		if m.Shared {
			out = append(out, m.Name)
		}
	}
	return out
}

// injectCmd puts a capability capbus already owns into the agent's profile
// patch. The difference from adopt: there is no plugin to switch off, because
// nothing installed this one except capbus.
func injectCmd(args []string) int {
	apply := false
	name := ""
	for _, a := range args {
		switch {
		case a == "--apply":
			apply = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "capbus inject: unknown flag %q\n", a)
			return 2
		default:
			name = a
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: capbus inject [--apply] <capability>")
		return 2
	}

	pl, err := adopt.PlanFromCapability(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus inject: %v\n", err)
		return 1
	}

	fmt.Printf("inject plan for %s\n\n", name)
	fmt.Printf("  command      %s %s\n", pl.Command, strings.Join(pl.Args, " "))
	if pl.Cwd != "" {
		fmt.Printf("  workdir      %s\n", pl.Cwd)
	}
	fmt.Printf("  profile      %s\n\n", pl.ProfilePatch)
	fmt.Println("this entry will be appended to the profile patch:")
	fmt.Println(pl.Block())

	if !apply {
		fmt.Println("dry run: nothing was written. add --apply to inject.")
		return 0
	}

	steps, err := adopt.Apply(pl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus inject: %v\n", err)
		return 1
	}
	fmt.Println("done:")
	for _, s := range steps {
		fmt.Println("  " + s)
	}
	fmt.Println("\nrestart the agent for this to take effect. to reverse: capbus undo")
	return 0
}

// logCmd prints what capbus itself did: which files it rewrote, which
// capability it toggled, when it started hosting backends.
func logCmd(args []string) int {
	n := 40
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tail":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "capbus log: --tail needs a number")
				return 2
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v <= 0 {
				fmt.Fprintf(os.Stderr, "capbus log: bad tail %q\n", args[i])
				return 2
			}
			n = v
		case "--clear":
			if err := audit.Clear(); err != nil {
				fmt.Fprintf(os.Stderr, "capbus log: %v\n", err)
				return 1
			}
			fmt.Println(i18n.T("action log cleared"))
			return 0
		default:
			fmt.Fprintf(os.Stderr, "capbus log: unknown flag %q\n", args[i])
			return 2
		}
	}

	entries, err := audit.Tail(n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capbus log: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Println(i18n.T("capbus has not changed anything yet"))
		return 0
	}
	for _, e := range entries {
		fmt.Printf("%s  %-14s %s\n", e.Time.Format("01-02 15:04:05"), e.Kind, e.Detail)
	}
	fmt.Println()
	fmt.Println(i18n.T("log file: ") + audit.Path())
	return 0
}

// printChanges reports what sync did to each server, in words a person can
// check against their own config. Symbols are ASCII so a GBK console shows them.
//
// "unchanged" is only worth printing in a dry run: on a normal sync it would
// repeat every capability on every run.
func printChanges(changes []materialize.Change, verbose bool) {
	last := "\x00"
	printed := 0
	for _, c := range changes {
		if c.Action == "unchanged" && !verbose {
			continue
		}
		if c.Agent != last {
			fmt.Println("  [" + c.Agent + "]")
			last = c.Agent
		}
		sym := "="
		switch c.Action {
		case "added", "written":
			sym = "+"
		case "updated":
			sym = "~"
		case "removed":
			sym = "-"
		case "conflict":
			sym = "!"
		}
		line := "    " + sym + " " + c.Name
		if c.Detail != "" {
			line += "  (" + c.Detail + ")"
		}
		fmt.Println(line)
		printed++
	}
	if printed > 0 {
		fmt.Println()
	}
}
