// Command protoc-gen-hearth-manifest writes an app's manifest.json from the
// Protobuf options of its service.
//
// The plugin takes 2 parameters: app=<id> and version=<major.minor.patch>. It
// reads only the files that protoc asks it to generate, checks every rule of
// docs/specs/options.md, and writes manifest.json at the output root.
package main

import (
	"fmt"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/frostyard/hearth/internal/model"
	"github.com/frostyard/hearth/manifest"
	"github.com/frostyard/hearth/schema"
)

// fileName is the name of the file the plugin writes: the manifest, at the
// output root.
const fileName = "manifest.json"

func main() {
	protogen.Options{ParamFunc: acceptParam}.Run(generate)
}

// acceptParam accepts every plugin parameter. The model reads them itself, so
// that 1 reading checks them for both the plugin and its tests.
func acceptParam(_, _ string) error {
	return nil
}

// Generate generates 1 plugin request. It is the plugin's whole work, without
// the standard input and output, so that a test runs it in memory.
func Generate(req *pluginpb.CodeGeneratorRequest) (*pluginpb.CodeGeneratorResponse, error) {
	gen, err := protogen.Options{ParamFunc: acceptParam}.New(req)
	if err != nil {
		return nil, fmt.Errorf("read the plugin request: %w", err)
	}
	if err := generate(gen); err != nil {
		return nil, err
	}
	return gen.Response(), nil
}

// generate builds the app's model, writes its manifest, and adds the file to
// the plugin's response.
func generate(gen *protogen.Plugin) error {
	app, err := model.Build(gen)
	if err != nil {
		return err
	}
	data, err := manifest.Marshal(app.Manifest())
	if err != nil {
		return err
	}
	if err := schema.Validate(data); err != nil {
		return fmt.Errorf("internal error: the generated manifest does not match %s: %w", schema.File, err)
	}
	file := gen.NewGeneratedFile(fileName, "")
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", fileName, err)
	}
	return nil
}
