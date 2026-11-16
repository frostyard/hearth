package model

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/protoutil"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// buildApp compiles sources, which are named by file name, and builds the app's
// model from the files in generate only.
func buildApp(t *testing.T, params string, sources map[string]string, generate ...string) (*App, error) {
	t.Helper()
	return buildAppWith(t, Build, params, sources, generate...)
}

// buildAppWith is buildApp with a choice of Build or BuildGo.
func buildAppWith(t *testing.T, build func(*protogen.Plugin) (*App, error), params string, sources map[string]string, generate ...string) (*App, error) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range sources {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	protoDir, err := filepath.Abs(filepath.Join("..", "..", "proto"))
	if err != nil {
		t.Fatal(err)
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{dir, protoDir},
		}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	files, err := compiler.Compile(t.Context(), generate...)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var (
		protos []*descriptorpb.FileDescriptorProto
		seen   = make(map[string]bool, len(files))
	)
	var add func(protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		imports := fd.Imports()
		for i := range imports.Len() {
			add(imports.Get(i).FileDescriptor)
		}
		protos = append(protos, protoutil.ProtoFromFileDescriptor(fd))
	}
	for _, file := range files {
		add(file)
	}
	request := &pluginpb.CodeGeneratorRequest{
		ProtoFile:      protos,
		FileToGenerate: generate,
		Parameter:      proto.String(params),
	}
	encoded, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pluginpb.CodeGeneratorRequest
	if err := proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	gen, err := protogen.Options{ParamFunc: func(_, _ string) error { return nil }}.New(&decoded)
	if err != nil {
		t.Fatalf("protogen: %v", err)
	}
	return build(gen)
}

