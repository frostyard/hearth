package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/protoutil"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/frostyard/hearth/internal/model"
	"github.com/frostyard/hearth/internal/protoschema"
)

// update rewrites the golden generated files instead of comparing them.
var update = flag.Bool("update", false, "rewrite the golden generated files")

// compileCase compiles the protos of 1 case folder and returns the plugin
// request that a protoc run would send.
func compileCase(t *testing.T, dir, params string) *pluginpb.CodeGeneratorRequest {
	t.Helper()
	caseDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	protoDir, err := filepath.Abs(filepath.Join("..", "..", "proto"))
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(caseDir, "*.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("%s holds no proto file", dir)
	}
	inputs := make([]string, len(paths))
	for i, path := range paths {
		inputs[i] = filepath.Base(path)
	}

	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{caseDir, protoDir},
		}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	files, err := compiler.Compile(t.Context(), inputs...)
	if err != nil {
		t.Fatalf("%s: %v", dir, err)
	}

	// The request holds each file after the files it imports.
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

	// A plugin reads its request from the wire, so the test does too: the
	// hearth options then parse as the types the plugin links.
	request := &pluginpb.CodeGeneratorRequest{
		ProtoFile:      protos,
		FileToGenerate: inputs,
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
	return &decoded
}

// generatedOf returns the Go file of 1 plugin response.
func generatedOf(t *testing.T, response *pluginpb.CodeGeneratorResponse) (string, []byte) {
	t.Helper()
	if response == nil {
		t.Fatal("the plugin returned no response")
	}
	for _, file := range response.GetFile() {
		if strings.HasSuffix(file.GetName(), "_hearth_mcp.go") {
			return file.GetName(), []byte(file.GetContent())
		}
	}
	t.Fatal("the plugin wrote no MCP tool")
	return "", nil
}

// readFile reads 1 whole file as its trimmed text.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// generateCase generates the golden case, and returns the generated source and
// the app's model.
func generateCase(t *testing.T) (string, *model.App) {
	t.Helper()
	dir := filepath.Join("testdata", "golden", "demo")
	params := readFile(t, filepath.Join(dir, "params.txt"))
	gen, err := protogen.Options{ParamFunc: acceptParam}.New(compileCase(t, dir, params))
	if err != nil {
		t.Fatalf("protogen: %v", err)
	}
	app, err := model.BuildGo(gen)
	if err != nil {
		t.Fatalf("BuildGo: %v", err)
	}
	if err := generate(gen); err != nil {
		t.Fatalf("generate: %v", err)
	}
	_, data := generatedOf(t, gen.Response())
	return string(data), app
}

// TestGolden checks the plugin against the committed generated file.
func TestGolden(t *testing.T) {
	dir := filepath.Join("testdata", "golden", "demo")
	params := readFile(t, filepath.Join(dir, "params.txt"))
	response, err := Generate(compileCase(t, dir, params))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	name, got := generatedOf(t, response)
	golden := filepath.Join(dir, filepath.Base(name))
	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the generated code differs from %s (run go test -update)\n--- generated ---\n%s", golden, got)
	}
}

// effectOf returns the effect constant of 1 tool in the generated source: the
// "Effect:" line that follows the tool's "Name:" line in the tools list.
func effectOf(t *testing.T, source, tool string) string {
	t.Helper()
	start := strings.Index(source, "[]mcpserver.Tool{")
	if start < 0 {
		t.Fatalf("the generated code builds no tools list:\n%s", source)
	}
	found := false
	for _, line := range strings.Split(source[start:], "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "Name:" {
			found = len(fields) == 2 && fields[1] == `"`+tool+`",`
			continue
		}
		if found && fields[0] == "Effect:" {
			return strings.TrimSuffix(strings.Join(fields, " "), ",")
		}
	}
	t.Fatalf("the generated code has no effect for the tool %q:\n%s", tool, source)
	return ""
}

// TestHintRead checks the effect of a read tool.
func TestHintRead(t *testing.T) {
	source, _ := generateCase(t)
	if got, want := effectOf(t, source, "get"), "Effect: mcpserver.EffectRead"; got != want {
		t.Errorf("get: %q, want %q", got, want)
	}
}

// TestHintWrite checks the effect of a write tool.
func TestHintWrite(t *testing.T) {
	source, _ := generateCase(t)
	if got, want := effectOf(t, source, "create"), "Effect: mcpserver.EffectWrite"; got != want {
		t.Errorf("create: %q, want %q", got, want)
	}
}

// TestHintOutward checks the effect of an outward tool.
func TestHintOutward(t *testing.T) {
	source, _ := generateCase(t)
	if got, want := effectOf(t, source, "publish"), "Effect: mcpserver.EffectOutward"; got != want {
		t.Errorf("publish: %q, want %q", got, want)
	}
}

