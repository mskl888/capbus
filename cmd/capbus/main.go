// Command capbus is the local capability bus: declare a capability once, and
// every agent on the machine gets it.
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/materialize"
	"github.com/capbus/capbus/internal/registry"
	"github.com/capbus/capbus/internal/supervise"
)

const version = "0.2.0-dev"

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
		p, detected := a.Path()
		status := "not installed"
		if detected {
			status = "ok"
		} else {
			ok = false
		}
		fmt.Printf("  %-16s %-8s %s\n", a.Name(), status, p)
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
	for _, m := range ms {
		fmt.Printf("%-16s %-6s %-10s %-9v %s\n",
			m.Name, m.Kind, m.Scope, m.Stateful, sourceString(m))
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
