package manifest

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// app is a manifest of 1 app with 1 operation, 1 tool and 1 event.
func app() *Manifest {
	return &Manifest{
		Hearth:       FormatMajor,
		App:          "marks",
		Version:      "1.2.0",
		Capabilities: []string{"marks:read"},
		Selectors:    []string{"folder"},
		Operations: []Operation{{
			Name:       "frostyard.frostmarks.v1.Marks.ListBookmarks",
			Permission: "marks:read",
			Effect:     "read",
			Expose:     []string{"ui", "agent"},
			Selectors:  []Selector{{Field: "folder.name", Selector: "folder"}},
			Tool:       "list_bookmarks",
		}},
		Events: []Event{{
			Type:           "org.frostyard.marks.bookmark.created",
			Message:        "frostyard.frostmarks.v1.BookmarkCreated",
			Topic:          "marks.bookmark.created.folder",
			Selector:       "folder",
			ReadPermission: "marks:read",
		}},
	}
}

// TestDiffNoChange checks that 2 equal manifests have no change at all.
func TestDiffNoChange(t *testing.T) {
	result := Diff(app(), app())
	if len(result.Breaking) != 0 || len(result.Additions) != 0 {
		t.Errorf("Diff of equal manifests = %+v, want no change", result)
	}
}

// TestDiffBreaking checks each breaking change of the format on an in-memory
// manifest.
func TestDiffBreaking(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Manifest)
		want   string
	}{
		{"removed operation", func(m *Manifest) { m.Operations = nil }, "operation frostyard.frostmarks.v1.Marks.ListBookmarks removed"},
		{"removed tool", func(m *Manifest) { m.Operations[0].Expose = []string{"ui"}; m.Operations[0].Tool = "" }, "tool list_bookmarks removed"},
		{"removed event", func(m *Manifest) { m.Events = nil }, "event org.frostyard.marks.bookmark.created removed"},
		{"changed permission", func(m *Manifest) { m.Operations[0].Permission = "marks:write" }, "permission changed"},
		{"changed effect", func(m *Manifest) { m.Operations[0].Effect = "outward" }, "effect changed"},
		{"changed selector", func(m *Manifest) {
			m.Operations[0].Selectors = []Selector{{Field: "folder.name", Selector: "group"}}
		}, "selectors changed"},
		{"moved selector field", func(m *Manifest) {
			m.Operations[0].Selectors = []Selector{{Field: "group.name", Selector: "folder"}}
		}, "selectors changed from [folder.name=folder] to [group.name=folder]"},
		{"changed read permission", func(m *Manifest) { m.Events[0].ReadPermission = "marks:write" }, "read_permission changed"},
		{"changed event selector", func(m *Manifest) {
			m.Events[0].Selector = "group"
			m.Events[0].Topic = "marks.bookmark.created.group"
		}, "selector changed"},
		{"changed topic", func(m *Manifest) { m.Events[0].Topic = "marks.bookmark.created.elsewhere" }, "topic changed"},
		{"changed app", func(m *Manifest) { m.App = "notes" }, `app changed from "marks" to "notes"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := app()
			tc.change(next)
			result := Diff(app(), next)
			if len(result.Breaking) == 0 {
				t.Fatalf("Diff found no breaking change, want 1 with %q", tc.want)
			}
			for _, line := range result.Breaking {
				if strings.Contains(line, tc.want) {
					return
				}
			}
			t.Errorf("the breaking changes %q hold no line with %q", result.Breaking, tc.want)
		})
	}
}

// TestDiffAddition checks that an addition is not breaking.
func TestDiffAddition(t *testing.T) {
	next := app()
	next.Operations = append(next.Operations, Operation{
		Name:       "frostyard.frostmarks.v1.Marks.CreateBookmark",
		Permission: "marks:write",
		Effect:     "write",
		Expose:     []string{"ui", "agent"},
		Tool:       "create_bookmark",
	})
	result := Diff(app(), next)
	if len(result.Breaking) != 0 {
		t.Errorf("Diff of an addition = %q, want no breaking change", result.Breaking)
	}
	if !slices.Contains(result.Additions, "tool create_bookmark added") {
		t.Errorf("Diff of an addition = %q, want the added tool", result.Additions)
	}
}

// TestDiffChangedApp checks that a different app keeps nothing of the old
// contract.
func TestDiffChangedApp(t *testing.T) {
	next := app()
	next.App = "notes"
	result := Diff(app(), next)
	for _, want := range []string{
		`app changed from "marks" to "notes"`,
		"operation frostyard.frostmarks.v1.Marks.ListBookmarks removed",
		"tool list_bookmarks removed",
		"event org.frostyard.marks.bookmark.created removed",
	} {
		if !slices.Contains(result.Breaking, want) {
			t.Errorf("the breaking changes %q hold no %q", result.Breaking, want)
		}
	}
}

// TestNextMajor checks the version rule that allows a breaking change: the next
// major version, or, while the app is a v0 app, the next minor version.
func TestNextMajor(t *testing.T) {
	for _, tc := range []struct {
		old, next string
		want      bool
	}{
		{"1.2.0", "2.0.0", true},
		{"1.2.0", "1.2.1", false},
		{"1.2.0", "1.3.0", false},
		{"1.2.0", "1.2.0", false},
		{"0.3.1", "0.4.0", true},
		{"0.3.1", "0.3.2", false},
		{"0.3.1", "1.0.0", true},
		{"0.3.1", "0.3.1", false},
	} {
		got, err := NextMajor(tc.old, tc.next)
		if err != nil {
			t.Fatalf("NextMajor(%q, %q): %v", tc.old, tc.next, err)
		}
		if got != tc.want {
			t.Errorf("NextMajor(%q, %q) = %v, want %v", tc.old, tc.next, got, tc.want)
		}
	}
	for _, invalid := range []string{"1.2", "1.2.x", "01.2.3", "v1.2.3", ""} {
		if _, err := NextMajor(invalid, "2.0.0"); err == nil {
			t.Errorf("NextMajor(%q, 2.0.0) = no error, want 1", invalid)
		}
	}
}

// TestMarshalCanonical checks the canonical JSON form: 2-space indent, 1 final
// newline, the members in the format's order, and the arrays in their stated
// order, whatever order the manifest's parts are in.
func TestMarshalCanonical(t *testing.T) {
	m := app()
	m.Operations = append(m.Operations, Operation{
		Name:       "frostyard.frostmarks.v1.Marks.Aaa",
		Permission: "marks:read",
		Effect:     "read",
		Expose:     []string{"agent", "ui"},
		Selectors:  []Selector{{Field: "b.name", Selector: "folder"}, {Field: "a.name", Selector: "folder"}},
		Tool:       "aaa",
	})
	m.Capabilities = []string{"marks:write", "marks:read"}
	m.Selectors = []string{"folder", "folder"}
	m.Events[0].Message = "frostyard.frostmarks.v1.BookmarkCreated"

	data, err := Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	text := string(data)
	if !strings.HasSuffix(text, "}\n") {
		t.Errorf("the manifest does not end with 1 newline:\n%s", text)
	}
	if !strings.HasPrefix(text, "{\n  \"hearth\": 1,\n  \"app\": \"marks\",\n") {
		t.Errorf("the manifest's first members are not hearth, app:\n%s", text)
	}
	for _, want := range []string{
		`"capabilities": [
    "marks:read",
    "marks:write"
  ]`,
		`"name": "frostyard.frostmarks.v1.Marks.Aaa"`,
		`"expose": [
        "ui",
        "agent"
      ]`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the manifest holds no %q:\n%s", want, text)
		}
	}
	// The operations are sorted by name, and their selectors by field.
	if strings.Index(text, "Marks.Aaa") > strings.Index(text, "Marks.ListBookmarks") {
		t.Errorf("the operations are not sorted by name:\n%s", text)
	}
	if strings.Index(text, `"field": "b.name"`) < strings.Index(text, `"field": "a.name"`) {
		t.Errorf("the selectors are not sorted by field:\n%s", text)
	}

	// Marshal twice writes the same bytes.
	again, err := Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(again) != text {
		t.Error("Marshal is not deterministic")
	}
}

// TestParse checks the reading of a manifest, and its refusals.
func TestParse(t *testing.T) {
	data, err := Marshal(app())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := Parse(data); err != nil {
		t.Errorf("Parse of a valid manifest: %v", err)
	}

	for _, tc := range []struct {
		name string
		data string
		want error
		text string
	}{
		{"unknown major", `{"hearth": 2}`, ErrFormat, "major 2"},
		{"no major", `{}`, ErrFormat, "major 0"},
		{"not an object", `[1]`, ErrInvalid, "JSON object"},
		{"missing member", `{"hearth": 1, "app": "marks"}`, ErrInvalid, "schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.data))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Parse(%s) = %v, want %v", tc.data, err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.text) {
				t.Errorf("Parse(%s) = %q, want it to contain %q", tc.data, err, tc.text)
			}
		})
	}
}
