package workflowcheck

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestExampleHooksAreRiskPaths guards .github/risk-paths.json against a new
// permission hook of the example app: 1 hook decides who may read or change 1
// stored object, outside developers copy the example, so the file that declares
// it must be a risk path at L1 or L2 (ADR-0068, ADR-0009).
func TestExampleHooksAreRiskPaths(t *testing.T) {
	cfg := readRiskPaths(t)
	files := exampleGoFiles(t)
	problems := hookRiskProblems(cfg, files)
	for _, problem := range problems {
		t.Error(problem)
	}
	hooks := 0
	for _, src := range files {
		names, err := authorizeHooks(src)
		if err != nil {
			t.Fatal(err)
		}
		hooks += len(names)
	}
	if hooks == 0 {
		t.Fatal("the example app declares no permission hook, so this test guards nothing")
	}
	t.Logf("%d Go file(s) of the example, %d permission hook(s)", len(files), hooks)
}

// TestHookRiskProblems checks the hook rule on in-memory files and lists.
func TestHookRiskProblems(t *testing.T) {
	hook := []byte("package notes\n\n// AuthorizeGetNote checks 1 note.\nfunc (s *Service) AuthorizeGetNote() {}\n")
	plain := []byte("package notes\n\n// GetNote reads 1 note.\nfunc (s *Service) GetNote() {}\n")
	files := map[string][]byte{
		"examples/notes-lite/notes.go": hook,
		"examples/notes-lite/other.go": plain,
	}
	entry := func(glob, level string) riskPath {
		return riskPath{Glob: glob, Level: level, Why: "permission-generation-and-checks: auth, the example app's hooks"}
	}
	for _, tc := range []struct {
		name string
		cfg  riskPathFile
		want int
	}{
		{
			name: "an entry at L2 for the file",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{entry("examples/notes-lite/notes.go", "L2")}},
		},
		{
			name: "an entry at L1 for a directory",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{entry("examples/**", "L1")}},
		},
		{
			name: "no entry for the file",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{entry("examples/notes-lite/other.go", "L2")}},
			want: 1,
		},
		{
			name: "an entry at L3 only",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{entry("examples/**", "L3")}},
			want: 1,
		},
		{
			name: "no entry at all",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3"},
			want: 1,
		},
	} {
		problems := hookRiskProblems(tc.cfg, files)
		if len(problems) != tc.want {
			t.Errorf("%s: hookRiskProblems = %v, want %d problem(s)", tc.name, problems, tc.want)
			continue
		}
		if tc.want == 1 && !strings.Contains(problems[0], "AuthorizeGetNote") {
			t.Errorf("%s: the problem = %q, want it to name the hook", tc.name, problems[0])
		}
	}
}

// hookRiskProblems returns 1 problem for each Go file that declares a method
// whose name starts with "Authorize", and that no risk-path entry at L1 or L2
// matches.
func hookRiskProblems(cfg riskPathFile, files map[string][]byte) []string {
	var problems []string
	for _, path := range slices.Sorted(maps.Keys(files)) {
		names, err := authorizeHooks(files[path])
		if err != nil {
			problems = append(problems, path+": "+err.Error())
			continue
		}
		if len(names) == 0 || coveredAtL1OrL2(cfg, path) {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s declares the permission hook %s, and no entry at L1 or L2 of .github/risk-paths.json matches it: add an entry for %s, or for the directory that holds it",
			path, strings.Join(names, ", "), path))
	}
	return problems
}

// coveredAtL1OrL2 reports whether 1 risk-path entry at L1 or L2 matches path.
func coveredAtL1OrL2(cfg riskPathFile, path string) bool {
	for _, entry := range cfg.Paths {
		if entry.Level != "L1" && entry.Level != "L2" {
			continue
		}
		if matchGlob(entry.Glob, path) {
			return true
		}
	}
	return false
}

// authorizeHooks returns each method name of 1 Go file's source that starts with
// "Authorize": the permission hook of 1 RPC on an object id.
func authorizeHooks(src []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "1.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || !strings.HasPrefix(fn.Name.Name, "Authorize") {
			continue
		}
		out = append(out, fn.Name.Name)
	}
	return out, nil
}

// exampleGoFiles returns each hand-written Go file of the example app, as its
// source, by its path from the repository root. A generated file (under gen/)
// and a test file are left out.
func exampleGoFiles(t *testing.T) map[string][]byte {
	t.Helper()
	root := filepath.Join("..", "..")
	files := map[string][]byte{}
	err := filepath.WalkDir(filepath.Join(root, "examples"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "gen" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// readRiskPaths reads .github/risk-paths.json.
func readRiskPaths(t *testing.T) riskPathFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "risk-paths.json"))
	if err != nil {
		t.Fatalf(".github/risk-paths.json: %v", err)
	}
	var cfg riskPathFile
	if err := json.Unmarshal(data, &cfg, json.RejectUnknownMembers(true)); err != nil {
		t.Fatalf(".github/risk-paths.json: %v", err)
	}
	return cfg
}

