package model

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

var (
	// appPattern is the pattern of an app's id.
	appPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)
	// versionPattern is the pattern of the version parameter.
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	// objectPattern is the pattern of an event's object and verb.
	objectPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// selectorPattern is the pattern of a selector's name.
	selectorPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Params are the parameters of a hearth plugin.
type Params struct {
	// App is the app's id.
	App string
	// Version is the app's semantic version.
	Version string
}

// ParseParams reads the plugin's parameters: "app=<id>,version=<major.minor.patch>".
// It refuses a missing or malformed app or version, and any other parameter.
// It reports every problem it finds, 1 line each, sorted.
func ParseParams(parameters string) (Params, error) {
	var (
		params   Params
		problems []string
	)
	for param := range strings.SplitSeq(parameters, ",") {
		param = strings.TrimSpace(param)
		if param == "" {
			continue
		}
		name, value, ok := strings.Cut(param, "=")
		if !ok {
			problems = append(problems, fmt.Sprintf("parameter %q is not <name>=<value>", param))
			continue
		}
		switch name {
		case "app":
			params.App = value
		case "version":
			params.Version = value
		default:
			problems = append(problems, fmt.Sprintf("unknown parameter %q", name))
		}
	}
	switch {
	case params.App == "":
		problems = append(problems, "the app parameter is required: app=<id>")
	case !appPattern.MatchString(params.App):
		problems = append(problems, fmt.Sprintf("the app parameter %q must match %s", params.App, appPattern))
	}
	switch {
	case params.Version == "":
		problems = append(problems, "the version parameter is required: version=<major.minor.patch>")
	case !versionPattern.MatchString(params.Version):
		problems = append(problems, fmt.Sprintf("the version parameter %q must match %s", params.Version, versionPattern))
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return Params{}, errors.New(strings.Join(problems, "\n"))
	}
	return params, nil
}

// snakeCase returns the MCP tool name of 1 RPC name: the name in snake_case,
// where an uppercase letter starts a new word, and a run of uppercase letters
// is 1 word ("GetURL" becomes "get_url", "CreateBookmark" becomes
// "create_bookmark").
func snakeCase(name string) string {
	var b strings.Builder
	b.Grow(len(name) + 4)
	runes := []rune(name)
	for i, r := range runes {
		if !unicode.IsUpper(r) {
			b.WriteRune(r)
			continue
		}
		if i > 0 && startsWord(runes, i) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// startsWord reports whether the uppercase letter at runes[i] starts a word of
// a CamelCase name: it follows a lowercase letter or a digit, or it is the
// last uppercase letter of a run that a lowercase letter follows.
func startsWord(runes []rune, i int) bool {
	previous := runes[i-1]
	if unicode.IsLower(previous) || unicode.IsDigit(previous) {
		return true
	}
	return unicode.IsUpper(previous) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
}
