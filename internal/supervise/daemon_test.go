package supervise

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/proto"
)

// TestHelperBackend is not a real test: when the supervisor spawns this test
// binary with GO_WANT_HELPER=1, it behaves as a minimal MCP stdio server. That
// keeps the multiplexer tests free of npm, docker and network access.
func TestHelperBackend(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER") != "1" {
		return
	}
	rd := proto.NewReader(os.Stdin)
	wr := proto.NewWriter(os.Stdout)
	for {
		m, err := rd.Read()
		if err != nil {
			os.Exit(0)
		}
		switch m.Method {
		case "initialize":
			if gate := os.Getenv("GO_WANT_HELPER_GATE"); gate != "" {
				for {
					if _, err := os.Stat(gate); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			_ = wr.Write(proto.Response(m.ID,
				`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake-backend","version":"1.0"}}`))
		case "notifications/initialized":
			// nothing to do
		case "tools/call":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(m.Params, &p)
			res, _ := json.Marshal(map[string]any{
				"content": []map[string]string{{"type": "text", "text": "served:" + p.Name}},
			})
			_ = wr.Write(proto.Response(m.ID, string(res)))
			// A capability-wide event: every attached agent should see this.
			_ = wr.Write(proto.Notification("notifications/progress", `{"progress":1}`))
		}
	}
}

func helperManifest() manifest.Manifest {
	return manifest.Manifest{
		Name:  "fake",
		Kind:  manifest.KindMCP,
		Scope: manifest.ScopeGlobal,
		Source: manifest.Source{
			Command: os.Args[0],
			Args:    []string{"-test.run=^TestHelperBackend$"},
			Env:     map[string]string{"GO_WANT_HELPER": "1"},
		},
	}
}

type conn struct {
	c  net.Conn
	rd *proto.Reader
	wr *proto.Writer
}

func attach(t *testing.T, b *backend) *conn {
	t.Helper()
	c, err := net.Dial("tcp", b.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := c.Write([]byte(b.token + "\n")); err != nil {
		t.Fatalf("token: %v", err)
	}
	br := bufio.NewReader(c)
	return &conn{c: c, rd: proto.NewReaderFrom(br), wr: proto.NewWriter(c)}
}

func startDaemon(t *testing.T) *Daemon {
	t.Helper()
	// Tests must never write into the user's real ~/.capbus: the supervisor
	// records activity, and a test that pollutes a real panel is worse than no
	// test at all.
	t.Setenv("CAPBUS_HOME", t.TempDir())
	lg := log.New(os.Stderr, "[test] ", 0)
	d := New([]manifest.Manifest{helperManifest()}, lg)
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

func TestInitializeBeforeReadyFailsInsteadOfInventingCapabilities(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "ready")
	m := helperManifest()
	m.Source.Env["GO_WANT_HELPER_GATE"] = gate
	t.Setenv("CAPBUS_HOME", t.TempDir())
	d := New([]manifest.Manifest{m}, log.New(os.Stderr, "[test] ", 0))
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(d.Close)

	c := attach(t, d.backends[0])
	defer c.c.Close()
	if err := c.wr.Write(proto.Request(1, "initialize",
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"early","version":"0"}}`)); err != nil {
		t.Fatal(err)
	}
	_ = c.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := c.rd.Read()
	if err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	if got.Error == nil {
		t.Fatalf("an unready backend must not invent capabilities: %s", got.Result)
	}
	if got.IDString() != "1" {
		t.Fatalf("error used the wrong request id: %s", got.ID)
	}

	if err := os.WriteFile(gate, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.WaitReady(5 * time.Second); err != nil {
		t.Fatalf("backend did not become ready after the gate opened: %v", err)
	}
}

func TestBadTokenIsRejected(t *testing.T) {
	d := startDaemon(t)
	b := d.backends[0]
	c, err := net.Dial("tcp", b.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("wrong-token\n")); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err == nil && line != "" {
		var m proto.Message
		if json.Unmarshal([]byte(line), &m) == nil && m.Error == nil {
			t.Fatalf("expected an error frame, got %s", line)
		}
	}
}

// The whole point: two agents, one backend, both using JSON-RPC id 1, and
// neither receives the other's answer.
func TestTwoClientsShareOneBackend(t *testing.T) {
	d := startDaemon(t)
	if err := d.WaitReady(60 * time.Second); err != nil {
		t.Fatalf("not ready: %v", err)
	}
	b := d.backends[0]

	a := attach(t, b)
	defer a.c.Close()
	bb := attach(t, b)
	defer bb.c.Close()

	for name, cn := range map[string]*conn{"alice": a, "bob": bb} {
		_ = cn.wr.Write(proto.Request(1, "initialize",
			`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"`+name+`","version":"0"}}`))
	}
	for name, cn := range map[string]*conn{"alice": a, "bob": bb} {
		_ = cn.c.SetReadDeadline(time.Now().Add(5 * time.Second))
		m, err := cn.rd.Read()
		if err != nil {
			t.Fatalf("%s initialize: %v", name, err)
		}
		var res struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		}
		if err := json.Unmarshal(m.Result, &res); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.ServerInfo.Name != "capbus/fake" {
			t.Fatalf("%s: expected shared server name, got %q", name, res.ServerInfo.Name)
		}
	}

	// Both call with id 1 at the same time.
	_ = a.wr.Write(proto.Request(1, "tools/call", `{"name":"alice"}`))
	_ = bb.wr.Write(proto.Request(1, "tools/call", `{"name":"bob"}`))

	got := map[string]string{}
	for _, item := range []struct {
		name string
		cn   *conn
	}{{"alice", a}, {"bob", bb}} {
		_ = item.cn.c.SetReadDeadline(time.Now().Add(10 * time.Second))
		for {
			m, err := item.cn.rd.Read()
			if err != nil {
				t.Fatalf("%s read: %v", item.name, err)
			}
			if !m.IsResponse() {
				continue // skip the broadcast notification
			}
			if m.IDString() != "1" {
				t.Fatalf("%s: id was not restored, got %s", item.name, m.IDString())
			}
			var res struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(m.Result, &res); err != nil || len(res.Content) == 0 {
				t.Fatalf("%s: bad result %s", item.name, string(m.Result))
			}
			got[item.name] = res.Content[0].Text
			break
		}
	}
	if got["alice"] != "served:alice" || got["bob"] != "served:bob" {
		t.Fatalf("responses crossed or lost: %v", got)
	}
}

// Closing the daemon must stop the restart loop, not race it. Without this,
// Ctrl-C kills the child and the loop spawns another one forever.
func TestCloseStopsRestartLoop(t *testing.T) {
	lg := log.New(os.Stderr, "[test] ", 0)
	d := New([]manifest.Manifest{helperManifest()}, lg)
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := d.WaitReady(60 * time.Second); err != nil {
		t.Fatalf("not ready: %v", err)
	}
	b := d.backends[0]

	d.Close()

	b.mu.Lock()
	gen := b.gen
	b.mu.Unlock()

	time.Sleep(700 * time.Millisecond)

	b.mu.Lock()
	later := b.gen
	b.mu.Unlock()
	if later != gen {
		t.Fatalf("restart loop survived Close: generation %d -> %d", gen, later)
	}
}

// An agent may send its requests and then half-close immediately (this is what
// a one-shot pipe does). The answer must still be delivered.
func TestHalfCloseStillReceivesResponse(t *testing.T) {
	d := startDaemon(t)
	if err := d.WaitReady(60 * time.Second); err != nil {
		t.Fatalf("not ready: %v", err)
	}
	b := d.backends[0]

	raw, err := net.Dial("tcp", b.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Write([]byte(b.token + "\n")); err != nil {
		t.Fatal(err)
	}
	wr := proto.NewWriter(raw)
	_ = wr.Write(proto.Request(1, "initialize", `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"p","version":"0"}}`))
	_ = wr.Write(proto.Request(2, "tools/call", `{"name":"x"}`))
	if tc, ok := raw.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}

	rd := proto.NewReader(raw)
	_ = raw.SetReadDeadline(time.Now().Add(10 * time.Second))
	for i := 0; i < 6; i++ {
		m, err := rd.Read()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.IsResponse() && m.IDString() == "2" {
			var res struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(m.Result, &res); err != nil || len(res.Content) == 0 {
				t.Fatalf("bad result: %s", string(m.Result))
			}
			if res.Content[0].Text != "served:x" {
				t.Fatalf("unexpected text %q", res.Content[0].Text)
			}
			return
		}
	}
	t.Fatal("response to the half-closed client never arrived")
}

// Capability-wide events must reach every attached agent.
func TestNotificationsAreBroadcast(t *testing.T) {
	d := startDaemon(t)
	if err := d.WaitReady(60 * time.Second); err != nil {
		t.Fatalf("not ready: %v", err)
	}
	b := d.backends[0]
	a := attach(t, b)
	defer a.c.Close()
	bb := attach(t, b)
	defer bb.c.Close()

	_ = a.wr.Write(proto.Request(7, "tools/call", `{"name":"x"}`))

	for name, cn := range map[string]*conn{"alice": a, "bob": bb} {
		_ = cn.c.SetReadDeadline(time.Now().Add(10 * time.Second))
		seen := false
		for i := 0; i < 4; i++ {
			m, err := cn.rd.Read()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if m.Method == "notifications/progress" {
				seen = true
				break
			}
		}
		if !seen {
			t.Fatalf("%s did not receive the broadcast", name)
		}
	}
}
