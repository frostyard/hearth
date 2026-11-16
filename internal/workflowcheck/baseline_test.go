package workflowcheck

import (
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// skipped reports whether the repository walk leaves directory name out:
// version control, dependencies, and build output.
func skipped(name string, extra map[string]bool) bool {
	switch name {
	case ".git", "node_modules", "build", "dist", "target":
		return true
	}
	return extra[name]
}

// repoFiles returns every file of the repository as a path from its root with
// forward slashes. The directories skipped, and each of extra, are left out.
func repoFiles(t *testing.T, extra ...string) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	skip := make(map[string]bool, len(extra))
	for _, name := range extra {
		skip[name] = true
	}
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipped(d.Name(), skip) {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestInstructionAliases guards row B1 of frostyard/core
// docs/specs/repository-baseline.md: AGENTS.md is the one instruction file,
// and every other agent entry point is a relative symlink to it.
func TestInstructionAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows checkout has no symlinks")
	}
	agents := filepath.Join("..", "..", "AGENTS.md")
	fi, err := os.Lstat(agents)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("AGENTS.md is not a regular file (mode %s); it is the one instruction file", fi.Mode())
	}
	want, err := filepath.EvalSymlinks(agents)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"CLAUDE.md",
		"GEMINI.md",
		".cursorrules",
		filepath.Join(".github", "copilot-instructions.md"),
	} {
		if got, ok := relativeSymlink(t, name); ok && got != want {
			t.Errorf("%s resolves to %q, want %q", name, got, want)
		}
	}

	name := filepath.Join(".claude", "skills")
	got, ok := relativeSymlink(t, name)
	wantSkills, err := filepath.EvalSymlinks(filepath.Join("..", "..", ".agents", "skills"))
	if err != nil {
		t.Fatalf(".agents/skills: %v", err)
	}
	if ok && got != wantSkills {
		t.Errorf("%s resolves to %q, want %q", name, got, wantSkills)
	}
	if fi, err := os.Stat(filepath.Join("..", "..", name)); err != nil {
		t.Errorf("%s: %v", name, err)
	} else if !fi.IsDir() {
		t.Errorf("%s resolves to a file, want the directory .agents/skills", name)
	}
}

// relativeSymlink reports whether name is a symlink whose target is relative,
// and returns the path it resolves to.
func relativeSymlink(t *testing.T, name string) (string, bool) {
	t.Helper()
	link := filepath.Join("..", "..", name)
	fi, err := os.Lstat(link)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return "", false
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is not a symlink", name)
		return "", false
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return "", false
	}
	if filepath.IsAbs(target) {
		t.Errorf("%s links to the absolute path %q; it must be relative", name, target)
		return "", false
	}
	got, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return "", false
	}
	return got, true
}

// riskPath is 1 entry of .github/risk-paths.json.
type riskPath struct {
	Glob  string `json:"glob"`
	Level string `json:"level"`
	Why   string `json:"why"`
}

// riskPathFile is the whole of .github/risk-paths.json.
type riskPathFile struct {
	SchemaVersion int        `json:"schema_version"`
	DefaultLevel  string     `json:"default_level"`
	Paths         []riskPath `json:"paths"`
}

// schemaVersion is the version of .github/risk-paths.json this repository
// writes (frostyard/core ADR-0078, docs/specs/risk-paths.md).
const schemaVersion = 2

// levels are the levels of frostyard/core docs/specs/risk-paths.md, from the
// strictest to the loosest: a lower number is stricter.
var levels = []string{"L1", "L2", "L3", "L4"}

// strictLevels are the levels that get risk:high and must name a protected
// boundary (rule B).
var strictLevels = levels[:2]

// governedListPaths are the paths of rule C of frostyard/core
// docs/specs/risk-paths.md: the list must name each of them, and each glob
// that matches 1 of them is at L1 or L2.
var governedListPaths = []string{
	".github/risk-paths.json",
	".github/workflows/risk-paths.yml",
}

