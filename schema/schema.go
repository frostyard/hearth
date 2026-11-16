// Package schema holds the JSON Schema of hearth's app manifest, format major
// 1, and the validation that a manifest passes before an app or lodge reads
// it.
//
// The schema's own bytes are the contract; docs/specs/manifest.md describes
// the format in prose.
package schema

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ID is the schema's $id: the URL that the schema publishes itself under.
const ID = "https://raw.githubusercontent.com/frostyard/hearth/main/schema/manifest-v1.json"

// File is the schema's path in this repository.
const File = "schema/manifest-v1.json"

// manifestV1 is the schema of the manifest format major 1.
//
//go:embed manifest-v1.json
var manifestV1 []byte

// Bytes returns a copy of the schema's bytes.
func Bytes() []byte {
	return bytes.Clone(manifestV1)
}

// Validate checks data, a manifest in JSON, against the manifest format's
// schema. The error of an invalid manifest is 1 line: the location of the
// first problem and what the problem is.
func Validate(data []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Bytes()))
	if err != nil {
		return fmt.Errorf("read %s: %w", File, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(ID, doc); err != nil {
		return fmt.Errorf("load %s: %w", File, err)
	}
	sch, err := compiler.Compile(ID)
	if err != nil {
		return fmt.Errorf("compile %s: %w", File, err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("read the manifest: %w", err)
	}
	if err := sch.Validate(value); err != nil {
		return detail(err)
	}
	return nil
}

// detail returns the first problem of a failed validation, as 1 line.
func detail(err error) error {
	var invalid *jsonschema.ValidationError
	if !errors.As(err, &invalid) {
		return err
	}
	queue := []jsonschema.OutputUnit{*invalid.DetailedOutput()}
	for len(queue) > 0 {
		unit := queue[0]
		queue = queue[1:]
		if unit.Error != nil {
			where := unit.InstanceLocation
			if where == "" {
				where = "/"
			}
			return fmt.Errorf("at %s: %s", where, unit.Error)
		}
		queue = append(queue, unit.Errors...)
	}
	return err
}