// TestSnakeCase checks the tool names of method names.
func TestSnakeCase(t *testing.T) {
	for name, want := range map[string]string{
		"CreateBookmark": "create_bookmark",
		"GetURL":         "get_url",
		"ReadHTTP":       "read_http",
		"List":           "list",
		"GetV2Thing":     "get_v2_thing",
		"ReadHTTPURL":    "read_httpurl",
		"Already_snake":  "already_snake",
		"X":              "x",
	} {
		if got := snakeCase(name); got != want {
			t.Errorf("snakeCase(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestParams checks the parameters of a plugin run.
func TestParams(t *testing.T) {
	params, err := ParseParams("app=marks,version=0.1.0")
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}
	if params.App != "marks" || params.Version != "0.1.0" {
		t.Errorf("ParseParams = %+v, want marks and 0.1.0", params)
	}
	for _, invalid := range []string{
		"",
		"version=0.1.0",
		"app=marks",
		"app=marks,version=0.1",
		"app=Marks,version=0.1.0",
		"app=marks,version=0.1.0,extra=1",
		"app=marks,surprise",
	} {
		if _, err := ParseParams(invalid); err == nil {
			t.Errorf("ParseParams(%q) = no error, want 1", invalid)
		}
	}
}

// TestSelectors checks rule 7: the selector fields of a request, as dotted
// paths, with a cycle of singular fields in the message graph.
func TestSelectors(t *testing.T) {
	const src = `syntax = "proto3";
package frostyard.app.v1;
import "frostyard/hearth/v1/options.proto";
option go_package = "example.com/app/v1;appv1";

message Node {
  string folder = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"];
  Node child = 2;
}

message GetRequest {
  Node node = 1;
}

message GetResponse {}

service Things {
  rpc Get(GetRequest) returns (GetResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
    };
  }
}
`
	app, err := buildApp(t, "app=app,version=0.1.0", map[string]string{"app.proto": src}, "app.proto")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var got []string
	for _, s := range app.Operations[0].Selectors {
		got = append(got, s.Field)
	}
	want := []string{"node.folder"}
	if !slices.Equal(got, want) {
		t.Errorf("the request's selector fields = %q, want %q", got, want)
	}
	if app.Operations[0].Method == nil {
		t.Error("the operation holds no method descriptor")
	}
}

// TestSelectorsRoot checks a selector field of the request itself.
func TestSelectorsRoot(t *testing.T) {
	const src = `syntax = "proto3";
package frostyard.app.v1;
import "frostyard/hearth/v1/options.proto";
option go_package = "example.com/app/v1;appv1";

message GetRequest {
  string folder = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"];
}

message GetResponse {}

service Things {
  rpc Get(GetRequest) returns (GetResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:read"
      effect: EFFECT_READ
      expose: EXPOSE_AGENT
    };
  }
}
`
	app, err := buildApp(t, "app=app,version=0.1.0", map[string]string{"app.proto": src}, "app.proto")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := app.Operations[0].Selectors; len(got) != 1 || got[0].Field != "folder" || got[0].Selector != "folder" {
		t.Errorf("the selectors = %+v, want the root field folder", got)
	}
	if app.Operations[0].Tool != "get" {
		t.Errorf("the tool name = %q, want get", app.Operations[0].Tool)
	}
}

// TestReadPermissionOnTopic checks rule 11: a read permission that a selector
// scopes is carried by the topic of every event that names it.
func TestReadPermissionOnTopic(t *testing.T) {
	const header = `syntax = "proto3";
package frostyard.app.v1;
import "frostyard/hearth/v1/options.proto";
option go_package = "example.com/app/v1;appv1";

message ListRequest {
  string folder = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"];
}

message ListResponse {}

message OpenRequest {}

message OpenResponse {}

service Things {
  rpc List(ListRequest) returns (ListResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
    };
  }
  rpc Open(OpenRequest) returns (OpenResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:open"
      effect: EFFECT_READ
      expose: EXPOSE_UI
    };
  }
}
`
	for _, tc := range []struct {
		name  string
		event string
		want  string // a substring of the error; empty for a valid app
	}{
		{
			name:  "the event carries the selector",
			event: `message Opened { option (frostyard.hearth.v1.hearth_event) = { object: "thing" verb: "opened" selector: "folder" read_permission: "app:read" }; }`,
		},
		{
			name:  "the read permission is scoped by no selector",
			event: `message Opened { option (frostyard.hearth.v1.hearth_event) = { object: "thing" verb: "opened" read_permission: "app:open" }; }`,
		},
		{
			name:  "the event carries no selector",
			event: `message Opened { option (frostyard.hearth.v1.hearth_event) = { object: "thing" verb: "opened" read_permission: "app:read" }; }`,
			want:  "does not carry",
		},
		{
			name:  "the event carries another selector",
			event: `message Opened { option (frostyard.hearth.v1.hearth_event) = { object: "thing" verb: "opened" selector: "group" read_permission: "app:read" }; }`,
			want:  "does not carry",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := buildApp(t, "app=app,version=0.1.0", map[string]string{"app.proto": header + tc.event}, "app.proto")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				if len(app.Events) != 1 {
					t.Fatalf("the model holds %d event(s), want 1", len(app.Events))
				}
				return
			}
			if err == nil {
				t.Fatalf("Build succeeded, and its error must contain %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not contain %q:\n%v", tc.want, err)
			}
		})
	}
}

// TestSelectorInsideCompound checks rule 7's path rule: a selector is allowed
// only on a path of singular message fields. A map value or a repeated message
// has no single value for the permission check, so generation refuses a
// selector inside 1 of them, for a UI-only RPC as well as for an agent RPC,
// also when a recursive message hides it.
func TestSelectorInsideCompound(t *testing.T) {
	const header = `syntax = "proto3";
package frostyard.app.v1;
import "frostyard/hearth/v1/options.proto";
option go_package = "example.com/app/v1;appv1";

message FolderRef {
  string name = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"];
}

message ListResponse {}

service Things {
  rpc List(ListRequest) returns (ListResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
    };
  }
}
`
	const compound = `carries the selector "folder" inside a map or a repeated field`
	for _, tc := range []struct {
		name    string
		request string
		want    string // a substring of the error; empty for a valid app
	}{
		{
			name:    "a singular message field",
			request: "message ListRequest { FolderRef folder = 1; }",
		},
		{
			name:    "a map value message",
			request: "message ListRequest { map<string, FolderRef> folders = 1; }",
			want:    `field folders.name ` + compound,
		},
		{
			name:    "a repeated message field",
			request: "message ListRequest { repeated FolderRef folders = 1; }",
			want:    `field folders.name ` + compound,
		},
		{
			name:    "a repeated message field under a singular message",
			request: "message Wrapper { repeated FolderRef folders = 1; }\nmessage ListRequest { Wrapper wrapper = 1; }",
			want:    `field wrapper.folders.name ` + compound,
		},
		{
			name:    "a map value message under a singular message",
			request: "message Wrapper { map<string, FolderRef> folders = 1; }\nmessage ListRequest { Wrapper wrapper = 1; }",
			want:    `field wrapper.folders.name ` + compound,
		},
		{
			// The walk ends: the same message, on the same kind of path, is
			// already on it.
			name:    "a singular cycle ends the walk",
			request: `message A { string f = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"]; A next = 2; }` + "\n" + "message ListRequest { A a = 1; }",
		},
		{
			// The guard keys on the message and on the path's kind, so the
			// message is read again once the path enters the repeated field.
			name:    "a recursive repeated message",
			request: `message Node { string name = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"]; repeated Node children = 2; }` + "\n" + "message ListRequest { Node node = 1; }",
			want:    `field node.children.name ` + compound,
		},
		{
			name:    "a recursive map value message",
			request: `message Node { string name = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"]; map<string, Node> children = 2; }` + "\n" + "message ListRequest { Node node = 1; }",
			want:    `field node.children.name ` + compound,
		},
		{
			name:    "a recursive message reached through a repeated field",
			request: `message Node { string name = 1 [(frostyard.hearth.v1.hearth_selector) = "folder"]; Node next = 2; }` + "\n" + "message ListRequest { repeated Node nodes = 1; }",
			want:    `field nodes.name ` + compound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := header + tc.request
			app, err := buildApp(t, "app=app,version=0.1.0", map[string]string{"app.proto": src}, "app.proto")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				if len(app.Operations[0].Selectors) != 1 {
					t.Fatalf("the selectors = %+v, want 1", app.Operations[0].Selectors)
				}
				return
			}
			if err == nil {
				t.Fatalf("Build succeeded, and its error must contain %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not contain %q:\n%v", tc.want, err)
			}
		})
	}
}