// boundaryIDs reads the identifiers of the protected boundaries of
// policies/agent-governance.json. It fails when the file holds none: rule B
// needs them.
func boundaryIDs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "policies", "agent-governance.json"))
	if err != nil {
		t.Fatalf("policies/agent-governance.json: %v", err)
	}
	var policy struct {
		ProtectedBoundaries []struct {
			ID string `json:"id"`
		} `json:"protected_boundaries"`
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatalf("policies/agent-governance.json: %v", err)
	}
	ids := make([]string, 0, len(policy.ProtectedBoundaries))
	for _, boundary := range policy.ProtectedBoundaries {
		if boundary.ID != "" {
			ids = append(ids, boundary.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatal("policies/agent-governance.json holds no protected boundary, so no why can name 1")
	}
	return ids
}

// riskPathProblems returns each problem of cfg with frostyard/core
// docs/specs/risk-paths.md, rules A, B and C; no problem means a valid version
// 2 list. A glob that matches no file is allowed: a later task adds those
// paths.
func riskPathProblems(cfg riskPathFile, boundaries []string) []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	if cfg.SchemaVersion != schemaVersion {
		add("schema_version = %d, want %d", cfg.SchemaVersion, schemaVersion)
	}
	if !slices.Contains(levels, cfg.DefaultLevel) {
		add("default_level = %q, want 1 of %q", cfg.DefaultLevel, levels)
	}
	if len(cfg.Paths) == 0 {
		add("the list has no path")
	}
	seen := make(map[string]bool, len(cfg.Paths))
	for i, p := range cfg.Paths {
		for _, problem := range validGlob(p.Glob) {
			add("entry %d: glob %q: %s", i, p.Glob, problem)
		}
		if seen[p.Glob] {
			add("entry %d: glob %q appears twice", i, p.Glob)
		}
		seen[p.Glob] = true
		if !slices.Contains(levels, p.Level) {
			add("entry %d: glob %q has the level %q, want 1 of %q", i, p.Glob, p.Level, levels)
		}
		if strings.TrimSpace(p.Why) == "" {
			add("entry %d: glob %q has no why", i, p.Glob)
		}
		if n := utf8.RuneCountInString(p.Why); n > 200 {
			add("entry %d: glob %q has a why of %d characters, want 200 at most", i, p.Glob, n)
		}
		if slices.Contains(strictLevels, p.Level) && !boundaryWhy(p.Why, boundaries) {
			add("entry %d: glob %q is at %s and has the why %q; it must start with 1 of the protected boundaries %q and a colon", i, p.Glob, p.Level, p.Why, boundaries)
		}
	}
	for _, required := range governedListPaths {
		matched := false
		for _, p := range cfg.Paths {
			if !matchGlob(p.Glob, required) {
				continue
			}
			matched = true
			if !slices.Contains(strictLevels, p.Level) {
				add("glob %q matches %s, so it must be at L1 or L2, not %q", p.Glob, required, p.Level)
			}
		}
		if !matched {
			add("no glob matches %s; the list must name it", required)
		}
	}
	return problems
}

// boundaryWhy reports whether why starts with 1 protected boundary identifier
// and a colon (rule B).
func boundaryWhy(why string, boundaries []string) bool {
	for _, id := range boundaries {
		if rest, ok := strings.CutPrefix(why, id+":"); ok && rest != "" {
			return true
		}
	}
	return false
}

// TestRiskPaths guards row B6 of frostyard/core
// docs/specs/repository-baseline.md: .github/risk-paths.json is a valid
// version 2 list, and each entry at L1 or L2 names a protected boundary of the
// repository's governance policy.
func TestRiskPaths(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "risk-paths.json"))
	if err != nil {
		t.Fatalf(".github/risk-paths.json: %v", err)
	}
	var cfg riskPathFile
	if err := json.Unmarshal(data, &cfg, json.RejectUnknownMembers(true)); err != nil {
		t.Fatalf(".github/risk-paths.json: %v", err)
	}
	if len(cfg.Paths) == 0 {
		t.Fatal(".github/risk-paths.json lists no path")
	}
	boundaries := boundaryIDs(t)
	for _, problem := range riskPathProblems(cfg, boundaries) {
		t.Error(problem)
	}
	t.Logf("%d globs, %d protected boundaries", len(cfg.Paths), len(boundaries))
}

