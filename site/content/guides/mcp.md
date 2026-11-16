# MCP tools

An app on hearth serves its agent RPCs as MCP tools. The tools come from the
same Protobuf service as the app's API, so a tool's name, its schema and its
hints cannot drift from the RPC. The exact contract is
[specs/mcp-tools.md](https://github.com/frostyard/hearth/blob/main/docs/specs/mcp-tools.md).

## Which RPCs become tools

An RPC is a tool when its `hearth_rpc` exposure holds `EXPOSE_AGENT`. An RPC
that only the UI reaches is in no tool:

```proto
rpc PublishNote(PublishNoteRequest) returns (PublishNoteResponse) {
  option (frostyard.hearth.v1.hearth_rpc) = {
    permission: "demo:publish"
    effect: EFFECT_OUTWARD
    expose: EXPOSE_UI
    expose: EXPOSE_AGENT
    agent_description: "Publish a note to the outside world."
  };
}
```

A tool's name is the RPC's name in snake_case (`publish_note`), and that string
is the tool name of the app's manifest too.

## What the plugin writes

`protoc-gen-hearth-mcp` writes the part of the API that agents reach, the input
and output schema of each tool, and the function that returns the tools:

```go
func NoteServiceTools(api NoteServiceAgentAPI) []mcpserver.Tool {
	return []mcpserver.Tool{
		{
			Name:         "publish_note",
			Description:  "Publish a note to the outside world.",
			Effect:       mcpserver.EffectOutward,
			InputSchema:  json.RawMessage(noteServicePublishNoteInputSchema),
			OutputSchema: json.RawMessage(noteServicePublishNoteOutputSchema),
			Call: func(ctx context.Context, args json.RawMessage) (proto.Message, error) {
				req := &PublishNoteRequest{}
				if err := protojson.Unmarshal(args, req); err != nil {
					return nil, fmt.Errorf("invalid arguments: %w", err)
				}
				return api.PublishNote(ctx, req)
			},
		},
	}
}
```

The schemas are the JSON Schema of the proto3 JSON form of the request and the
response: properties by JSON name, no undeclared property, 64-bit integers as
strings, `bytes` as base64, enums by name, and 1 definition per recursive
message. An agent RPC may carry no `google.protobuf.Any` and no map of messages,
so a schema always describes the call exactly.

## The effect is the hint

| Effect | `readOnlyHint` | `destructiveHint` | `openWorldHint` |
| --- | --- | --- | --- |
| `read` | true | false | false |
| `write` | false | false | false |
| `outward` | false | true | true |

An agent reads the hints before it calls the tool, so the effect is worth a
careful choice: `write` is a change it can undo, and `outward` is irreversible or
leaves the system.

## Serve the tools

Pass the API that the permission check wraps, so each call is checked before the
handler runs:

```go
// The app's API, with the generated permission check of every RPC.
api := service.Wired()
tools := demov1.NoteServiceTools(api)
server := mcpserver.NewServer(&mcp.Implementation{Name: "demo", Version: "0.1.0"},
	authz.LocalCaller(), tools)
```

### Over stdio, as the local user

The app's own user starts the stdio server, so it needs no token: the process is
already the user's, and `authz.LocalCaller()` gives each call the local user's
grant.

```go
if err := mcpserver.ServeStdio(ctx, server, os.Stdin, os.Stdout); err != nil {
	return err
}
```

That is the `mcp` subcommand that an app's CLI serves to an editor or a script.

### 1 server, 1 caller

`mcpserver.NewServer` fixes 1 caller for every call of its server. It is the
caller of a process that serves 1 user: the stdio server of the local user, or a
server that 1 agent's own process builds.

Never give a server that acts as the local user to a lodge caller. A caller that
lodge authenticates arrives with `Lodge-Principal` and `Lodge-Grant` on the
app's lodge socket, and the adapter sets that caller for that **request** of the
app's API ([the permissions guide](permissions.md)). Today lodge routes an agent
to the app's API, which the app serves over Connect
([the Connect route](connect.md)), not to its MCP server. A transport that
authenticates each MCP call and picks the caller per call is a later phase; until
then, build 1 server per caller, or serve the local user alone.

## Errors an agent reads

| Case | The text |
| --- | --- |
| a refused permission check | `permission denied: needs <cap> on <selector>=<path>` |
| arguments that are no JSON object | `invalid arguments: the arguments are not 1 JSON object` |
| arguments that do not fit the request | `invalid arguments: <the decode error>` |

Each is a tool error, not a protocol error: the agent reads the text and can
correct its call.

## A description that helps

An agent chooses a tool by its name and its description. The description is the
RPC's `agent_description`, or its leading comment, and 1 line per example:

```
Create a note in a group.
Example input: {"note": {"group": "/inbox"}}
```

An example must be 1 call the tool accepts, so write a path with its leading `/`
and use the JSON names of the fields (`toGroup`, not `to_group`).

Write the description for a reader who does not have the source: what the tool
does, and when to call it.
