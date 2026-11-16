// Package authz is hearth's permission runtime: a grant holds capabilities,
// each on a path, and a check answers whether a caller may do 1 thing to 1
// object.
//
// The runtime is the code side of docs/specs/permissions.md. A generated check
// (protoc-gen-hearth-authz) reads a request's selector fields and asks the
// grant; a generated hook resolves a stored object's selector inside the app's
// transaction. A caller arrives in the context: the lodge adapter sets it from
// lodge's headers, an app's private socket and its stdio MCP server set the
// local user.
package authz

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

var (
	// capPattern is the pattern of a capability: "<app>:<capability>".
	capPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}:[a-z][a-z0-9_]*$`)
	// selectorNamePattern is the pattern of a selector's name.
	selectorNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Item is 1 grant item: a capability, and the path it holds for each selector
// it names. A selector the item does not name is the root path for that
// selector.
type Item struct {
	// Cap is the capability: "<app>:<capability>".
	Cap string `json:"cap"`
	// Sel maps a selector's name to the path the item holds for it.
	Sel map[string]string `json:"sel,omitempty"`
}

// Grant is a set of grant items. A check passes when 1 item covers it, so the
// grant is the union of its items. The zero Grant allows nothing.
type Grant struct {
	// items are the grant's items, each with a canonical path for every
	// selector it names.
	items []Item
	// all is set by Local: the grant of the local user, which passes every
	// check on every path.
	all bool
}

// Local returns the grant of the local user: every capability on the root
// path. An app's stdio MCP server and its private socket run as this user.
func Local() Grant {
	return Grant{all: true}
}

// NewGrant returns a grant of items, with each item checked: the capability
// matches `^[a-z][a-z0-9]{0,15}:[a-z][a-z0-9_]*$`, each selector name matches
// `^[a-z][a-z0-9_]*$`, and each path is valid (see CleanPath). The items are
// copied, and their paths are canonically spelled.
func NewGrant(items ...Item) (Grant, error) {
	out := make([]Item, 0, len(items))
	for i, item := range items {
		checked, err := checkItem(item)
		if err != nil {
			return Grant{}, fmt.Errorf("grant item %d: %w", i, err)
		}
		out = append(out, checked)
	}
	return Grant{items: out}, nil
}

// ParseGrant reads a grant: a JSON array of items, each with a "cap" and an
// optional "sel". Every item is checked as NewGrant checks it, and an unknown
// member of an item is an error, so that a misspelled "sel" cannot widen the
// grant.
func ParseGrant(data []byte) (Grant, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var items []Item
	if err := decoder.Decode(&items); err != nil {
		return Grant{}, fmt.Errorf("read the grant: %w", err)
	}
	if items == nil {
		return Grant{}, errors.New("read the grant: the grant is a JSON array of items")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Grant{}, errors.New("read the grant: the data after the JSON array is not allowed")
	}
	grant, err := NewGrant(items...)
	if err != nil {
		return Grant{}, err
	}
	return grant, nil
}

// Items returns the grant's items. The local grant has none.
func (g Grant) Items() []Item {
	out := make([]Item, len(g.items))
	for i, item := range g.items {
		out[i] = Item{Cap: item.Cap}
		if len(item.Sel) == 0 {
			continue
		}
		out[i].Sel = make(map[string]string, len(item.Sel))
		for name, path := range item.Sel {
			out[i].Sel[name] = path
		}
	}
	return out
}

// MarshalJSON writes the grant as the JSON array of its items. The local grant
// has no items, so a marshalled local grant allows nothing: an app never sends
// it, and lodge never reads it.
func (g Grant) MarshalJSON() ([]byte, error) {
	items := g.items
	if items == nil {
		items = []Item{}
	}
	data, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("write the grant: %w", err)
	}
	return data, nil
}

// Check reports whether the grant covers the capability cap on every selector
// value. One item must cover each value: the union of the grant's items is
// what a caller holds.
//
// An absent value (an empty Path) is the root path. An invalid path returns a
// *PathError before any item is read. With no selector value at all, the check
// passes only when 1 item with cap holds the root path for every selector it
// names, or names none.
//
// The local grant passes every check.
func (g Grant) Check(cap string, sels ...Sel) error {
	if len(sels) == 0 {
		if g.all {
			return nil
		}
		for _, item := range g.items {
			if item.Cap == cap && item.atRoot() {
				return nil
			}
		}
		return &DeniedError{Cap: cap}
	}
	values := make([]requestValue, len(sels))
	for i, sel := range sels {
		path, err := cleanRequestPath(sel.Selector, sel.Path)
		if err != nil {
			return err
		}
		values[i] = requestValue{selector: sel.Selector, path: path}
	}
	if g.all {
		return nil
	}
	for i, value := range values {
		covered := false
		for _, item := range g.items {
			if item.covers(cap, value, values) {
				covered = true
				break
			}
		}
		if !covered {
			return &DeniedError{Cap: cap, Selector: sels[i].Selector, Path: value.path}
		}
	}
	return nil
}

// CheckAny reports whether the grant holds the capability cap on any path. A
// generated check calls it before the app's hook runs, for an RPC on an object
// id: the hook then checks the object's stored selector value.
//
// The local grant passes every check.
func (g Grant) CheckAny(cap string) error {
	if g.all {
		return nil
	}
	for _, item := range g.items {
		if item.Cap == cap {
			return nil
		}
	}
	return &DeniedError{Cap: cap}
}

// Allows reports whether the grant covers the capability cap on 1 selector
// value, for filtering a list. An invalid path allows nothing.
func (g Grant) Allows(cap, selector, path string) bool {
	return g.Check(cap, Sel{Selector: selector, Path: path}) == nil
}

// Sel is 1 selector value of a request: the selector's name, and the path the
// request carries for it. An empty Path is the root.
type Sel struct {
	// Selector is the selector's name.
	Selector string
	// Path is the value the request carries.
	Path string
}

// DeniedError is a refused check: the capability the caller needs, and the
// selector and path the grant does not cover when the check was on a selector
// value.
type DeniedError struct {
	// Cap is the capability the caller needs.
	Cap string
	// Selector is the selector of the refused value, or "" for a check that
	// names no selector.
	Selector string
	// Path is the canonical path of the refused value.
	Path string
}

// Error implements error. The text is
// `permission denied: needs <cap> on <selector>=<path>`, or
// `permission denied: needs <cap>` for a check that names no selector.
func (e *DeniedError) Error() string {
	if e.Selector == "" {
		return "permission denied: needs " + e.Cap
	}
	return fmt.Sprintf("permission denied: needs %s on %s=%s", e.Cap, e.Selector, e.Path)
}

// requestValue is 1 canonical selector value of 1 check.
type requestValue struct {
	selector string
	path     string
}

// checkItem validates 1 grant item and returns it with canonical paths.
func checkItem(item Item) (Item, error) {
	if !capPattern.MatchString(item.Cap) {
		return Item{}, fmt.Errorf("the capability %q must match %s", item.Cap, capPattern)
	}
	if len(item.Sel) == 0 {
		return Item{Cap: item.Cap}, nil
	}
	sel := make(map[string]string, len(item.Sel))
	for name, path := range item.Sel {
		if !selectorNamePattern.MatchString(name) {
			return Item{}, fmt.Errorf("the selector name %q must match %s", name, selectorNamePattern)
		}
		cleaned, err := CleanPath(path)
		if err != nil {
			var pathErr *PathError
			if errors.As(err, &pathErr) {
				pathErr.Selector = name
			}
			return Item{}, err
		}
		sel[name] = cleaned
	}
	return Item{Cap: item.Cap, Sel: sel}, nil
}

// atRoot reports whether the item holds the root path for every selector it
// names.
func (it Item) atRoot() bool {
	for _, path := range it.Sel {
		if path != Root {
			return false
		}
	}
	return true
}

// covers reports whether the item covers 1 request value of a check, given
// every request value of that check. The item must hold the capability, its
// path for the value's selector must cover the value, and its path for every
// other selector it names must cover every request value of that selector: the
// root when the request carries none.
func (it Item) covers(cap string, want requestValue, values []requestValue) bool {
	if it.Cap != cap {
		return false
	}
	path, ok := it.Sel[want.selector]
	if !ok {
		path = Root
	}
	if !covers(path, want.path) {
		return false
	}
	for name, itemPath := range it.Sel {
		if name == want.selector {
			continue
		}
		named := false
		for _, value := range values {
			if value.selector != name {
				continue
			}
			named = true
			if !covers(itemPath, value.path) {
				return false
			}
		}
		if !named && !covers(itemPath, Root) {
			return false
		}
	}
	return true
}