// TestRiskPathProblems checks the validation of frostyard/core
// docs/specs/risk-paths.md (rules B and C, ADR-0078) on in-memory lists.
func TestRiskPathProblems(t *testing.T) {
	boundaries := []string{"workflow-and-permissions", "supply-chain"}
	entry := func(glob, level, why string) riskPath {
		return riskPath{Glob: glob, Level: level, Why: why}
	}
	valid := riskPathFile{
		SchemaVersion: 2,
		DefaultLevel:  "L3",
		Paths: []riskPath{
			entry(".github/risk-paths.json", "L2", "workflow-and-permissions: governance, the list"),
			entry(".github/workflows/**", "L2", "workflow-and-permissions: governance, the CI workflows"),
			entry("src/**", "L3", "ordinary code"),
		},
	}
	for _, tc := range []struct {
		name string
		cfg  riskPathFile
		want string // a substring of the 1 problem the case must give; empty for a valid list
	}{
		{"valid v2 list", valid, ""},
		{"bad level", riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{
			entry(".github/risk-paths.json", "L2", "workflow-and-permissions: the list"),
			entry(".github/workflows/**", "L2", "workflow-and-permissions: the CI workflows"),
			entry("src/**", "L5", "ordinary code"),
		}}, `has the level "L5"`},
		{"no default level", riskPathFile{SchemaVersion: 2, Paths: valid.Paths}, `default_level = ""`},
		{"schema version 1", riskPathFile{SchemaVersion: 1, DefaultLevel: "L3", Paths: valid.Paths}, "schema_version = 1"},
		{"an L3 entry with a free-text why", riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{
			entry(".github/risk-paths.json", "L2", "workflow-and-permissions: the list"),
			entry(".github/workflows/**", "L2", "workflow-and-permissions: the CI workflows"),
			entry("docs/how-to/**", "L3", "how-to documentation"),
		}}, ""},
		{"an L1 entry with a category that is no boundary", riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{
			entry(".github/risk-paths.json", "L2", "workflow-and-permissions: the list"),
			entry(".github/workflows/**", "L2", "workflow-and-permissions: the CI workflows"),
			entry("src/keys.go", "L1", "signing and keys: the keys"),
		}}, "is at L1 and has the why"},
		{"a boundary with no colon", riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{
			entry(".github/risk-paths.json", "L2", "workflow-and-permissions the list"),
			entry(".github/workflows/**", "L2", "workflow-and-permissions: the CI workflows"),
		}}, "is at L2 and has the why"},
		{"rule C: an L3 glob for the list", riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{
			entry(".github/risk-paths.json", "L3", "the list"),
			entry(".github/workflows/**", "L2", "workflow-and-permissions: the CI workflows"),
		}}, "so it must be at L1 or L2"},
		{"rule C: no glob for the caller", riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{
			entry(".github/risk-paths.json", "L2", "workflow-and-permissions: the list"),
			entry("src/**", "L3", "ordinary code"),
		}}, "no glob matches .github/workflows/risk-paths.yml"},
	} {
		problems := riskPathProblems(tc.cfg, boundaries)
		switch {
		case tc.want == "" && len(problems) > 0:
			t.Errorf("%s: riskPathProblems = %v, want none", tc.name, problems)
		case tc.want != "" && len(problems) != 1:
			t.Errorf("%s: riskPathProblems = %v, want exactly 1 problem that contains %q", tc.name, problems, tc.want)
		case tc.want != "" && !strings.Contains(problems[0], tc.want):
			t.Errorf("%s: riskPathProblems = %q, want it to contain %q", tc.name, problems[0], tc.want)
		}
	}
}

