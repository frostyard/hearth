package schema

import (
	"strings"
	"testing"
)

// minimal is a manifest that matches the schema.
const minimal = `{
  "hearth": 1,
  "app": "min",
  "version": "1.0.0",
  "capabilities": ["min:read"],
  "selectors": [],
  "operations": [
    {
      "name": "frostyard.min.v1.ThingService.GetThing",
      "permission": "min:read",
      "effect": "read",
      "expose": ["ui", "agent"],
      "selectors": [],
      "tool": "get_thing"
    }
  ],
  "events": []
}`

// TestValidate checks the schema on a valid manifest and on the refusals that
// the format's rules name.
func TestValidate(t *testing.T) {
	if err := Validate([]byte(minimal)); err != nil {
		t.Fatalf("Validate of a valid manifest: %v", err)
	}
	for _, tc := range []struct {
		name string
		text string
		want string // a substring of the 1-line error
	}{
		{"another format major", strings.Replace(minimal, `"hearth": 1`, `"hearth": 2`, 1), "/hearth"},
		{"a missing member", strings.Replace(minimal, `  "selectors": [],
`, "", 1), "missing property 'selectors'"},
		{"an unknown member", strings.Replace(minimal, `"hearth": 1,`, `"hearth": 1, "extra": true,`, 1), "'extra' not allowed"},
		{"no tool with an agent exposure", strings.Replace(minimal, `,
      "tool": "get_thing"`, "", 1), "missing property 'tool'"},
		{"a tool with no agent exposure", strings.Replace(minimal, `"expose": ["ui", "agent"]`, `"expose": ["ui"]`, 1), "at /operations/0"},
		{"an unknown effect", strings.Replace(minimal, `"effect": "read"`, `"effect": "peek"`, 1), "at /operations/0/effect"},
		{"a bad capability", strings.Replace(minimal, `"min:read"`, `"min:Read"`, 1), "does not match pattern"},
		{"not JSON", "{", "read the manifest"},
	} {
		err := Validate([]byte(tc.text))
		if err == nil {
			t.Errorf("%s: Validate = no error, want 1 that names %q", tc.name, tc.want)
			continue
		}
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: the error is not 1 line: %q", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Validate = %q, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

// TestBytes checks that the schema's bytes are the file's, and that a copy
// cannot change them.
func TestBytes(t *testing.T) {
	first := Bytes()
	if !strings.Contains(string(first), ID) {
		t.Errorf("the schema does not hold its own $id %q", ID)
	}
	first[0] = 'x'
	if got := string(Bytes()); got[0] != '{' {
		t.Error("Bytes returned the schema's own bytes, so a caller can change them")
	}
}
