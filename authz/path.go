package authz

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Root is the root path: a grant on it covers every path, and an absent
// selector value means it.
const Root = "/"

// PathError is an invalid path: a grant item's path, or a request's.
//
// A request's path is the *PathError that Check returns, and 1 grant item's
// path is the error that NewGrant or ParseGrant wraps.
type PathError struct {
	// Selector is the selector the path belongs to, or "" when it belongs to
	// no selector.
	Selector string
	// Path is the path as it was given.
	Path string
	// Reason is why the path is invalid.
	Reason string
}

// Error implements error. The text is
// `invalid <selector> path "<path>": <reason>`, or
// `invalid path "<path>": <reason>` when the path belongs to no selector.
func (e *PathError) Error() string {
	if e.Selector == "" {
		return fmt.Sprintf("invalid path %q: %s", e.Path, e.Reason)
	}
	return fmt.Sprintf("invalid %s path %q: %s", e.Selector, e.Path, e.Reason)
}

// CleanPath returns the canonical form of 1 path: its Unicode NFC form, with
// the rules of docs/specs/permissions.md checked.
//
// A path starts with "/", and "/" alone is the root. A path has no empty
// segment, no "." segment, no ".." segment and no trailing "/". The empty
// string is not a path: an absent selector value is the root path, and the
// caller of CleanPath substitutes it.
func CleanPath(path string) (string, error) {
	if path == "" {
		return "", &PathError{Path: path, Reason: "it is empty"}
	}
	cleaned := norm.NFC.String(path)
	if !strings.HasPrefix(cleaned, "/") {
		return "", &PathError{Path: path, Reason: `it must start with "/"`}
	}
	if cleaned == Root {
		return Root, nil
	}
	if strings.HasSuffix(cleaned, "/") {
		return "", &PathError{Path: path, Reason: `it must not end with "/"`}
	}
	for segment := range strings.SplitSeq(cleaned[1:], "/") {
		switch segment {
		case "":
			return "", &PathError{Path: path, Reason: "it has an empty segment"}
		case ".":
			return "", &PathError{Path: path, Reason: `the segment "." is not allowed`}
		case "..":
			return "", &PathError{Path: path, Reason: `the segment ".." is not allowed`}
		}
	}
	return cleaned, nil
}

// cleanRequestPath returns the canonical form of 1 request path, with the
// selector of the request value in the error. An absent path is the root.
func cleanRequestPath(selector, path string) (string, error) {
	value := path
	if value == "" {
		value = Root
	}
	cleaned, err := CleanPath(value)
	if err != nil {
		var pathErr *PathError
		if errors.As(err, &pathErr) {
			return "", &PathError{Selector: selector, Path: path, Reason: pathErr.Reason}
		}
		return "", err
	}
	return cleaned, nil
}

// covers reports whether the grant path grant covers the request path want:
// they are equal, want is below grant, or grant is the root.
func covers(grant, want string) bool {
	return grant == Root || want == grant || strings.HasPrefix(want, grant+"/")
}