// TestBoundaryWhy checks that a why names 1 protected boundary, before a colon.
func TestBoundaryWhy(t *testing.T) {
	boundaries := []string{"supply-chain", "workflow-and-permissions"}
	for why, want := range map[string]bool{
		"supply-chain: the tool pins":                    true,
		"workflow-and-permissions: governance, the list": true,
		"governance: the list":                           false,
		"Supply-chain: the tool pins":                    false,
		"supply-chain the tool pins":                     false,
		"supply-chain:":                                  false,
		" supply-chain: the tool pins":                   false,
	} {
		if got := boundaryWhy(why, boundaries); got != want {
			t.Errorf("boundaryWhy(%q) = %v, want %v", why, got, want)
		}
	}
}

// maxBraceExpansions is the most globs 1 brace group set may give
// (frostyard/core scripts/lib/risk-paths.mjs).
const maxBraceExpansions = 256

// matchGlob reports whether glob matches the whole slash-separated path
// (frostyard/core docs/specs/risk-paths.md): {a,b} is 1 of the alternatives,
// ** as a whole segment is 0 or more whole segments, * and ? never cross a
// slash, and every other character is literal. An empty path matches nothing,
// and a glob that cannot be expanded matches nothing.
func matchGlob(glob, p string) bool {
	if p == "" {
		return false
	}
	expansions, errs := expandBraces(glob)
	if len(errs) > 0 {
		return false
	}
	pathSegments := strings.Split(p, "/")
	for _, expansion := range expansions {
		if matchSegments(strings.Split(expansion, "/"), pathSegments) {
			return true
		}
	}
	return false
}

