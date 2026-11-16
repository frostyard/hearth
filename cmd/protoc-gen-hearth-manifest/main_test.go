package main

import (
	"bytes"
	"flag"
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

	"github.com/frostyard/hearth/internal/model"
	"github.com/frostyard/hearth/schema"
)

// update rewrites the golden manifests instead of comparing them.
var update = flag.Bool("update", false, "rewrite the golden manifests")

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

// manifestOf returns the manifest of 1 plugin response.
func manifestOf(t *testing.T, response *pluginpb.CodeGeneratorResponse) []byte {
	t.Helper()
	if response == nil {
		t.Fatal("the plugin returned no response")
	}
	for _, file := range response.GetFile() {
		if file.GetName() == fileName {
			return []byte(file.GetContent())
		}
	}
	t.Fatalf("the plugin wrote no %s", fileName)
	return nil
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

// TestGolden checks the plugin against the committed manifest of each golden
// case. The bookmarks case also holds the Any of a UI-only RPC, which an agent
// RPC may not carry.
func TestGolden(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "golden", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Fatal("no golden case")
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			params := readFile(t, filepath.Join(dir, "params.txt"))
			response, err := Generate(compileCase(t, dir, params))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			got := manifestOf(t, response)
			if err := schema.Validate(got); err != nil {
				t.Fatalf("the generated manifest is not valid: %v", err)
			}
			golden := filepath.Join(dir, fileName)
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
				t.Errorf("the generated manifest differs from %s (run go test -update)\n--- generated ---\n%s", golden, got)
			}
		})
	}
}

// TestProto3Optional checks that the plugin declares the proto3 optional
// feature: protoc and buf refuse a plugin that does not, for a file with an
// optional field. The bookmarks golden case has 1.
func TestProto3Optional(t *testing.T) {
	response, err := Generate(compileCase(t, filepath.Join("testdata", "golden", "bookmarks"), "app=marks,version=0.1.0"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got, want := response.GetSupportedFeatures(), uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL); got&want == 0 {
		t.Errorf("the plugin's supported features are %d, and they must hold FEATURE_PROTO3_OPTIONAL (%d)", got, want)
	}
	_ = manifestOf(t, response)
}

// TestFail checks that an invalid app fails generation, with 1 line for each
// problem, and writes no file.
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
				t.Fatalf("%s/want.txt is empty: it must hold a substring that the error has, or the case passes for any error", dir)
			}
			response, err := Generate(compileCase(t, dir, params))
			if err == nil {
				t.Fatalf("Generate succeeded, and the error must contain %q", want)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error does not contain %q:\n%v", want, err)
			}
			if files := response.GetFile(); len(files) > 0 {
				t.Errorf("an invalid app wrote %d file(s)", len(files))
			}
		})
	}
}

// TestOneProblemPerLine checks that the model reports every problem of 1 build,
// 1 line each, sorted, and each line prefixed with the full name of its
// element.
func TestOneProblemPerLine(t *testing.T) {
	dir := t.TempDir()
	const src = `syntax = "proto3";
package frostyard.fail.v1;
import "frostyard/hearth/v1/options.proto";
option go_package = "example.com/fail/v1;failv1";
message PingRequest {}
message PingResponse {}
service PingService {
  rpc Ping(PingRequest) returns (PingResponse) {
    option (frostyard.hearth.v1.hearth_rpc) = {
      expose: EXPOSE_UI
    };
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "app.proto"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := Generate(compileCase(t, dir, "app=fail,version=0.1.0"))
	if err == nil {
		t.Fatalf("Generate succeeded, and the app has no permission and no effect: %v", response)
	}
	lines := strings.Split(err.Error(), "\n")
	if len(lines) != 2 {
		t.Fatalf("the error has %d line(s), want 2:\n%v", len(lines), err)
	}
	if !slices.IsSorted(lines) {
		t.Errorf("the error's lines are not sorted: %q", lines)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "frostyard.fail.v1.PingService.Ping: ") {
			t.Errorf("the line %q does not start with the method's full name", line)
		}
	}
}

// TestParams checks the plugin's parameters against the model's own reading.
func TestParams(t *testing.T) {
	params, err := model.ParseParams("app=marks,version=0.1.0")
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}
	if params.App != "marks" || params.Version != "0.1.0" {
		t.Errorf("ParseParams = %+v, want app=marks, version=0.1.0", params)
	}
	for _, invalid := range []string{"", "app=marks", "version=0.1.0", "app=marks,version=1.0", "app=Marks,version=0.1.0", "app=marks,version=0.1.0,extra=1"} {
		if _, err := model.ParseParams(invalid); err == nil {
			t.Errorf("ParseParams(%q) = no error, want 1", invalid)
		}
	}
}

// TestOptionsParse checks that the request of a golden case carries the hearth
// options as their generated types, as a plugin run does.
func TestOptionsParse(t *testing.T) {
	request := compileCase(t, filepath.Join("testdata", "golden", "minimal"), "app=min,version=1.0.0")
	gen, err := protogen.Options{ParamFunc: acceptParam}.New(request)
	if err != nil {
		t.Fatalf("protogen: %v", err)
	}
	app, err := model.Build(gen)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(app.Operations) != 1 || app.Operations[0].Permission != "min:read" {
		t.Fatalf("Build = %+v, want 1 operation with min:read", app)
	}
}