// TestOnlyGeneratedFiles checks that a file that is imported but not generated
// is not read.
func TestOnlyGeneratedFiles(t *testing.T) {
	const dependency = `syntax = "proto3";
package frostyard.dep.v1;
import "frostyard/hearth/v1/options.proto";
option go_package = "example.com/dep/v1;depv1";

message PingRequest {}
message PingResponse {}

service Dep {
  rpc Ping(PingRequest) returns (PingResponse) {}
}
`
	const src = `syntax = "proto3";
package frostyard.app.v1;
import "frostyard/hearth/v1/options.proto";
import "dep.proto";
option go_package = "example.com/app/v1;appv1";

message PingRequest {}
message PingResponse {}

service Things {
  rpc Ping(PingRequest) returns (PingResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
    };
  }
}
`
	app, err := buildApp(t, "app=app,version=0.1.0", map[string]string{"app.proto": src, "dep.proto": dependency}, "app.proto")
	if err != nil {
		t.Fatalf("Build: %v\n(the dependency's RPC has no hearth_rpc, and it is not generated)", err)
	}
	if len(app.Operations) != 1 {
		t.Fatalf("the model holds %d operation(s), want 1", len(app.Operations))
	}
}

// TestParseGoParams checks that a Go plugin accepts the standard parameters of
// protoc-gen-go beside hearth's own, and that the manifest plugin still refuses
// them.
func TestParseGoParams(t *testing.T) {
	params, err := ParseGoParams("paths=source_relative,module=github.com/frostyard/hearth,annotate_code,annotate_code=true,Mapp.proto=example.com/app/v1,app=marks,version=0.1.0")
	if err != nil {
		t.Fatalf("ParseGoParams: %v", err)
	}
	if params.App != "marks" || params.Version != "0.1.0" {
		t.Errorf("ParseGoParams = %+v, want marks and 0.1.0", params)
	}
	for _, invalid := range []string{
		"paths=source_relative",
		"app=marks,version=0.1.0,paths=nowhere",
		"app=marks,version=0.1.0,annotate_code=maybe",
		"app=marks,version=0.1.0,Mapp.proto=",
	} {
		if _, err := ParseGoParams(invalid); err == nil {
			t.Errorf("ParseGoParams(%q) = no error, want 1", invalid)
		}
	}

	// The manifest plugin's own parameters stay strict.
	for _, strict := range []string{
		"app=marks,version=0.1.0,paths=source_relative",
		"app=marks,version=0.1.0,annotate_code",
	} {
		if _, err := ParseParams(strict); err == nil {
			t.Errorf("ParseParams(%q) = no error, want 1", strict)
		}
	}
}