// expandBraces returns 1 expansion of glob for each combination of its brace
// groups, and the reasons glob cannot be expanded; a glob with a reason expands
// to nothing. Groups do not nest.
func expandBraces(glob string) (expansions []string, errs []string) {
	var parts [][]string
	literal := ""
	for i := 0; i < len(glob); {
		switch glob[i] {
		case '}':
			errs = append(errs, "unmatched '}'")
			i++
		case '{':
			rest := glob[i+1:]
			close, nextOpen := strings.IndexByte(rest, '}'), strings.IndexByte(rest, '{')
			if close < 0 {
				return nil, append(errs, "unmatched '{'")
			}
			if nextOpen >= 0 && nextOpen < close {
				return nil, append(errs, "nested braces are not supported")
			}
			alternatives := strings.Split(rest[:close], ",")
			if len(alternatives) < 2 {
				errs = append(errs, "a brace group needs at least 2 alternatives")
			}
			if slices.Contains(alternatives, "") {
				errs = append(errs, "a brace group has an empty alternative")
			}
			parts = append(parts, []string{literal}, alternatives)
			literal = ""
			i += close + 2
		default:
			literal += glob[i : i+1]
			i++
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	parts = append(parts, []string{literal})

	expansions = []string{""}
	for _, alternatives := range parts {
		next := make([]string, 0, len(expansions)*len(alternatives))
		for _, prefix := range expansions {
			for _, alternative := range alternatives {
				next = append(next, prefix+alternative)
			}
		}
		if len(next) > maxBraceExpansions {
			return nil, []string{fmt.Sprintf("brace expansion gives more than %d globs", maxBraceExpansions)}
		}
		expansions = next
	}
	return expansions, nil
}

// matchSegments reports whether the path segments match the glob segments,
// where a ** segment is 0 or more whole segments.
func matchSegments(globSegments, pathSegments []string) bool {
	patterns := make([]*regexp.Regexp, len(globSegments))
	for i, segment := range globSegments {
		if segment != "**" {
			patterns[i] = segmentPattern(segment)
		}
	}
	memo := make(map[[2]int]bool, len(globSegments)*len(pathSegments)+1)
	var visit func(g, p int) bool
	visit = func(g, p int) bool {
		if match, ok := memo[[2]int{g, p}]; ok {
			return match
		}
		var match bool
		switch {
		case g == len(patterns):
			match = p == len(pathSegments)
		case patterns[g] == nil:
			match = visit(g+1, p) || (p < len(pathSegments) && visit(g, p+1))
		default:
			match = p < len(pathSegments) && patterns[g].MatchString(pathSegments[p]) && visit(g+1, p+1)
		}
		memo[[2]int{g, p}] = match
		return match
	}
	return visit(0, 0)
}

// segmentPattern compiles 1 glob segment: * is any run of characters within the
// segment, ? is 1 such character, and every other character is literal.
func segmentPattern(segment string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, c := range segment {
		switch c {
		case '*':
			b.WriteString(`[^/]*`)
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// validGlob returns the reasons glob is not a path glob of frostyard/core
// docs/specs/risk-paths.md; no reason means a valid glob.
func validGlob(glob string) []string {
	if glob == "" {
		return []string{"glob is empty"}
	}
	var errs []string
	for _, c := range []string{"[", "]", "!", `\`} {
		if strings.Contains(glob, c) {
			errs = append(errs, fmt.Sprintf("%q is not supported", c))
		}
	}
	expansions, braceErrs := expandBraces(glob)
	errs = append(errs, braceErrs...)
	for _, expansion := range expansions {
		if strings.HasPrefix(expansion, "/") {
			errs = append(errs, "a glob must not start with '/'")
		}
		if strings.HasPrefix(expansion, "./") {
			errs = append(errs, "a glob must not start with './'")
		}
		for _, segment := range strings.Split(expansion, "/") {
			switch {
			case segment == "":
				errs = append(errs, "a glob must not have an empty segment")
			case segment == "..":
				errs = append(errs, "a glob must not have a '..' segment")
			case strings.Contains(segment, "**") && segment != "**":
				errs = append(errs, "'**' must be a whole segment")
			}
		}
	}
	return unique(errs)
}

// unique returns list without a repeated entry, in the order of 1st appearance.
func unique(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// TestValidGlob checks the glob validation of frostyard/core
// docs/specs/risk-paths.md on the cases a repository's list can hold.
func TestValidGlob(t *testing.T) {
	for _, glob := range []string{"a/**", "{a,b}/c", "**", "Makefile", "mise.toml"} {
		if problems := validGlob(glob); len(problems) > 0 {
			t.Errorf("validGlob(%q) = %v, want no problem", glob, problems)
		}
	}
	for _, glob := range []string{
		"internal/model/{model.go,}",
		"{a}",
		"{a,{b,c}}",
		"a}",
		"{a,b",
		"[ab]",
		"!a",
		`a\b`,
		"a**",
		"a/**b",
		"/a",
		"./a",
		"a//b",
		"a/../b",
		strings.Repeat("{a,b}", 9),
	} {
		if problems := validGlob(glob); len(problems) == 0 {
			t.Errorf("validGlob(%q) = no problem, want at least 1", glob)
		}
	}
}

// TestMatchGlob checks the glob rules of frostyard/core
// docs/specs/risk-paths.md on its own cases and the ones this repository's
// list holds.
func TestMatchGlob(t *testing.T) {
	for _, tc := range []struct {
		glob string
		path string
		want bool
	}{
		{"**/x", "x", true},
		{"**/x", "a/x", true},
		{"**/x", "a/b/x", true},
		{"**/x", "a/x/y", false},
		{"**", "x", true},
		{"**", "a/b", true},
		{"**", ".github/x.yml", true},
		{"x*", "x", true},
		{"x*", "a/x", false},
		{"docs/*.md", "docs/a.md", true},
		{"docs/*.md", "docs/a/b.md", false},
		{".github/{risk-paths.json,settings.yml}", ".github/risk-paths.json", true},
		{".github/{risk-paths.json,settings.yml}", ".github/workflows/ci.yml", false},
		{".github/workflows/**", ".github/workflows/release.yml", true},
		{".github/workflows/**", ".github/risk-paths.json", false},
		{"internal/model/**", "internal/model/model.go", true},
		{"internal/model/**", "internal/manifest/model.go", false},
		{"{mise.toml,mise.lock}", "mise.lock", true},
		{"{mise.toml,mise.lock}", "mise.tom", false},
		{"Makefile", "Makefile", true},
		{"Makefile", "app/Makefile", false},
		{"**", "", false},
	} {
		if got := matchGlob(tc.glob, tc.path); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.glob, tc.path, got, tc.want)
		}
	}
}

// dependabotUpdate is 1 entry of the updates list of
// .github/dependabot.yml.
type dependabotUpdate struct {
	Ecosystem string `yaml:"package-ecosystem"`
	Directory string `yaml:"directory"`
	Groups    map[string]struct {
		Patterns  []string `yaml:"patterns"`
		AppliesTo string   `yaml:"applies-to"`
	} `yaml:"groups"`
}

// dependabotFile is the whole of .github/dependabot.yml.
type dependabotFile struct {
	Version int                `yaml:"version"`
	Updates []dependabotUpdate `yaml:"updates"`
}

// ecosystem names the Dependabot package-ecosystem of the manifest file
// base, if base is a manifest.
func ecosystem(base string) (string, bool) {
	switch base {
	case "go.mod":
		return "gomod", true
	case "package.json":
		return "npm", true
	case "Cargo.toml":
		return "cargo", true
	case "Dockerfile", "Containerfile":
		return "docker", true
	}
	return "", false
}

// normalizeDir drops the trailing slash of a Dependabot directory, and keeps
// the root as "/".
func normalizeDir(dir string) string {
	if dir == "/" {
		return dir
	}
	return strings.TrimSuffix(dir, "/")
}

// TestDependabotCoversManifests guards row B13 of frostyard/core
// docs/specs/repository-baseline.md: .github/dependabot.yml has 1 entry for
// each manifest of the repository, the github-actions entry at "/", and each
// entry has exactly 1 group whose patterns are ["*"], with no applies-to, so
// that security updates stay 1 pull request each.
func TestDependabotCoversManifests(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatalf(".github/dependabot.yml: %v", err)
	}
	var cfg dependabotFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf(".github/dependabot.yml: %v", err)
	}
	if cfg.Version != 2 {
		t.Errorf("version = %d, want 2", cfg.Version)
	}
	covered := make(map[[2]string]bool, len(cfg.Updates))
	actions := false
	for _, u := range cfg.Updates {
		dir := normalizeDir(u.Directory)
		covered[[2]string{u.Ecosystem, dir}] = true
		if u.Ecosystem == "github-actions" && dir == "/" {
			actions = true
		}
		if len(u.Groups) != 1 {
			t.Errorf("%s at %s has %d groups, want exactly 1", u.Ecosystem, dir, len(u.Groups))
			continue
		}
		for name, group := range u.Groups {
			if !slices.Equal(group.Patterns, []string{"*"}) {
				t.Errorf("%s at %s: group %q has the patterns %q, want [\"*\"]", u.Ecosystem, dir, name, group.Patterns)
			}
			if group.AppliesTo != "" {
				t.Errorf("%s at %s: group %q holds applies-to %q, and security updates must stay 1 pull request each", u.Ecosystem, dir, name, group.AppliesTo)
			}
		}
	}
	if !actions {
		t.Error("no github-actions entry with directory /")
	}

	manifests := 0
	for _, f := range repoFiles(t, "docs") {
		eco, ok := ecosystem(path.Base(f))
		if !ok {
			continue
		}
		manifests++
		dir := path.Dir(f)
		want := "/"
		if dir != "." {
			want = "/" + dir
		}
		if !covered[[2]string{eco, want}] {
			t.Errorf("%s has no Dependabot entry (package-ecosystem %s, directory %s)", f, eco, want)
		}
	}
	if manifests == 0 {
		t.Fatal("no dependency manifest found")
	}
	t.Logf("%d manifests, %d Dependabot entries", manifests, len(cfg.Updates))
}
