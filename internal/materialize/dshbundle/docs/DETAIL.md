# capbus

Declare an MCP capability once on this machine; capbus writes it into each
agent's own config in that agent's own format, and shows you what is on and
what it costs per turn.

## What the sidebar shows

One row per declared capability, with a light:

| Light | Meaning |
|---|---|
| green | called recently, working |
| yellow | declared and enabled, not called recently |
| grey | switched off |
| red | enabled but the last call failed |

The number on the right is the tokens that capability's tool schemas add to
**every** turn while it is enabled. A dash means it has not been measured yet;
run `capbus cost` to measure it.

Click a row to switch that capability on or off. This writes the same
`enabled.json` that `capbus enable` / `capbus disable` write, so the two never
disagree. Switching a capability off removes it from the agents at the next
`capbus sync`, which is how it stops costing tokens.

## How it works

The Harness Host half runs the `capbus` executable (`capbus api state`) and
answers two routes on the Harness origin. The browser never talks to a second
server and never holds a token; the web panel stays loopback-only and
token-protected, and this bundle does not embed it.

## Limits, stated plainly

- The shared backend (`capbus supervise --experimental`) is experimental and
  meant for tools-only capabilities: the backend handshakes once for every
  agent, a server that asks its client something gets an error, and
  notifications reach every attached agent.
- This panel writes only the enabled flag. It does not install, remove, or edit
  capabilities; that stays in the manifest and `capbus sync`.
- The numbers are measured per capability from a real `tools/list`, not
  estimated. A capability that has never been started has no number.