// TestBuildGo checks that BuildGo builds the same model as Build when it reads
// the standard Go parameters.
func TestBuildGo(t *testing.T) {
	app, err := buildAppWith(t, BuildGo, "paths=source_relative,app=app,version=0.1.0",
		map[string]string{"app.proto": objectIDProto}, "app.proto")
	if err != nil {
		t.Fatalf("BuildGo: %v", err)
	}
	if len(app.Operations) != 4 {
		t.Fatalf("BuildGo found %d operation(s), want 4", len(app.Operations))
	}
}

// objectIDProto is an app with an object-id RPC, 1 with a nested object id, 1
// with neither, 1 whose ids are no string, and a recursive message.
const objectIDProto = `syntax = "proto3";
package frostyard.app.v1;
import "frostyard/hearth/v1/options.proto";
option go_package = "example.com/app/v1;appv1";

message Node {
  string id = 1;
  Node child = 2;
}

message GetRequest {
  string id = 1;
}

message MoveRequest {
  string note_id = 1;
  Node node = 2;
  repeated string ids = 3;
  map<string, string> labels = 4;
}

// Color is an enum, and 1 of its fields can be an object id.
enum Color {
  COLOR_UNSPECIFIED = 0;
  COLOR_RED = 1;
}

message MoveIntRequest {
  int64 id = 1;
  bytes doc_id = 2;
  Color state_id = 3;
  Node node_id = 4;
}

message GetResponse {}
message MoveResponse {}
message RootRequest {}

service Things {
  rpc Get(GetRequest) returns (GetResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
    };
  }
  rpc Move(MoveRequest) returns (MoveResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:write"
      effect: EFFECT_WRITE
      expose: EXPOSE_UI
    };
  }
  rpc MoveInt(MoveIntRequest) returns (MoveResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:write"
      effect: EFFECT_WRITE
      expose: EXPOSE_UI
    };
  }
  rpc Root(RootRequest) returns (GetResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
    };
  }
}
`

// TestObjectIDs checks the object-id walk: a singular string named "id" or
// ending in "_id", through singular message fields, and nothing in a map, a
// repeated field or a cycle.
func TestObjectIDs(t *testing.T) {
	app, err := buildApp(t, "app=app,version=0.1.0", map[string]string{"app.proto": objectIDProto}, "app.proto")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := map[string][]string{
		"frostyard.app.v1.Things.Get":     {"id"},
		"frostyard.app.v1.Things.Move":    {"note_id", "node.id"},
		"frostyard.app.v1.Things.MoveInt": {"id", "doc_id", "state_id", "node_id.id"},
		"frostyard.app.v1.Things.Root":    {},
	}
	for _, op := range app.Operations {
		got := op.ObjectIDs
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, want[op.Name]) {
			t.Errorf("%s: object ids = %q, want %q", op.Name, op.ObjectIDs, want[op.Name])
		}
	}
}
