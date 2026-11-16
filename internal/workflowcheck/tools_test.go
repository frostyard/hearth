package workflowcheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// goRunPackages returns the package of each `go run` plugin of 1 buf.gen.yaml's
// text. A plugin that is a list is a command and its arguments; a plugin that is
// 1 binary name alone runs no Go code, and this returns nothing for it.
func goRunPackages(text string) ([]string, error) {
	var cfg bufGenFile
	if err := yaml.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, err
	}
	var out []string
	for _, plugin := range cfg.Plugins {
		command, ok := plugin.Command.([]any)
		if !ok {
			continue
		}
		if len(command) < 2 || command[0] != "go" || command[1] != "run" {
			continue
		}
		pkg := ""
		if len(command) > 2 {
			value, ok := command[2].(string)
			if !ok {
				return nil, fmt.Errorf("the command of a plugin names no package: %v", command)
			}
			pkg = value
		}
		out = append(out, pkg)
	}
	return out, nil
}

// toolProblems returns each problem of the `go run` plugins of 1 buf.gen.yaml's
// text, given the tool paths that its module's go.mod pins. A plugin comes from
// this repository's own commands (./cmd/...), or from a path that a `tool`
// directive pins, so that go.sum carries its checksum (ADR-0010).
func toolProblems(text string, tools []string) ([]string, error) {
	packages, err := goRunPackages(text)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, pkg := range packages {
		if strings.HasPrefix(pkg, "./cmd/") || slices.Contains(tools, pkg) {
			continue
		}
		problems = append(problems, "runs `go run "+pkg+"`, and the go.mod of its module pins no such tool: run a ./cmd/ package, or add a tool directive")
	}
	return problems, nil
}

