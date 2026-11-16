// Package manifest holds hearth's app manifest: the types of format major 1,
// the canonical JSON encoding, and the compatibility rules that `hearth
// manifest diff` applies.
//
// The format is defined by docs/specs/manifest.md and by
// schema/manifest-v1.json. The manifest is the app's contract with lodge: its
// capabilities, selectors, operations and events.
package manifest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/frostyard/hearth/schema"
)

// FormatMajor is the format major version that this package reads and writes.
// It is the value of the manifest's "hearth" member.
const FormatMajor = 1

// Manifest is an app's manifest, in format major 1.
type Manifest struct {
	// Hearth is the format major version, always FormatMajor.
	Hearth int `json:"hearth"`
	// App is the app's id, such as "marks".
	App string `json:"app"`
	// Version is the app's semantic version, such as "0.1.0".
	Version string `json:"version"`
	// Capabilities are the capabilities of the app's RPCs and events, sorted.
	Capabilities []string `json:"capabilities"`
	// Selectors are the selectors that the app's RPCs and events carry,
	// sorted.
	Selectors []string `json:"selectors"`
	// Operations are the app's RPCs, sorted by name.
	Operations []Operation `json:"operations"`
	// Events are the app's events, sorted by type.
	Events []Event `json:"events"`
}

// Operation is 1 RPC of the app's service.
type Operation struct {
	// Name is the RPC's full name, such as
	// "frostyard.frostmarks.v1.Marks.GetBookmark".
	Name string `json:"name"`
	// Permission is the capability the call needs, "<app>:<capability>".
	Permission string `json:"permission"`
	// Effect is "read", "write" or "outward".
	Effect string `json:"effect"`
	// Expose is where the RPC is reachable: "ui", "agent", or both, in that
	// order.
	Expose []string `json:"expose"`
	// Selectors are the request fields that carry a selector value, sorted
	// by field. It is always present, also when it is empty.
	Selectors []Selector `json:"selectors"`
	// Tool is the RPC's MCP tool name. It is present only when Expose holds
	// "agent".
	Tool string `json:"tool,omitzero"`
}

// Selector is 1 request field that carries a selector value.
type Selector struct {
	// Field is the dotted path of proto field names, such as
	// "bookmark.folder".
	Field string `json:"field"`
	// Selector is the selector's name, such as "folder".
	Selector string `json:"selector"`
}

// Event is 1 event that an RPC of the app publishes.
type Event struct {
	// Type is the event's CloudEvents type,
	// "org.frostyard.<app>.<object>.<verb>".
	Type string `json:"type"`
	// Message is the full name of the message that carries the event.
	Message string `json:"message"`
	// Topic is the broker topic, "<app>.<object>.<verb>", with
	// ".<selector>" when the event carries a selector.
	Topic string `json:"topic"`
	// Selector is the selector whose value the event carries. It is present
	// only when the event carries 1.
	Selector string `json:"selector,omitzero"`
	// ReadPermission is the capability a consumer needs to read the event's
	// content.
	ReadPermission string `json:"read_permission"`
}

// ErrFormat is the error of a manifest whose format major this hearth does not
// read.
var ErrFormat = errors.New("unsupported manifest format major")

// ErrInvalid is the error of a manifest that does not match the format's
// schema.
var ErrInvalid = errors.New("invalid manifest")

