// Package supervise runs one shared instance of each machine-wide capability
// and fans its stdio JSON-RPC out to any number of agents.
//
// The problem it solves: every agent wants its own copy of every MCP server, so
// a machine with four agents runs four `npx` trees and four cold starts. Here
// the expensive backend starts once, and each agent gets a thin `capbus bridge`
// shim that forwards to it.
package supervise

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/proto"
)

const (
	// handshakeTimeout covers npx downloading a package on first run.
	handshakeTimeout = 90 * time.Second
	maxBackoff       = 30 * time.Second
)

// pending remembers which client owns a backend request, so responses can be
// routed back with the caller's original id.
type pending struct {
	c      *client
	origID json.RawMessage
}

type client struct {
	conn net.Conn
	rd   *proto.Reader
	wr   *proto.Writer
	b    *backend
}

type backend struct {
	spec manifest.Manifest
	log  *log.Logger

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	wr      *proto.Writer
	rd      *proto.Reader
	clients map[*client]struct{}
	pending map[string]*pending
	initRes json.RawMessage
	nextID  int64
	gen     int
	addr    string
	token   string
	ln      net.Listener
	started time.Time

	// closed stops the restart loop. Without it, Close() kills the child and
	// run() immediately spawns another one - an unstoppable supervisor.
	closed    chan struct{}
	closeOnce sync.Once
}

// Daemon owns one backend per global capability.
type Daemon struct {
	lg       *log.Logger
	backends []*backend
}

// New builds a daemon for every global capability. Workspace-scoped
// capabilities are deliberately excluded: they hold live state and must not be
// shared between agents.
func New(ms []manifest.Manifest, lg *log.Logger) *Daemon {
	d := &Daemon{lg: lg}
	for _, m := range ms {
		if m.Scope != manifest.ScopeGlobal {
			continue
		}
		d.backends = append(d.backends, &backend{
			spec:    m,
			log:     lg,
			clients: map[*client]struct{}{},
			pending: map[string]*pending{},
			token:   randomToken(),
			closed:  make(chan struct{}),
		})
	}
	return d
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

// Start binds a loopback listener per capability and starts the backing
// processes. It returns as soon as listeners are up; processes start async.
func (d *Daemon) Start() error {
	for _, b := range d.backends {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("listen for %s: %w", b.spec.Name, err)
		}
		b.ln = ln
		b.addr = ln.Addr().String()
		b.started = time.Now()
		go b.acceptLoop()
		go b.run()
		d.lg.Printf("%s: endpoint %s", b.spec.Name, b.addr)
	}
	if len(d.backends) == 0 {
		return errors.New("no global capabilities declared")
	}
	return nil
}

// State snapshots what bridges need.
func (d *Daemon) State() *State {
	s := &State{Capabilities: map[string]CapState{}}
	for _, b := range d.backends {
		b.mu.Lock()
		pid := 0
		if b.cmd != nil && b.cmd.Process != nil {
			pid = b.cmd.Process.Pid
		}
		s.Capabilities[b.spec.Name] = CapState{
			Address:    b.addr,
			Token:      b.token,
			PID:        pid,
			StartedAt:  b.started,
			Generation: b.gen,
		}
		b.mu.Unlock()
	}
	return s
}

// Close stops listeners and kills every capability process, then guarantees
// the restart loops exit.
func (d *Daemon) Close() {
	for _, b := range d.backends {
		b.shutdown()
	}
}

func (b *backend) shutdown() {
	b.closeOnce.Do(func() { close(b.closed) })
	if b.ln != nil {
		_ = b.ln.Close()
	}
	b.mu.Lock()
	cmd := b.cmd
	b.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// WaitReady blocks until every backend has completed its MCP handshake, or the
// timeout expires. Tests use it; the CLI does not need to.
func (d *Daemon) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ready := true
		for _, b := range d.backends {
			b.mu.Lock()
			n := len(b.initRes)
			b.mu.Unlock()
			if n == 0 {
				ready = false
				break
			}
		}
		if ready {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("timeout waiting for capabilities to become ready")
}

// --- listener ---------------------------------------------------------------

func (b *backend) acceptLoop() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			return
		}
		go b.handleConn(conn)
	}
}

