package main

import (
	"slices"
	"strings"
	"testing"
)

// TestParseChanges checks the reading of git diff --name-status -M.
func TestParseChanges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  []change
	}{
		{"no change", "", nil},
		{"an addition", "A\t.memory/corrections/2026-11-15-a.json\n", []change{
			{Status: "A", Paths: []string{".memory/corrections/2026-11-15-a.json"}},
		}},
		{"a deletion", "D\t.memory/corrections/2026-11-15-a.json\n", []change{
			{Status: "D", Paths: []string{".memory/corrections/2026-11-15-a.json"}},
		}},
		{"a rename", "R100\t.memory/corrections/2026-11-15-a.json\t.memory/corrections/2026-11-15-b.json\n", []change{
			{Status: "R100", Paths: []string{".memory/corrections/2026-11-15-a.json", ".memory/corrections/2026-11-15-b.json"}},
		}},
		{"a change and an addition", "M\t.memory/corrections/a.json\nA\t.memory/corrections/b.json\n", []change{
			{Status: "M", Paths: []string{".memory/corrections/a.json"}},
			{Status: "A", Paths: []string{".memory/corrections/b.json"}},
		}},
	} {
		got := parseChanges(tc.input)
		if !slices.EqualFunc(got, tc.want, func(a, b change) bool {
			return a.Status == b.Status && slices.Equal(a.Paths, b.Paths)
		}) {
			t.Errorf("%s: parseChanges = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// recordText is 1 record file, with the named promoted_to.
func recordText(promotedTo string) []byte {
	return []byte(`{
  "date": "2026-11-15",
  "scope": "ci",
  "correction": "c",
  "evidence": "e",
  "promoted_to": "` + promotedTo + `"
}
`)
}

// TestRecordProblems checks the rule that a record file is permanent, except
// for an empty promoted_to that is now filled.
func TestRecordProblems(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before []byte
		after  []byte
		want   string // a substring of the 1 problem; empty for no problem
	}{
		{"no change", recordText(""), recordText(""), ""},
		{"promoted_to filled", recordText(""), recordText("AGENTS.md"), ""},
		{"promoted_to filled twice", recordText("AGENTS.md"), recordText("docs/README.md"), "only an empty promoted_to may be filled"},
		{"promoted_to emptied", recordText("AGENTS.md"), recordText(""), "only an empty promoted_to may be filled"},
		{"the evidence changed", recordText(""), []byte(`{
  "date": "2026-11-15",
  "scope": "ci",
  "correction": "c",
  "evidence": "other",
  "promoted_to": ""
}
`), "evidence changed"},
		{"the correction changed", recordText(""), []byte(`{
  "date": "2026-11-15",
  "scope": "ci",
  "correction": "other",
  "evidence": "e",
  "promoted_to": ""
}
`), "correction changed"},
		{"the date changed", recordText(""), []byte(`{
  "date": "2026-11-16",
  "scope": "ci",
  "correction": "c",
  "evidence": "e",
  "promoted_to": ""
}
`), "date changed"},
		{"the scope changed", recordText(""), []byte(`{
  "date": "2026-11-15",
  "scope": "gate",
  "correction": "c",
  "evidence": "e",
  "promoted_to": ""
}
`), "scope changed"},
		{"a formatter rewrote the file", recordText(""), []byte(`{"date": "2026-11-15", "scope": "ci", "correction": "c", "evidence": "e", "promoted_to": ""}`), ""},
		{"the record cannot be read", recordText(""), []byte("not json\n"), "cannot be read"},
	} {
		problems := recordProblems("a.json", tc.before, tc.after)
		switch {
		case tc.want == "" && len(problems) > 0:
			t.Errorf("%s: recordProblems = %v, want none", tc.name, problems)
		case tc.want != "" && len(problems) != 1:
			t.Errorf("%s: recordProblems = %v, want exactly 1 problem that contains %q", tc.name, problems, tc.want)
		case tc.want != "" && !strings.Contains(problems[0], tc.want):
			t.Errorf("%s: recordProblems = %q, want it to contain %q", tc.name, problems[0], tc.want)
		}
	}
}
