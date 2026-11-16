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
		if strings.HasSuffix(file.GetName(), "_hearth_events.go") {
			return file.GetName(), []byte(file.GetContent())
		}
	}
	t.Fatal("the plugin wrote no publisher")
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

// TestNoEvent checks that a file with no event message writes no file.
func TestNoEvent(t *testing.T) {
	dir := filepath.Join("testdata", "noevent")
	params := readFile(t, filepath.Join(dir, "params.txt"))
	response, err := Generate(compileCase(t, dir, params))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if files := response.GetFile(); len(files) > 0 {
		t.Errorf("a file with no event message wrote %d file(s)", len(files))
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
// case cannot show on its own: the 3 publishers of an event with a selector, of
// an event without 1, and of a nested event message.
func TestGeneratedContent(t *testing.T) {
	response, err := Generate(compileCase(t, filepath.Join("testdata", "golden", "notes"), "app=notes,version=0.1.0"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	_, data := generatedOf(t, response)
	source := string(data)
	for _, want := range []string{
		"// Code generated by protoc-gen-hearth-events. DO NOT EDIT.",
		"var EventNoteCreated = events.Spec{",
		`App:            "notes",`,
		`Type:           "org.frostyard.notes.note.created",`,
		`Topic:          "notes.note.created.group",`,
		`Selector:       "group",`,
		`ReadPermission: "notes:read",`,
		"func NewNoteCreated(ctx context.Context, subject, path string) (events.Event, error) {",
		"return events.New(ctx, EventNoteCreated, subject, path)",
		"func EmitNoteCreated(ctx context.Context, w events.Writer, subject, path string) error {",
		"event, err := NewNoteCreated(ctx, subject, path)",
		"return w.Write(ctx, event)",
		"var EventNotePublished = events.Spec{",
		`Topic:          "notes.note.published",`,
		"func NewNotePublished(ctx context.Context, subject string) (events.Event, error) {",
		`return events.New(ctx, EventNotePublished, subject, "")`,
		"func EmitNotePublished(ctx context.Context, w events.Writer, subject string) error {",
		"var EventLifecycle_Archived = events.Spec{",
		`Type:           "org.frostyard.notes.note.archived",`,
		"func NewLifecycle_Archived(ctx context.Context, subject, path string) (events.Event, error) {",
		"func EmitLifecycle_Archived(ctx context.Context, w events.Writer, subject, path string) error {",
		"// EventNoteCreated is the event org.frostyard.notes.note.created, of the message",
		"// frostyard.notes.v1.NoteCreated, on the topic notes.note.created.group and the",
		"// NewNoteCreated returns the event org.frostyard.notes.note.created of the",
		"// object subject, at path of the selector group. Its author is the caller of",
		"// EmitNoteCreated writes the event org.frostyard.notes.note.created of the",
		"// object subject, at path of the selector group, to w: the outbox of the",
		"// change's transaction.",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("the generated code does not hold %q:\n%s", want, source)
		}
	}
	// The event without a selector has no path, so its publishers take no path.
	if strings.Contains(source, "NewNotePublished(ctx context.Context, subject, path string)") {
		t.Error("the publisher of an event without a selector takes a path")
	}
	if strings.Contains(source, `Selector:       "",`) {
		t.Error("the spec of an event without a selector names an empty selector")
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
	if _, data := generatedOf(t, gen.Response()); !bytes.Contains(data, []byte("func EmitNoteCreated")) {
		t.Error("the generated file holds no publisher")
	}
}