func (b *backend) handleConn(conn net.Conn) {
	br := bufio.NewReaderSize(conn, 64<<10)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}
	if strings.TrimSpace(line) != b.token {
		_, _ = conn.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":null,\"error\":{\"code\":-32000,\"message\":\"bad token\"}}\n"))
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	c := &client{conn: conn, rd: proto.NewReaderFrom(br), wr: proto.NewWriter(conn), b: b}
	b.mu.Lock()
	b.clients[c] = struct{}{}
	n := len(b.clients)
	b.mu.Unlock()
	b.log.Printf("%s: client attached (%d)", b.spec.Name, n)

	c.serve()

	// The agent half-closed its end, but answers to requests it already made
	// are still in flight. Dropping the connection here would lose them.
	b.waitDrain(c, 10*time.Second)

	b.mu.Lock()
	delete(b.clients, c)
	n = len(b.clients)
	b.mu.Unlock()
	conn.Close()
	b.log.Printf("%s: client detached (%d)", b.spec.Name, n)
}

// waitDrain blocks until no request from this client is waiting for an answer.
func (b *backend) waitDrain(c *client, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		n := 0
		for _, p := range b.pending {
			if p.c == c {
				n++
			}
		}
		b.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// serve is one agent's conversation with the shared backend.
func (c *client) serve() {
	for {
		m, err := c.rd.Read()
		if err != nil {
			return
		}
		switch {
		case m.Method == "initialize":
			// Answer locally. The backend is already initialised; forwarding a
			// second initialize would reset or error on the shared instance.
			c.reply(m.ID, c.b.cachedInit())
		case m.Method == "notifications/initialized":
			// Nothing to forward: the handshake happened once, at startup.
		case m.IsRequest():
			c.b.forward(c, m)
		case m.IsNotification():
			if err := c.b.writeBackend(m); err != nil {
				return
			}
		}
	}
}

func (c *client) reply(id json.RawMessage, result json.RawMessage) {
	_ = c.wr.Write(&proto.Message{JSONRPC: "2.0", ID: id, Result: result})
}

// --- multiplexing -----------------------------------------------------------

func (b *backend) cachedInit() json.RawMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.initRes) == 0 {
		out, _ := json.Marshal(map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "capbus/" + b.spec.Name, "version": "shared"},
		})
		return out
	}
	var v map[string]any
	if err := json.Unmarshal(b.initRes, &v); err != nil {
		return b.initRes
	}
	// Advertise that this is a shared instance, keeping the backend's real
	// capabilities and protocol version untouched.
	v["serverInfo"] = map[string]any{"name": "capbus/" + b.spec.Name, "version": "shared"}
	out, err := json.Marshal(v)
	if err != nil {
		return b.initRes
	}
	return out
}

// forward rewrites the caller's id to a daemon-global one so two agents can
// both use id 1 without colliding.
func (b *backend) forward(c *client, m *proto.Message) {
	b.mu.Lock()
	b.nextID++
	gid := b.nextID
	key := strconv.FormatInt(gid, 10)
	b.pending[key] = &pending{c: c, origID: append(json.RawMessage(nil), m.ID...)}
	b.mu.Unlock()

	m.SetID(gid)
	if err := b.writeBackend(m); err != nil {
		b.mu.Lock()
		delete(b.pending, key)
		b.mu.Unlock()
		_ = c.wr.Write(proto.ErrorResponse(m.ID, -32000, "capability unavailable: "+err.Error()))
	}
}

func (b *backend) writeBackend(m *proto.Message) error {
	b.mu.Lock()
	w := b.wr
	b.mu.Unlock()
	if w == nil {
		return errors.New("capability process is not running")
	}
	return w.Write(m)
}

func (b *backend) broadcast(m *proto.Message) {
	b.mu.Lock()
	cs := make([]*client, 0, len(b.clients))
	for c := range b.clients {
		cs = append(cs, c)
	}
	b.mu.Unlock()
	for _, c := range cs {
		_ = c.wr.Write(m)
	}
}