// goModTools returns each path that 1 go.mod's text pins as a tool: the path of
// a `tool` directive, or the path on each line of a `tool (...)` block.
func goModTools(text string) []string {
	var out []string
	block := false
	for line := range strings.SplitSeq(text, "\n") {
		if cut, _, found := strings.Cut(line, "//"); found {
			line = cut
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch {
		case block && fields[0] == ")":
			block = false
		case block:
			out = append(out, fields[0])
		case fields[0] == "tool" && len(fields) == 1:
			// A block that opens on this line: "tool (".
			block = true
		case fields[0] == "tool" && fields[1] == "(":
			block = true
		case fields[0] == "tool":
			out = append(out, fields[1])
		}
	}
	return out
}

// moduleTools returns the path of the go.mod at or above 1 directory, and the
// tools that it pins.
func moduleTools(t *testing.T, dir string) (string, []string) {
	t.Helper()
	for {
		path := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(path)
		if err == nil {
			return path, goModTools(string(data))
		}
		if !os.IsNotExist(err) {
			t.Fatalf("%s: %v", path, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod at or above %s", dir)
		}
		dir = parent
	}
}

// TestBufGoRunToolsPinned guards the pin of a `go run` plugin: such a plugin is
// a package of this repository's own commands, or a path that the `tool`
// directive of its module's go.mod pins, so go.sum carries its checksum
// (ADR-0010). No plugin is a bare module path of an unpinned package.
func TestBufGoRunToolsPinned(t *testing.T) {
	var templates []string
	err := filepath.WalkDir(filepath.Join("..", ".."), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipped(d.Name(), nil) {
				return fs.SkipDir
			}
			return nil
		}
		if name := d.Name(); strings.HasPrefix(name, "buf.gen") && (strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			templates = append(templates, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) == 0 {
		t.Fatal("no buf.gen.yaml found")
	}
	slices.Sort(templates)
	for _, template := range templates {
		data, err := os.ReadFile(template)
		if err != nil {
			t.Fatalf("%s: %v", template, err)
		}
		absolute, err := filepath.Abs(template)
		if err != nil {
			t.Fatalf("%s: %v", template, err)
		}
		modPath, tools := moduleTools(t, filepath.Dir(absolute))
		problems, err := toolProblems(string(data), tools)
		if err != nil {
			t.Fatalf("%s: %v", template, err)
		}
		for _, problem := range problems {
			t.Errorf("%s %s (%s pins %d tool(s))", template, problem, modPath, len(tools))
		}
		t.Logf("%s: its module %s pins %d tool(s)", template, modPath, len(tools))
	}
}

// TestToolProblems checks the pin rule on in-memory templates and go.mod files.
func TestToolProblems(t *testing.T) {
	const pinned = "connectrpc.com/connect/cmd/protoc-gen-connect-go"
	for _, tc := range []struct {
		name string
		text string
		// tools is the `tool` block of the module's go.mod, or "".
		tools string
		want  string // a substring of the 1 problem; empty for no problem
	}{
		{
			name: "a binary plugin",
			text: "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: gen\n",
		},
		{
			name: "a package of this repository",
			text: "version: v2\nplugins:\n  - local: [\"go\", \"run\", \"./cmd/protoc-gen-hearth-mcp\"]\n    out: gen\n",
		},
		{
			name:  "a pinned tool",
			text:  "version: v2\nplugins:\n  - local:\n      - go\n      - run\n      - " + pinned + "\n    out: gen\n",
			tools: "tool " + pinned + "\n",
		},
		{
			name: "a tool of a tool block",
			text: "version: v2\nplugins:\n  - local: [\"go\", \"run\", \"example.com/app/cmd/x\"]\n    out: gen\n",
			tools: `module example.com/app

go 1.27.4

tool (
	example.com/app/cmd/x
	example.com/app/cmd/y
)
`,
		},
		{
			name: "an unpinned package",
			text: "version: v2\nplugins:\n  - local: [\"go\", \"run\", \"" + pinned + "\"]\n    out: gen\n",
			want: "pins no such tool",
		},
		{
			name:  "a tool of another module",
			text:  "version: v2\nplugins:\n  - local: [\"go\", \"run\", \"example.com/other/cmd/x\"]\n    out: gen\n",
			tools: "tool " + pinned + "\n",
			want:  "pins no such tool",
		},
		{
			name: "a go run that names no package",
			text: "version: v2\nplugins:\n  - local: [\"go\", \"run\"]\n    out: gen\n",
			want: "pins no such tool",
		},
		{
			name: "a command that is no go run",
			text: "version: v2\nplugins:\n  - local: [\"./scripts/gen.sh\"]\n    out: gen\n",
		},
	} {
		problems, err := toolProblems(tc.text, goModTools(tc.tools))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		switch {
		case tc.want == "" && len(problems) > 0:
			t.Errorf("%s: toolProblems = %v, want none", tc.name, problems)
		case tc.want != "" && len(problems) != 1:
			t.Errorf("%s: toolProblems = %v, want exactly 1 problem that contains %q", tc.name, problems, tc.want)
		case tc.want != "" && !strings.Contains(problems[0], tc.want):
			t.Errorf("%s: toolProblems = %q, want it to contain %q", tc.name, problems[0], tc.want)
		}
	}
}

// TestGoModTools checks that a tool directive and a tool block are both read,
// and that nothing else is.
func TestGoModTools(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"no tool", "module example.com/app\n\ngo 1.27.4\n", nil},
		{"1 tool", "module example.com/app\n\ntool example.com/app/cmd/x\n", []string{"example.com/app/cmd/x"}},
		{"a tool block", "module example.com/app\n\ntool (\n\t// a comment\n\texample.com/app/cmd/x\n\texample.com/app/cmd/y\n)\n", []string{"example.com/app/cmd/x", "example.com/app/cmd/y"}},
		{"a require, and no tool", "module example.com/app\n\nrequire example.com/app v1.0.0\n", nil},
	} {
		if got := goModTools(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("%s: goModTools = %q, want %q", tc.name, got, tc.want)
		}
	}
}
