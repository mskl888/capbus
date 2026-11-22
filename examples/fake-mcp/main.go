// Command fake-mcp is a minimal MCP stdio server. It exists so the supervisor
// can be exercised end to end without npm, docker or a network: it answers
// initialize, lists one tool, and echoes tool calls back.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func main() {
	fmt.Fprintf(os.Stderr, "fake-mcp: started pid=%d\n", os.Getpid())

	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	out := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(out)

	send := func(m message) {
		_ = enc.Encode(m)
		_ = out.Flush()
	}

	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			var m message
			if json.Unmarshal(line, &m) == nil {
				switch m.Method {
				case "initialize":
					send(message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(
						`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake-mcp","version":"1.0"}}`)})
				case "notifications/initialized":
					// nothing to do
				case "tools/list":
					send(message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(
						`{"tools":[{"name":"echo","description":"echo text back","inputSchema":{"type":"object"}}]}`)})
				case "tools/call":
					var p struct {
						Arguments map[string]any `json:"arguments"`
					}
					_ = json.Unmarshal(m.Params, &p)
					text, _ := p.Arguments["text"].(string)
					res, _ := json.Marshal(map[string]any{
						"content": []map[string]string{{"type": "text", "text": "echo:" + text}},
					})
					send(message{JSONRPC: "2.0", ID: m.ID, Result: res})
				}
			}
		}
		if err != nil {
			return
		}
	}
}
