// Package mcpserver exposes capbus itself as an MCP server.
//
// An agent that has capbus attached can enumerate, register and materialise
// capabilities by calling tools - no shell, no YAML editing, no guessing at
// file paths. The tool descriptions are deliberately written as instructions:
// for an MCP client they *are* the documentation.
package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	yaml "gopkg.in/yaml.v3"

	"github.com/capbus/capbus/internal/enabled"
	"github.com/capbus/capbus/internal/manifest"
	"github.com/capbus/capbus/internal/materialize"
	"github.com/capbus/capbus/internal/proto"
	"github.com/capbus/capbus/internal/registry"
	"github.com/capbus/capbus/internal/supervise"
)

// Server speaks MCP over stdio.
type Server struct {
	in      *proto.Reader
	out     *proto.Writer
	version string
}

func New(in *proto.Reader, out *proto.Writer, version string) *Server {
	return &Server{in: in, out: out, version: version}
}

// Serve runs until the client closes the stream.
func (s *Server) Serve() error {
	for {
		m, err := s.in.Read()
		if err != nil {
			return err
		}
		switch {
		case m.Method == "initialize":
			res := map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "capbus", "version": s.version},
				"instructions": "capbus keeps one declared capability shared by every agent on this machine. " +
					"Call capbus_list first, then capbus_register to add one, then capbus_sync to write it into " +
					"every installed agent. Never put credentials in a manifest: declare them under " +
					"permissions.secrets and ask the user to run `capbus secret set NAME`.",
			}
			b, _ := json.Marshal(res)
			_ = s.out.Write(&proto.Message{JSONRPC: "2.0", ID: m.ID, Result: b})
		case m.Method == "notifications/initialized":
			// nothing to do
		case m.Method == "ping":
			_ = s.out.Write(&proto.Message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(`{}`)})
		case m.Method == "tools/list":
			_ = s.out.Write(&proto.Message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(toolsJSON)})
		case m.Method == "tools/call":
			_ = s.out.Write(s.call(m))
		case m.IsRequest():
			_ = s.out.Write(proto.ErrorResponse(m.ID, -32601, "unknown method: "+m.Method))
		}
	}
}

func (s *Server) call(m *proto.Message) *proto.Message {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return textResult(m.ID, "error: cannot parse tool call: "+err.Error(), true)
	}

	switch p.Name {
	case "capbus_list":
		return textResult(m.ID, s.list(), false)
	case "capbus_register":
		var a struct {
			Manifest string `json:"manifest"`
		}
		_ = json.Unmarshal(p.Arguments, &a)
		msg, ok := s.register(a.Manifest)
		return textResult(m.ID, msg, !ok)
	case "capbus_sync":
		var a struct {
			Shared bool `json:"shared"`
		}
		_ = json.Unmarshal(p.Arguments, &a)
		msg, ok := s.sync(a.Shared)
		return textResult(m.ID, msg, !ok)
	case "capbus_status":
		return textResult(m.ID, s.status(), false)
	}
	return textResult(m.ID, "error: unknown tool "+p.Name, true)
}

func textResult(id json.RawMessage, msg string, isErr bool) *proto.Message {
	res := map[string]any{
		"content": []map[string]string{{"type": "text", "text": msg}},
	}
	if isErr {
		res["isError"] = true
	}
	b, _ := json.Marshal(res)
	return proto.Response(id, string(b))
}

func (s *Server) list() string {
	ms, errs := registry.Load()
	var b strings.Builder
	if len(ms) == 0 {
		b.WriteString("No capabilities registered yet. Use capbus_register with a YAML manifest.\n")
	}
	for _, m := range ms {
		fmt.Fprintf(&b, "%s\tkind=%s\tscope=%s\tstateful=%v\tsource=%s\n",
			m.Name, m.Kind, m.Scope, m.Stateful, m.Executable())
	}
	for _, e := range errs {
		fmt.Fprintf(&b, "error: %v\n", e)
	}
	if st, err := supervise.LoadState(); err == nil && len(st.Capabilities) > 0 {
		fmt.Fprintf(&b, "\nshared backend running (pid %d): %d capability(ies)\n", st.SupervisorPID, len(st.Capabilities))
	} else {
		b.WriteString("\nshared backend: not running (agents will each start their own copy)\n")
	}
	return b.String()
}

