# 0007 — Generate MCP tools from an app's agent RPCs

- **Status:** Accepted
- **Date:** 2026-11-15

## Context

lodge routes agents to an app's tools: an agent lists the tools of the apps the
user may use, and calls 1 (frostyard/core
[ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)).
An app's RPCs already declare what an agent needs: the exposure `agent`, a
description, request examples, the effect, and the permission. They are in the
manifest that lodge reads (ADR-0004).

The same facts hold today:

- A tool's schema must be an exact description of the call, or an agent sends
  arguments the app cannot read. 1 hand-written schema per tool drifts from the
  RPC it describes.
- The proto3 JSON mapping is the app's wire format for `Connect` and for its
  other clients, so it is also what an agent sends.
- An agent must be told what a tool does before it calls it. The effect —
  `read`, `write` or `outward` — is exactly that, and the Model Context
  Protocol has hints for it (`readOnlyHint`, `destructiveHint`,
  `openWorldHint`).
- An agent understands a tool error and corrects its call; a protocol error ends
  the call with no explanation.
- The app's own user, on the app's machine, is an agent too: an editor's MCP
  client or a script starts the app's stdio server. It needs no token, because
  the process is already the user's.
- No `Connect` client is in this phase: the app's handler is a Go interface, and
  the Connect wiring comes with the example app in phase 3.

## Decision

hearth generates each MCP tool from 1 agent RPC, and serves the tools with the
Go MCP SDK (github.com/modelcontextprotocol/go-sdk, v1).

- **1 tool per agent RPC.** `protoc-gen-hearth-mcp` writes a tool for each RPC
  whose exposure holds `agent`, and nothing for an RPC that only the UI reaches.
  The tool's name is the model's tool name (the RPC name in snake_case), so the
  tool name in the manifest and the tool name an agent sees are the same string.
- **The schema is the proto3 JSON mapping.** The input schema is the JSON Schema
  (draft 2020-12) of the request message, and the output schema is the same of
  the response message: properties by JSON name, no property the message does
  not declare, 64-bit integers as strings, `bytes` as base64, enums by name (the
  enum `NullValue` is the JSON null), a
  recursive message as a definition under `$defs` with a `$ref`, and a real
  oneof as "none or exactly 1 of its fields". The well-known types have their
  proto3 JSON form (a `Timestamp` is a date-time string). The subset is closed:
  an agent RPC may carry no `google.protobuf.Any` and no map of messages
  (ADR-0004), so every schema describes the call exactly.
- **The request and the response are objects.** A tool's arguments and its
  result are JSON objects, so the request message and the response message must
  have a JSON object form. `Struct` and `Empty` are objects; a `Timestamp`, a
  `Duration`, a `FieldMask`, a `Value`, a `ListValue` and every wrapper type are
  not, and the plugin refuses an agent RPC that uses 1 as its request or its
  response. A schema that describes a string as an object would describe no
  call, and the tool could not work.
- **The description is the agent's.** It is the RPC's `agent_description`, or
  its leading comment, and 1 line `Example input: <json>` per example. An
  example must be a call the tool accepts, so the plugin validates each 1
  against the tool's input schema and refuses 1 that does not fit: an agent
  reads the example and copies it.
- **The effect is the hint.** `read` is read-only and opens no world; `write` is
  neither read-only nor destructive; `outward` is destructive and open. All 3
  hints are written, never left absent, because the protocol reads an absent
  `destructiveHint` or `openWorldHint` as true.
- **An error is a tool error.** A refused check returns the permission runtime's
  text as 1 text content with the error flag, and malformed arguments return
  `invalid arguments: <detail>`. The agent reads either and can correct itself.
- **The stdio server is the local user.** `mcpserver.ServeStdio` runs the tools
  over 2 streams with no token, and the app builds the server with
  `authz.LocalCaller()`, so every tool call runs as the local user.
- **1 server serves 1 caller.** `mcpserver.NewServer` fixes the caller of every
  call of that server, so an app builds 1 server per caller, and it never gives
  a server that acts as the local user to a caller that lodge authenticated.
  lodge sets the caller of the app's API, per request, on the app's lodge socket
  (ADR-0006); an agent that lodge routes reaches that API, not the app's MCP
  server. A transport that authenticates each MCP call and picks the caller per
  call is a later phase.
- **The tools take a Go interface.** `NotesTools(api NotesAgentAPI)` takes the
  app's handler, so an in-process call is 1 method call; a `Connect` client that
  satisfies the same interface plugs in without a change to the generated code.
  The Connect wiring comes with the example app of phase 3.

## Consequences

- 1 Protobuf service gives an app its tools, its schemas, its hints and its
  descriptions, and they cannot drift from its API.
- The tool list follows the exposure: an RPC that only the UI reaches is not a
  tool, and it is not in the agent API either.
- An agent RPC's shape is narrower than the UI's: no `Any` and no map of a
  message, which is what makes the schema a faithful description of the call.
- An app that leans on `Any` or a map of messages for an agent RPC must change
  its API, or expose the RPC to the UI alone.
- The tool text is 1 more user-visible surface: a change of a description or an
  example is a change an agent sees.
- The SDK is 1 more dependency, pinned in `go.mod`, and its protocol version is
  the SDK's: hearth writes no JSON-RPC frame itself.
- A stdio server has no authentication of its own. It runs as the user who
  started it, which is exactly the local user's grant; an app that serves stdio
  on behalf of another user must not exist.

## Alternatives considered

- **A hand-written tool per RPC in each app:** rejected. It is the schema the
  app's API already gives, written twice, and the copy that drifts is the one an
  agent reads.
- **The `Connect` handler as the tool's call directly:** deferred, not rejected.
  The tools take a plain Go interface, which a Connect client satisfies; wiring
  Connect into the demo needs the example app, which is phase 3's work.
- **The raw proto descriptor as the schema:** rejected. An agent's client reads
  JSON Schema, and lodge and the client would each need a Protobuf parser.
- **A hint chosen by the app in its options:** rejected. The effect is already
  declared per RPC, and 2 sources for 1 hint would drift.
- **A token on the stdio server:** rejected. The process is the user's, so a
  token would be a secret kept for no gain; the local user's grant is the
  boundary.

## References

- Shapes: [design/overview.md](../design/overview.md),
  [specs/mcp-tools.md](../specs/mcp-tools.md),
  [plan 0001, phase 2](../plans/0001-roadmap.md#phase-2--the-permission-runtime-the-lodge-adapter-and-mcp-built-2026-11-15)
- Builds on: [ADR-0004](0004-generate-the-manifest-from-protobuf-options.md)
  (the model that holds each RPC's tool name, effect and examples),
  [ADR-0006](0006-check-permissions-in-the-app-and-trust-lodge-only-on-its-socket.md)
  (the permission a tool call runs)
- External: [Model Context Protocol](https://modelcontextprotocol.io/specification/2025-06-18/server/tools),
  [JSON Schema draft 2020-12](https://json-schema.org/draft/2020-12),
  [Protobuf JSON mapping](https://protobuf.dev/programming-guides/json/)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)
