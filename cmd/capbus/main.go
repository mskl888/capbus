// Command capbus is the local capability bus: declare a capability once, and
// every agent on the machine gets it.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/materialize"
	"github.com/capbus/capbus/internal/registry"
)

const version = "0.1.0-dev"

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
		os.Exit(sync(os.Args[2:]))
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
  capbus doctor          check environment, agents and capability sources
  capbus list            list declared capabilities
  capbus sync [--dry-run] write capabilities into every detected agent
  capbus version
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

func sync(args []string) int {
	dryRun := false
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			dryRun = true
		default:
			fmt.Fprintf(os.Stderr, "capbus sync: unknown flag %q\n", a)
			return 2
		}
	}
	ms, errs := registry.Load()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "! %v\n", e)
	}
	wrote, syncErrs := materialize.Sync(ms, dryRun)
	// Report real errors before the generic "nothing to do" line, or a failing
	// materializer looks indistinguishable from a machine with no agents.
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
