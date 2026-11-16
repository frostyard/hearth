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
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
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
		if strings.HasSuffix(file.GetName(), "_hearth_authz.go") {
			return file.GetName(), []byte(file.GetContent())
		}
	}
	t.Fatal("the plugin wrote no permission check")
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

// TestGolden checks the plugin against the committed generated file of each
// golden case.
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
		})
	}
}

// TestProto3Optional checks that the plugin declares the proto3 optional
// feature, which protoc and buf require for a file with an optional field.
func TestProto3Optional(t *testing.T) {
	response, err := Generate(compileCase(t, filepath.Join("testdata", "golden", "notes"), "app=notes,version=0.1.0"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got, want := response.GetSupportedFeatures(), uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL); got&want == 0 {
		t.Errorf("the plugin's supported features are %d, and they must hold FEATURE_PROTO3_OPTIONAL (%d)", got, want)
	}
}

// TestGeneratedContent checks the parts of the generated file that the golden
// case cannot show on its own: the 2 service prefixes, the 3 kinds of check,
// and the hook of each object-id RPC.
func TestGeneratedContent(t *testing.T) {
	response, err := Generate(compileCase(t, filepath.Join("testdata", "golden", "notes"), "app=notes,version=0.1.0"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	_, data := generatedOf(t, response)
	source := string(data)
	for _, want := range []string{
		"// Code generated by protoc-gen-hearth-authz. DO NOT EDIT.",
		"type NotesAPI interface {",
		"type NotesAuthorizer[Tx any] interface {",
		"type NotesImpl[Tx any] interface {",
		"func NewNotesAuthz[Tx any](impl NotesImpl[Tx], inTx authz.TxFunc[Tx]) NotesAPI {",
		"panic(",
		"func CheckNotesGet(grant authz.Grant, req *GetRequest) error {",
		`return grant.CheckAny("notes:read")`,
		"func CheckNotesList(grant authz.Grant, req *ListRequest) error {",
		`return grant.Check("notes:read", authz.Sel{Selector: "group", Path: req.GetGroup()})`,
		"func CheckNotesCreate(grant authz.Grant, req *CreateRequest) error {",
		`return grant.Check("notes:write", authz.Sel{Selector: "group", Path: req.GetNote().GetGroup()})`,
		"func CheckNotesPing(grant authz.Grant, req *PingRequest) error {",
		`return grant.Check("notes:read")`,
		"func CheckAdminGet(grant authz.Grant, req *GetRequest) error {",
		"AuthorizeGet(ctx context.Context, tx Tx, grant authz.Grant, req *GetRequest) error",
		"AuthorizeCreate(ctx context.Context, tx Tx, grant authz.Grant, req *CreateRequest) error",
		"a.impl.AuthorizeGet(ctx, tx, grant, req)",
		"authz.WithTx(ctx, tx)",
		"impl.Ping(ctx, req)",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("the generated code does not hold %q:\n%s", want, source)
		}
	}
	// Ping has neither a selector nor an object id, so it has no hook and runs
	// outside a transaction.
	if strings.Contains(source, "AuthorizePing") {
		t.Error("the generated code has a hook for Ping, which runs on no object id")
	}
}

// TestFail checks that an invalid app fails generation, with 1 line that holds
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

// TestAcceptsGoParams checks that a go run plugin's standard parameters arrive
// beside hearth's own, and that the plugin reads them.
func TestAcceptsGoParams(t *testing.T) {
	request := compileCase(t, filepath.Join("testdata", "golden", "notes"),
		"paths=source_relative,annotate_code,Mnotes.proto=example.com/notesv1,app=notes,version=0.1.0")
	gen, err := protogen.Options{ParamFunc: acceptParam}.New(request)
	if err != nil {
		t.Fatalf("protogen: %v", err)
	}
	if err := generate(gen); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// annotate_code adds the .meta file of the generated 1.
	files := gen.Response().GetFile()
	if len(files) != 2 {
		t.Fatalf("the response holds %d file(s), want the generated file and its annotation", len(files))
	}
	if _, data := generatedOf(t, gen.Response()); !bytes.Contains(data, []byte("func CheckNotesList")) {
		t.Error("the generated file holds no check")
	}
}
