package registry

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/capbus/capbus/internal/manifest"
)

// Dir returns the directory manifests are read from, honouring CAPBUS_HOME.
func Dir() string {
	if d := os.Getenv("CAPBUS_HOME"); d != "" {
		return filepath.Join(d, "capabilities")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "capabilities"
	}
	return filepath.Join(home, ".capbus", "capabilities")
}

// Dirs returns every directory searched for manifests, in priority order.
// The user's own directory wins over the repo's examples.
func Dirs() []string {
	return []string{Dir(), filepath.Join(".", "capabilities")}
}

// Load reads every *.yaml / *.yml manifest it can find. A broken manifest is
// reported but does not stop the rest from loading.
func Load() ([]manifest.Manifest, []error) {
	var out []manifest.Manifest
	var errs []error
	seen := map[string]bool{}

	for _, dir := range Dirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := filepath.Ext(e.Name())
			if ext != ".yaml" && ext != ".yml" {
				continue
			}
			m, err := manifest.Load(filepath.Join(dir, e.Name()))
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if seen[m.Name] {
				errs = append(errs, fmt.Errorf("duplicate capability %q (second at %s)", m.Name, m.Origin))
				continue
			}
			seen[m.Name] = true
			out = append(out, m)
		}
	}
	manifest.Sort(out)
	return out, errs
}
