// Package probe measures what a capability costs the model: it starts the
// server, asks for its tool list, and records how big the answer is.
package probe

import (
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/proto"
)

// Result is one measurement. Bytes is the raw tools/list payload, which is what
// the client inserts into the model's context every turn.
type Result struct {
	Tools int
	Bytes int
	// Names are the tool names the server advertises. They are kept because
	// they are the only cheap evidence of whether a server holds live state
	// (names like session_open or target_select), and because the panel needs
	// them to show a user what to ask for.
	Names []string
}

// Measure performs a full handshake and tools/list, then stops the process.
// timeout should allow for a cold package download on the first run.
func Measure(m manifest.Manifest, timeout time.Duration) (Result, error) {
	bin, args, err := m.Command()
	if err != nil {
		return Result{}, err
	}
	env, err := m.ResolveEnv()
	if err != nil {
		return Result{}, err
	}

	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Dir = m.WorkDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	// A capability's own diagnostics must not become our output.
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	wr := proto.NewWriter(stdin)
	rd := proto.NewReader(stdout)
	if err := wr.Write(proto.Request(1, "initialize",
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"capbus-probe","version":"0"}}`)); err != nil {
		return Result{}, err
	}

	type outcome struct {
		r   Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		for {
			msg, err := rd.Read()
			if err != nil {
				ch <- outcome{err: err}
				return
			}
			if !msg.IsResponse() {
				continue
			}
			switch msg.IDString() {
			case "1":
				_ = wr.Write(proto.Notification("notifications/initialized", ""))
				_ = wr.Write(proto.Request(2, "tools/list", "{}"))
			case "2":
				var payload struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				}
				_ = json.Unmarshal(msg.Result, &payload)
				names := make([]string, 0, len(payload.Tools))
				for _, t := range payload.Tools {
					names = append(names, t.Name)
				}
				ch <- outcome{r: Result{Tools: len(payload.Tools), Bytes: len(msg.Result), Names: names}}
				return
			}
		}
	}()

	select {
	case o := <-ch:
		return o.r, o.err
	case <-time.After(timeout):
		return Result{}, errors.New("timed out waiting for tools/list")
	}
}