// Marshal writes the manifest as canonical JSON: 2-space indent, 1 final
// newline, the members in the format's order, and each array in its stated
// order. Marshal orders the arrays itself, so any Manifest encodes to the same
// bytes.
func Marshal(m *Manifest) ([]byte, error) {
	c := canonical(m)
	data, err := json.Marshal(c, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return append(data, '\n'), nil
}

// canonical returns a copy of m whose arrays are in the format's order.
func canonical(m *Manifest) *Manifest {
	c := *m
	c.Capabilities = slices.Clone(m.Capabilities)
	slices.Sort(c.Capabilities)
	c.Capabilities = slices.Compact(c.Capabilities)
	c.Selectors = slices.Clone(m.Selectors)
	slices.Sort(c.Selectors)
	c.Selectors = slices.Compact(c.Selectors)

	c.Operations = make([]Operation, len(m.Operations))
	for i, op := range m.Operations {
		op.Expose = exposeOrder(op.Expose)
		op.Selectors = slices.Clone(op.Selectors)
		slices.SortFunc(op.Selectors, func(a, b Selector) int {
			return strings.Compare(a.Field, b.Field)
		})
		if op.Selectors == nil {
			op.Selectors = []Selector{}
		}
		c.Operations[i] = op
	}
	slices.SortFunc(c.Operations, func(a, b Operation) int {
		return strings.Compare(a.Name, b.Name)
	})
	if c.Operations == nil {
		c.Operations = []Operation{}
	}

	c.Events = slices.Clone(m.Events)
	slices.SortFunc(c.Events, func(a, b Event) int {
		return strings.Compare(a.Type, b.Type)
	})
	if c.Events == nil {
		c.Events = []Event{}
	}

	c.Capabilities = nonNil(c.Capabilities)
	c.Selectors = nonNil(c.Selectors)
	return &c
}

// nonNil returns s, or an empty slice when s is nil, so that it encodes as []
// and not as null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// exposeOrder returns expose without a repeated value, in the format's order:
// ui, then agent. It returns an empty slice, never nil.
func exposeOrder(expose []string) []string {
	out := make([]string, 0, len(expose))
	for _, name := range []string{"ui", "agent"} {
		if slices.Contains(expose, name) {
			out = append(out, name)
		}
	}
	return out
}

// Parse reads a manifest: it refuses a format major that this hearth does not
// read, and a manifest that does not match schema/manifest-v1.json.
func Parse(data []byte) (*Manifest, error) {
	major, err := formatMajor(data)
	if err != nil {
		return nil, err
	}
	if major != FormatMajor {
		return nil, fmt.Errorf("%w: the manifest is major %d, and this hearth reads major %d", ErrFormat, major, FormatMajor)
	}
	if err := schema.Validate(data); err != nil {
		return nil, fmt.Errorf("%w: it does not match schema/manifest-v1.json: %v", ErrInvalid, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return &m, nil
}

// formatMajor returns the format major that data declares. It reads only that
// member, so that a manifest of a newer major is recognized before its other
// members are checked.
func formatMajor(data []byte) (int, error) {
	var head struct {
		Hearth int `json:"hearth"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return 0, fmt.Errorf("%w: it is not a JSON object: %w", ErrInvalid, err)
	}
	return head.Hearth, nil
}

// Read reads and parses the manifest at path.
func Read(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Version is a semantic version of 3 numbers, as the format's "version"
// member holds it.
type Version struct {
	Major, Minor, Patch int
}

// ParseVersion reads a version of the manifest's "version" member.
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%w: version %q is not MAJOR.MINOR.PATCH", ErrInvalid, s)
	}
	var v Version
	dst := []*int{&v.Major, &v.Minor, &v.Patch}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return Version{}, fmt.Errorf("%w: version %q is not MAJOR.MINOR.PATCH without leading zeroes", ErrInvalid, s)
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("%w: version %q is not MAJOR.MINOR.PATCH", ErrInvalid, s)
		}
		*dst[i] = n
	}
	return v, nil
}

// String writes the version as MAJOR.MINOR.PATCH.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// NextMajor reports whether the version next may carry a breaking change over
// old: next's major number is greater than old's, or, while old is a v0
// version, next's minor number is greater (svu's v0 rule). A v0 app that only
// takes a patch release may not break its contract.
func NextMajor(oldVersion, nextVersion string) (bool, error) {
	old, err := ParseVersion(oldVersion)
	if err != nil {
		return false, err
	}
	next, err := ParseVersion(nextVersion)
	if err != nil {
		return false, err
	}
	if next.Major > old.Major {
		return true, nil
	}
	if old.Major == 0 && next.Major == 0 && next.Minor > old.Minor {
		return true, nil
	}
	return false, nil
}