func (s *Server) register(src string) (string, bool) {
	if strings.TrimSpace(src) == "" {
		return "error: manifest is empty. Pass the YAML body in the manifest argument.", false
	}
	var m manifest.Manifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		return "error: manifest is not valid YAML: " + err.Error(), false
	}
	if err := m.Validate(); err != nil {
		return "error: " + err.Error(), false
	}
	if err := os.MkdirAll(registry.Dir(), 0o755); err != nil {
		return "error: " + err.Error(), false
	}
	path := filepath.Join(registry.Dir(), m.Name+".yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		return "error: " + err.Error(), false
	}
	out := fmt.Sprintf("registered %q at %s\n", m.Name, path)
	if len(m.Permissions.Secrets) > 0 {
		out += fmt.Sprintf("This capability needs credentials. Ask the user to run:\n  capbus secret set %s\n"+
			"Do not ask for the value: it must never pass through the conversation.\n",
			strings.Join(m.Permissions.Secrets, "\n  capbus secret set "))
	}
	out += "Next: call capbus_sync to write it into every installed agent."
	return out, true
}

func (s *Server) sync(shared bool) (string, bool) {
	ms, errs := registry.Load()
	if len(ms) == 0 {
		return "error: no capabilities registered", false
	}
	if shared {
		if st, err := supervise.LoadState(); err != nil || len(st.Capabilities) == 0 {
			return "error: shared=true requires a running backend. Ask the user to run capbus supervise, " +
				"or call capbus_sync with shared=false.", false
		}
	}
	effective := enabled.Filter(ms)
	for _, m := range effective {
		if !m.Shared {
			continue
		}
		if st, err := supervise.LoadState(); err != nil || len(st.Capabilities) == 0 {
			return "error: capability " + m.Name + " must be shared, but no shared backend is running. " +
				"Ask the user to run `capbus supervise`; per-agent copies would diverge silently.", false
		}
		break
	}
	wrote, syncErrs := materialize.Sync(effective, materialize.Options{Shared: shared})
	var b strings.Builder
	for _, w := range wrote {
		b.WriteString("wrote " + w + "\n")
	}
	for _, e := range syncErrs {
		b.WriteString("error: " + e.Error() + "\n")
	}
	for _, e := range errs {
		b.WriteString("manifest error: " + e.Error() + "\n")
	}
	if len(wrote) == 0 {
		b.WriteString("error: no agent config found. Install an agent, or ask the user to run capbus doctor.\n")
	}
	return b.String(), len(wrote) > 0 && len(syncErrs) == 0
}

func (s *Server) status() string {
	var b strings.Builder
	ms, errs := registry.Load()
	fmt.Fprintf(&b, "capabilities: %d declared, %d invalid\n", len(ms), len(errs))

	var detected []string
	for _, a := range materialize.Agents() {
		if a.Detect() {
			detected = append(detected, a.Name())
		}
	}
	sort.Strings(detected)
	if len(detected) == 0 {
		b.WriteString("agents: none detected\n")
	} else {
		fmt.Fprintf(&b, "agents: %s\n", strings.Join(detected, ", "))
	}

	if st, err := supervise.LoadState(); err == nil && len(st.Capabilities) > 0 {
		names := make([]string, 0, len(st.Capabilities))
		for n := range st.Capabilities {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "shared backend: running (pid %d): %s\n", st.SupervisorPID, strings.Join(names, ", "))
	} else {
		b.WriteString("shared backend: not running\n")
	}
	return b.String()
}

const toolsJSON = `{
  "tools": [
    {
      "name": "capbus_list",
      "description": "List the capabilities registered on this machine, their scope, and whether the shared backend is running. Call this first: it tells you what already exists before you add anything.",
      "inputSchema": {"type": "object", "properties": {}, "additionalProperties": false}
    },
    {
      "name": "capbus_register",
      "description": "Register an MCP capability from a YAML manifest. Pass the full YAML body. Required fields: name, kind (mcp|skill|cli), scope (global|workspace|session), source (exactly one of npm|oci|path|command), and for stateful capabilities scope must be workspace. NEVER put credentials in the manifest: list their environment variable names under permissions.secrets, then tell the user to run 'capbus secret set NAME' themselves. If the user pastes a token into the conversation, do not store it; advise them to pass it to the CLI via stdin.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "manifest": {"type": "string", "description": "The full YAML manifest body."}
        },
        "required": ["manifest"],
        "additionalProperties": false
      }
    },
    {
      "name": "capbus_sync",
      "description": "Write every registered capability into each installed agent's own config format (Claude Code, Cursor, Codex, DSH). Run this after capbus_register. Set shared=true only when the user has 'capbus supervise' running, so agents connect to one backend instead of starting a copy each.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "shared": {"type": "boolean", "description": "Point global capabilities at the shared backend. Requires capbus supervise to be running.", "default": false}
        },
        "additionalProperties": false
      }
    },
    {
      "name": "capbus_status",
      "description": "Report declared capabilities, which agents are installed, and whether the shared backend is running. Use it to diagnose why an agent cannot see a capability.",
      "inputSchema": {"type": "object", "properties": {}, "additionalProperties": false}
    }
  ]
}`