// TestUIOnlyHasNoTool checks that an RPC that only the UI reaches is in no tool
// and in no agent API.
func TestUIOnlyHasNoTool(t *testing.T) {
	source, _ := generateCase(t)
	if strings.Contains(source, `"watch"`) {
		t.Error("the generated code has a tool for Watch, which only the UI reaches")
	}
	if strings.Contains(source, "Watch(") {
		t.Error("the agent API of the generated code has Watch, which only the UI reaches")
	}
	for _, tool := range []string{`"get"`, `"list"`, `"create"`, `"publish"`} {
		if !strings.Contains(source, tool) {
			t.Errorf("the generated code has no tool %s", tool)
		}
	}
}

// TestDescriptions checks the 3 description forms: the agent description, the
// method's comment, and 1 example line per example.
func TestDescriptions(t *testing.T) {
	source, _ := generateCase(t)
	for _, want := range []string{
		`"Read 1 note by its id.\nExample input: {\"id\": \"n-1\"}"`,
		`"List reads the notes of 1 group."`,
		`"Create a note in a group.\nExample input: {\"note\": {\"group\": \"/inbox\"}}\nExample input: {\"note\": {\"group\": \"/work/2026\"}}"`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("the generated code has no description %s:\n%s", want, source)
		}
	}
}

// TestExamplesValidate checks that each example of an agent RPC validates
// against the generated schema of its tool's input.
func TestExamplesValidate(t *testing.T) {
	source, app := generateCase(t)
	examples := 0
	for _, op := range app.Operations {
		if len(op.Examples) == 0 {
			continue
		}
		if !strings.Contains(strings.Join(op.Expose, ","), model.ExposeAgent) {
			continue
		}
		data, err := protoschema.ForMessage(op.Method.Input())
		if err != nil {
			t.Fatalf("%s: %v", op.Name, err)
		}
		if !strings.Contains(source, string(data)) {
			t.Errorf("%s: the generated code does not hold its input schema", op.Name)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", op.Name, err)
		}
		compiler := jsonschema.NewCompiler()
		const id = "https://example.com/tool.json"
		if err := compiler.AddResource(id, doc); err != nil {
			t.Fatalf("%s: %v", op.Name, err)
		}
		schema, err := compiler.Compile(id)
		if err != nil {
			t.Fatalf("%s: %v", op.Name, err)
		}
		for _, example := range op.Examples {
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(example))
			if err != nil {
				t.Fatalf("%s: %v", op.Name, err)
			}
			if err := schema.Validate(value); err != nil {
				t.Errorf("%s: the example %s does not validate: %v", op.Name, example, err)
			}
			examples++
		}
	}
	if examples == 0 {
		t.Fatal("no example of an agent RPC was checked")
	}
}

// TestFail checks that a refused app fails generation, with an error that holds
// the case's text, and writes no file.
func TestFail(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "fail", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Fatal("no failure case")
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			params := readFile(t, filepath.Join(dir, "params.txt"))
			want := readFile(t, filepath.Join(dir, "want.txt"))
			if want == "" {
				t.Fatalf("%s/want.txt is empty: it must hold a substring that the error has", dir)
			}
			response, err := Generate(compileCase(t, dir, params))
			if err == nil {
				t.Fatalf("Generate succeeded, and the error must contain %q", want)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error does not contain %q:\n%v", want, err)
			}
			if files := response.GetFile(); len(files) > 0 {
				t.Errorf("a refused app wrote %d file(s)", len(files))
			}
		})
	}
}

// TestNoAgentRPC checks that a file without an agent RPC writes no file.
func TestNoAgentRPC(t *testing.T) {
	dir := t.TempDir()
	const src = `syntax = "proto3";
package frostyard.app.v1;
import "frostyard/hearth/v1/options.proto";
option go_package = "example.com/appv1;appv1";
message Request {}
message Response {}
service Things {
  rpc Ping(Request) returns (Response) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      permission: "app:read"
      effect: EFFECT_READ
      expose: EXPOSE_UI
    };
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "app.proto"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := Generate(compileCase(t, dir, "app=app,version=0.1.0"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if files := response.GetFile(); len(files) != 0 {
		t.Errorf("an app without an agent RPC wrote %d file(s)", len(files))
	}
}

// TestAcceptsGoParams checks that the standard parameters of a Go plugin arrive
// beside hearth's own, and that the plugin reads them.
func TestAcceptsGoParams(t *testing.T) {
	dir := filepath.Join("testdata", "golden", "demo")
	request := compileCase(t, dir, "paths=source_relative,annotate_code,Mdemo.proto=example.com/demov1,app=demo,version=0.1.0")
	gen, err := protogen.Options{ParamFunc: acceptParam}.New(request)
	if err != nil {
		t.Fatalf("protogen: %v", err)
	}
	if err := generate(gen); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// annotate_code adds the .meta file of the generated 1.
	if files := gen.Response().GetFile(); len(files) != 2 {
		t.Fatalf("the response holds %d file(s), want the generated file and its annotation", len(files))
	}
}