// failAll answers every in-flight request with an error, so an agent sees a
// clean failure instead of hanging when the backend dies.
func (b *backend) failAll(reason string) {
	b.mu.Lock()
	ps := b.pending
	b.pending = map[string]*pending{}
	b.mu.Unlock()
	for _, p := range ps {
		_ = p.c.wr.Write(proto.ErrorResponse(p.origID, -32000, reason))
	}
}

// --- lifecycle --------------------------------------------------------------

func (b *backend) run() {
	backoff := time.Second
	for {
		select {
		case <-b.closed:
			b.log.Printf("%s: supervisor stopping", b.spec.Name)
			return
		default:
		}

		if err := b.start(); err != nil {
			b.log.Printf("%s: start failed: %v", b.spec.Name, err)
			select {
			case <-b.closed:
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		b.pump()
		b.failAll("capability process exited")
		b.teardown()
		select {
		case <-b.closed:
			return
		case <-time.After(backoff):
		}
	}
}

func (b *backend) start() error {
	name, args, err := b.spec.Command()
	if err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	// Secrets are injected here, at spawn time. Nothing on disk outside the
	// encrypted store ever sees them.
	env, err := b.spec.ResolveEnv()
	if err != nil {
		return err
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Backend diagnostics go to our stderr; stdout is reserved for protocol.
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	select {
	case <-b.closed:
		_ = cmd.Process.Kill()
		return errors.New("supervisor stopped")
	default:
	}

	wr := proto.NewWriter(stdin)
	rd := proto.NewReader(stdout)

	b.mu.Lock()
	b.cmd, b.stdin, b.wr, b.rd = cmd, stdin, wr, rd
	b.gen++
	gen := b.gen
	b.mu.Unlock()

	b.log.Printf("%s: started %s (pid %d, gen %d)", b.spec.Name, strings.Join(append([]string{name}, args...), " "), cmd.Process.Pid, gen)

	initRes, err := handshake(rd, wr, name)
	if err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("handshake: %w", err)
	}
	b.mu.Lock()
	b.initRes = initRes
	b.mu.Unlock()
	b.log.Printf("%s: ready", b.spec.Name)
	return nil
}

func (b *backend) teardown() {
	b.mu.Lock()
	cmd, stdin := b.cmd, b.stdin
	b.cmd, b.stdin, b.wr, b.rd = nil, nil, nil, nil
	b.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
}

// handshake performs the single MCP initialise with the backend and returns
// the negotiated result, which every later client will be handed.
func handshake(rd *proto.Reader, wr *proto.Writer, name string) (json.RawMessage, error) {
	req := proto.Request(1, "initialize",
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"capbus","version":"0.2.0"}}`)
	if err := wr.Write(req); err != nil {
		return nil, err
	}

	type result struct {
		m   *proto.Message
		err error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			m, err := rd.Read()
			if err != nil {
				ch <- result{nil, err}
				return
			}
			if m.IsResponse() && m.IDString() == "1" {
				ch <- result{m, nil}
				return
			}
		}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		if r.m.Error != nil {
			return nil, fmt.Errorf("%s", r.m.Error.Message)
		}
		if err := wr.Write(proto.Notification("notifications/initialized", "")); err != nil {
			return nil, err
		}
		return r.m.Result, nil
	case <-time.After(handshakeTimeout):
		return nil, fmt.Errorf("%s did not answer initialize within %s", name, handshakeTimeout)
	}
}

// pump routes backend output back to the right client.
func (b *backend) pump() {
	b.mu.Lock()
	rd := b.rd
	b.mu.Unlock()
	if rd == nil {
		return
	}
	for {
		m, err := rd.Read()
		if err != nil {
			return
		}
		switch {
		case m.IsResponse():
			key := m.IDString()
			b.mu.Lock()
			p := b.pending[key]
			delete(b.pending, key)
			b.mu.Unlock()
			if p == nil {
				continue // response to the startup handshake, or already answered
			}
			m.ID = p.origID
			_ = p.c.wr.Write(m)
		case m.IsNotification():
			b.broadcast(m)
		default:
			// Server->client requests (sampling, roots) are ambiguous with
			// several clients: dropping is honest, guessing is not.
			b.log.Printf("%s: dropped server request %q (shared instance cannot answer for one agent)", b.spec.Name, m.Method)
		}
	}
}
