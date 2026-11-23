package mcpserver

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/capbus/capbus/internal/proto"
)

// run feeds requests through the server and returns what it wrote. Serve ends
// on EOF, which is exactly the shutdown path a client produces.
func run(t *testing.T, reqs ...string) []proto.Message {
	t.Helper()
	in := strings.NewReader(strings.Join(reqs, "\n") + "\n")
	var out bytes.Buffer
	srv := New(proto.NewReader(in), proto.NewWriter(&out), "test", true)
	_ = srv.Serve()

	var msgs []proto.Message
	rd := proto.NewReader(&out)
	for {
		m, err := rd.Read()
		if err != nil {
			break
		}
		msgs = append(msgs, *m)
	}
	return msgs
}

func callTool(id int, name, args string) string {
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"` +
		name + `","arguments":` + args + `}}`
}

// registerArgs encodes the argument object properly: a YAML manifest may
// legitimately contain quotes, and hand-concatenated JSON would corrupt it.
func registerArgs(manifest string) string {
	b, _ := json.Marshal(map[string]string{"manifest": manifest})
	return string(b)
}

func textOf(t *testing.T, m proto.Message) string {
	t.Helper()
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(m.Result, &res); err != nil {
		t.Fatalf("bad tool result: %s", string(m.Result))
	}
	if len(res.Content) == 0 {
		t.Fatalf("empty tool result: %s", string(m.Result))
	}
	return res.Content[0].Text
}

func isError(t *testing.T, m proto.Message) bool {
	t.Helper()
	var res struct {
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(m.Result, &res)
	return res.IsError
}

func TestInitialize(t *testing.T) {
	msgs := run(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"p","version":"0"}}}`)
	if len(msgs) != 1 {
		t.Fatalf("expected one reply, got %d", len(msgs))
	}
	var res struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(msgs[0].Result, &res); err != nil {
		t.Fatal(err)
	}
	if res.ServerInfo.Name != "capbus" {
		t.Fatalf("server name %q", res.ServerInfo.Name)
	}
	if !strings.Contains(res.Instructions, "capbus_list") {
		t.Fatal("instructions should tell the model where to start")
	}
}

func TestToolsList(t *testing.T) {
	msgs := run(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var res struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(msgs[0].Result, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(res.Tools))
	}
	for _, tool := range res.Tools {
		if len(tool.Description) < 40 {
			t.Fatalf("%s needs a description a model can act on", tool.Name)
		}
	}
}

func TestRegisterThenList(t *testing.T) {
	t.Setenv("CAPBUS_HOME", filepath.Join(t.TempDir(), ".capbus"))

	// '@' cannot start a YAML plain scalar, so the package name must be quoted.
	manifest := "name: fs\nkind: mcp\nscope: global\nsource:\n  npm: \"@modelcontextprotocol/server-filesystem\"\n"
	msgs := run(t,
		callTool(1, "capbus_register", registerArgs(manifest)),
		callTool(2, "capbus_list", `{}`),
	)
	if isError(t, msgs[0]) {
		t.Fatalf("register failed: %s", textOf(t, msgs[0]))
	}
	if !strings.Contains(textOf(t, msgs[1]), "fs") {
		t.Fatalf("list does not mention the new capability: %s", textOf(t, msgs[1]))
	}
}

// The guard that keeps two agents from sharing one live browser must survive
// the MCP path too, or an agent could bypass it by registering via a tool.
func TestRegisterRejectsStatefulGlobal(t *testing.T) {
	t.Setenv("CAPBUS_HOME", filepath.Join(t.TempDir(), ".capbus"))

	m := "name: browser\nkind: mcp\nscope: global\nstateful: true\nsource:\n  npm: \"@playwright/mcp\"\n"
	msgs := run(t, callTool(1, "capbus_register", registerArgs(m)))
	if !isError(t, msgs[0]) {
		t.Fatal("stateful + global must be rejected over MCP too")
	}
	if !strings.Contains(textOf(t, msgs[0]), "stateful") {
		t.Fatalf("error should name the rule: %s", textOf(t, msgs[0]))
	}
}

func TestUnknownToolIsAnError(t *testing.T) {
	msgs := run(t, callTool(1, "capbus_nope", `{}`))
	if !isError(t, msgs[0]) {
		t.Fatal("unknown tool must set isError")
	}
}
