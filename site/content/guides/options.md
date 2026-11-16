# The options

An app on hearth writes 1 Protobuf service, and annotates it with hearth's
options. The plugins read those annotations, so the app's permissions, its MCP
tools, its events and its manifest all come from 1 set of files.

The options live in `frostyard/hearth/v1/options.proto`, and every app imports
it:

```proto
syntax = "proto3";

package frostyard.frostmarks.v1;

import "frostyard/hearth/v1/options.proto";

option go_package = "example.com/frostmarks/gen/v1;marksv1";
```

## The 3 options

| Option | On | What it declares |
| --- | --- | --- |
| `hearth_rpc` | a method | the RPC's permission, effect, exposure, agent description and examples |
| `hearth_selector` | a field | the selector name that the field carries |
| `hearth_event` | a message | the event's object, verb, selector and read permission |

The extension numbers 51370 to 51379 are hearth's in-house range until the
Protobuf global extension registry grants a number.

## Annotate an RPC

Every method needs `hearth_rpc`:

```proto
service Marks {
  rpc ListBookmarks(ListBookmarksRequest) returns (ListBookmarksResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "marks:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
      expose: EXPOSE_AGENT
      agent_description: "List the bookmarks of a folder."
      examples: "{\"folder\": {\"name\": \"reading\"}}"
    };
  }
}
```

- **`permission`** is the capability the call needs, as
  `<app>:<capability>`. The app part is the plugin's `app` parameter.
- **`effect`** is `EFFECT_READ` for a call that changes nothing,
  `EFFECT_WRITE` for a change that can be undone, and `EFFECT_OUTWARD` for an
  irreversible change or one that leaves the system.
- **`expose`** lists where the RPC is reachable: `EXPOSE_UI` for the app's own
  user interface, `EXPOSE_AGENT` for an MCP tool. At least 1 is required.
- **`agent_description`** tells an agent when to call the tool.
- **`examples`** holds request examples, each a JSON object in the proto3 JSON
  form of the request.

### An agent RPC

An RPC that agents reach must be a faithful tool: not streaming, and its
request and response messages carry neither `google.protobuf.Any` nor a map of
messages. Follow the messages your RPC names, and keep the tool's input and
output plain.

A UI-only RPC may carry `Any`, because no agent sees its schema.

### Selectors

A request field that names a selector carries `hearth_selector`:

```proto
message FolderRef {
  string name = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"];
}
```

The field must be a singular `string`. hearth finds it through the request's
message fields, so `folder.name` in this request is the selector `folder`:

```proto
message ListBookmarksRequest {
  FolderRef folder = 1;
}
```

## Declare an event

An event is 1 message with `hearth_event`:

```proto
message BookmarkCreated {
  option (frostyard.hearth.v1.hearth_event) = {
    object: "bookmark"
    verb: "created"
    selector: "folder"
    read_permission: "marks:read"
  };
  string id = 1;
}
```

The event's type is `org.frostyard.<app>.<object>.<verb>`, and its topic is
`<app>.<object>.<verb>`, plus `.<selector>` when it carries 1.

A read permission that a selector scopes must be on the topic of every event
that names it. Here, `marks:read` is scoped by the selector `folder` (1 RPC
carries it), so an event with `read_permission: "marks:read"` must carry
`selector: "folder"`. An event whose read permission no RPC scopes may carry a
selector, or none.

## Generate

Run the plugin from `buf`:

```yaml
# buf.gen.yaml
version: v2
plugins:
  - local: protoc-gen-hearth-manifest
    out: gen
    opt:
      - app=marks
      - version=0.1.0
```

```sh
buf generate
```

The plugin writes `gen/manifest.json`. When a rule fails, it writes nothing and
prints 1 line for each problem; `buf` shows the lines and exits with an error.

Look up every member of the file it writes in the
[manifest format](../reference/manifest.md).