// admissionNames are the calls that admit 1 socket's caller: lodge.Listener
// admits 1 user id, and connectserver.Private and connectserver.Lodge apply the
// caller of the private and of the lodge socket. The file that calls them is an
// app's socket admission.
var admissionNames = []string{"lodge.Listener", "connectserver.Private", "connectserver.Lodge"}

// TestSocketAdmissionIsRiskPaths guards .github/risk-paths.json against a new
// socket admission: 1 file admits 1 user id on 1 socket and applies the private
// or the lodge handler, so that refusal is the app's own check, and outside
// developers copy the example's. Such a file must be a risk path at L1 or L2
// (ADR-0068, ADR-0010).
func TestSocketAdmissionIsRiskPaths(t *testing.T) {
	cfg := readRiskPaths(t)
	files := admissionGoFiles(t)
	for _, problem := range admissionRiskProblems(cfg, files) {
		t.Error(problem)
	}
	admissions := 0
	for _, src := range files {
		calls, err := socketAdmissions(src)
		if err != nil {
			t.Fatal(err)
		}
		admissions += len(calls)
	}
	if admissions == 0 {
		t.Fatal("no hand-written Go file of the tree admits a socket's caller, so this test guards nothing")
	}
	t.Logf("%d Go file(s) of the tree checked, %d socket admission(s)", len(files), admissions)
}

// TestAdmissionRiskProblems checks the admission rule on in-memory files.
func TestAdmissionRiskProblems(t *testing.T) {
	admission := []byte("package main\n\nvar listener = lodge.Listener(nil, 0)\n")
	plain := []byte("package main\n\nfunc main() {}\n")
	files := map[string][]byte{
		"examples/notes-lite/cmd/notelite/main.go": admission,
		"examples/notes-lite/other.go":             plain,
	}
	entry := func(glob, level string) riskPath {
		return riskPath{Glob: glob, Level: level, Why: "permission-generation-and-checks: auth, the example's socket admission"}
	}
	for _, tc := range []struct {
		name string
		cfg  riskPathFile
		want int
	}{
		{
			name: "an entry at L1 for the file",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{entry("examples/notes-lite/cmd/notelite/main.go", "L1")}},
		},
		{
			name: "an entry at L2 for the directory",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{entry("examples/notes-lite/cmd/notelite/**", "L2")}},
		},
		{
			name: "no entry for the file",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{entry("examples/notes-lite/notes.go", "L1")}},
			want: 1,
		},
		{
			name: "an entry at L3 only",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3", Paths: []riskPath{entry("examples/**", "L3")}},
			want: 1,
		},
		{
			name: "no entry at all",
			cfg:  riskPathFile{SchemaVersion: 2, DefaultLevel: "L3"},
			want: 1,
		},
	} {
		problems := admissionRiskProblems(tc.cfg, files)
		if len(problems) != tc.want {
			t.Errorf("%s: admissionRiskProblems = %v, want %d problem(s)", tc.name, problems, tc.want)
			continue
		}
		if tc.want == 1 && !strings.Contains(problems[0], "lodge.Listener") {
			t.Errorf("%s: the problem = %q, want it to name the call", tc.name, problems[0])
		}
	}
}

// admissionRiskProblems returns 1 problem for each Go file that admits 1
// socket's caller, and that no risk-path entry at L1 or L2 matches.
func admissionRiskProblems(cfg riskPathFile, files map[string][]byte) []string {
	var problems []string
	for _, path := range slices.Sorted(maps.Keys(files)) {
		calls, err := socketAdmissions(files[path])
		if err != nil {
			problems = append(problems, path+": "+err.Error())
			continue
		}
		if len(calls) == 0 || coveredAtL1OrL2(cfg, path) {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s admits a socket's caller (%s), and no entry at L1 or L2 of .github/risk-paths.json matches it: add an entry for %s, or for the directory that holds it",
			path, strings.Join(calls, ", "), path))
	}
	return problems
}

// socketAdmissions returns each call of 1 Go file's source that admits 1
// socket's caller, as "<package>.<function>".
func socketAdmissions(src []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "1.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if name := pkg.Name + "." + selector.Sel.Name; slices.Contains(admissionNames, name) {
			out = append(out, name)
		}
		return true
	})
	return out, nil
}

// admissionGoFiles returns each hand-written, non-test Go file of the tree but
// the 2 packages that write socket admission itself, by its path from the
// repository root.
func admissionGoFiles(t *testing.T) map[string][]byte {
	t.Helper()
	root := filepath.Join("..", "..")
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "connectserver", "lodge", "gen", "third_party":
				return fs.SkipDir
			}
			if skipped(d.Name(), nil) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if generatedSource(data) {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// generatedSource reports whether 1 Go file's source is generated code: its
// first line carries the 2 markers of the convention.
func generatedSource(src []byte) bool {
	first, _, _ := bytes.Cut(src, []byte("\n"))
	return bytes.Contains(first, []byte("Code generated")) && bytes.Contains(first, []byte("DO NOT EDIT."))
}
