// Package panel serves the local dashboard: every capability arranged around
// capbus, coloured by whether it is actually working, with what it costs.
//
// It is a foreground program, not a service: capbus ui binds 127.0.0.1, opens a
// browser, and stops on Ctrl-C. Nothing is exposed to the network, and the page
// is one embedded HTML file with no build chain, so `go build` still produces a
// single binary.
package panel

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/capbus/capbus/internal/activity"
	"github.com/capbus/capbus/internal/enabled"
	"github.com/capbus/capbus/internal/registry"
	"github.com/capbus/capbus/internal/scan"
	"github.com/capbus/capbus/internal/supervise"
	"github.com/capbus/capbus/internal/usage"
)

//go:embed web.html
var webFS embed.FS

// Node is one capability as the panel needs it.
type Node struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Scope    string `json:"scope"`
	Stateful bool   `json:"stateful"`
	Enabled  bool   `json:"enabled"`
	Tools    int    `json:"tools"`
	Tokens   int    `json:"tokens"`
	Measured bool   `json:"measured"`
	Calls    int    `json:"calls"`
	LastCall string `json:"lastCall,omitempty"`
	TopTool  string `json:"topTool,omitempty"`
	BytesOut int    `json:"bytesOut"`
	Trimmed  int    `json:"trimmed"`
	Runnable bool   `json:"runnable"`
	// Light is what the panel draws: active, stale, idle, off, dead.
	Light string `json:"light"`
	Why   string `json:"why,omitempty"`
}

// Snapshot is everything the page renders.
type Snapshot struct {
	Declared   int      `json:"declared"`
	Enabled    int      `json:"enabled"`
	Tokens     int      `json:"tokens"`
	Measured   int      `json:"measured"`
	Unmeasured int      `json:"unmeasured"`
	Supervisor bool     `json:"supervisor"`
	SuperPID   int      `json:"superPid"`
	Nodes      []Node   `json:"nodes"`
	Findings   []string `json:"findings"`
	Generated  string   `json:"generated"`
	Token      string   `json:"token"`
}

// Build reads the local state. Read-only.
func Build() Snapshot {
	ms, _ := registry.Load()
	st, _ := enabled.Load()

	events, _ := activity.Tail(4000)
	sum := activity.Summarize(events)

	snap := Snapshot{Declared: len(ms), Generated: time.Now().Format("15:04:05")}
	now := time.Now()

	for _, m := range ms {
		n := Node{
			Name: m.Name, Kind: string(m.Kind), Scope: string(m.Scope),
			Stateful: m.Stateful, Enabled: st.IsEnabled(m.Name), Light: "off",
		}
		if bin, _, err := m.Command(); err == nil {
			_, lookErr := exec.LookPath(bin)
			n.Runnable = lookErr == nil
			if !n.Runnable {
				n.Why = "缺少运行时: " + bin
			}
		}
		if c, ok := usage.Get(m.Name); ok {
			n.Tools, n.Tokens, n.Measured = c.Tools, c.Tokens, true
			snap.Tokens += c.Tokens
			snap.Measured++
		} else {
			snap.Unmeasured++
		}
		if s, ok := sum[m.Name]; ok {
			n.Calls = s.Calls
			n.BytesOut, n.Trimmed = s.BytesOut, s.Trimmed
			if !s.LastCall.IsZero() {
				n.LastCall = s.LastCall.Format("01-02 15:04")
			}
			best := 0
			for tool, c := range s.Tools {
				if c > best {
					best = c
					n.TopTool = tool
				}
			}
		}

		switch {
		case !n.Enabled:
			n.Light = "off"
		case !n.Runnable:
			n.Light = "dead"
		case n.Calls == 0:
			n.Light = "idle"
			n.Why = "从未被调用"
		case now.Sub(lastCallTime(sum, m.Name)) < 7*24*time.Hour:
			n.Light = "active"
		default:
			n.Light = "stale"
			n.Why = "超过 7 天没调用"
		}
		if n.Enabled {
			snap.Enabled++
		}
		snap.Nodes = append(snap.Nodes, n)
	}

	sort.Slice(snap.Nodes, func(i, j int) bool {
		if snap.Nodes[i].Tokens != snap.Nodes[j].Tokens {
			return snap.Nodes[i].Tokens > snap.Nodes[j].Tokens
		}
		return snap.Nodes[i].Name < snap.Nodes[j].Name
	})

	if ss, err := supervise.LoadState(); err == nil && len(ss.Capabilities) > 0 {
		snap.Supervisor = true
		snap.SuperPID = ss.SupervisorPID
	}

	for _, f := range scan.Run().Findings {
		snap.Findings = append(snap.Findings, fmt.Sprintf("[%s] %s", f.Severity, f.Title))
	}
	return snap
}

func lastCallTime(sum map[string]*activity.Summary, name string) time.Time {
	if s, ok := sum[name]; ok {
		return s.LastCall
	}
	return time.Time{}
}

// Server is the local HTTP endpoint.
type Server struct {
	ln    net.Listener
	token string
}

// New binds the panel to 127.0.0.1. Port 0 asks the OS for a free one; a
// fixed port lets a user keep the URL in a bookmark.
func New(port int) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &Server{ln: ln, token: hex.EncodeToString(b)}, nil
}

// URL is where the panel is served.
func (s *Server) URL() string { return "http://" + s.ln.Addr().String() + "/" }

// Serve blocks until the listener is closed.
func (s *Server) Serve() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		body, err := webFS.ReadFile("web.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// The page needs the token to call the write endpoint; injecting it
		// here is simpler than a cookie and keeps the server stateless.
		out := strings.Replace(string(body), "__CAPBUS_TOKEN__", s.token, 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(out))
	})

	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		snap := Build()
		snap.Token = ""
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(snap)
	})

	// Toggling is the one write the panel performs. It requires the token so a
	// random web page cannot flip capabilities behind the user's back.
	mux.HandleFunc("/api/toggle", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		if r.Header.Get("X-Capbus-Token") != s.token {
			http.Error(w, "bad token", 403)
			return
		}
		var body struct {
			Name string `json:"name"`
			On   bool   `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ms, _ := registry.Load()
		found := false
		for _, m := range ms {
			if m.Name == body.Name {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "no such capability", 404)
			return
		}
		st, err := enabled.Load()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		st.Set(body.Name, body.On)
		if err := st.Save(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(204)
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(s.ln)
}

// Close stops the listener.
func (s *Server) Close() error { return s.ln.Close() }

// OpenBrowser tries to show the URL. A failure is not fatal: the user can
// paste it.
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
