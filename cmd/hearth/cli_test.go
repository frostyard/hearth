package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fixtureDir holds the diff cases: 1 folder for each, with old.json, new.json
// and want.txt.
const fixtureDir = "../../manifest/testdata/diff"

// want is 1 case's expected result: the exit code, and the lines that the
// command's output must hold.
type want struct {
	code  int
	lines []string
}

// readWant reads 1 case's want.txt: the first line is "exit N", and each other
// non-empty line must appear in the command's output.
func readWant(t *testing.T, dir string) want {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "want.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var result want
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if i == 0 {
			code, ok := strings.CutPrefix(line, "exit ")
			if !ok {
				t.Fatalf("%s/want.txt: the first line %q is not \"exit N\"", dir, line)
			}
			n, err := strconv.Atoi(code)
			if err != nil {
				t.Fatalf("%s/want.txt: %v", dir, err)
			}
			result.code = n
			continue
		}
		if line != "" {
			result.lines = append(result.lines, line)
		}
	}
	return result
}

// run runs the CLI and returns its exit code and combined output.
func run(args ...string) (int, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

// TestDiffFixtures checks `hearth manifest diff` against every case: its exit
// code, and the lines it prints.
func TestDiffFixtures(t *testing.T) {
	cases, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no diff fixture")
	}
	for _, c := range cases {
		if !c.IsDir() {
			continue
		}
		t.Run(c.Name(), func(t *testing.T) {
			dir := filepath.Join(fixtureDir, c.Name())
			expected := readWant(t, dir)
			code, output := run("manifest", "diff", filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json"))
			if code != expected.code {
				t.Errorf("exit code = %d, want %d\n%s", code, expected.code, output)
			}
			for _, line := range expected.lines {
				if !strings.Contains(output, line) {
					t.Errorf("the output has no line with %q:\n%s", line, output)
				}
			}
		})
	}
}

// TestDiffVerbose checks that --verbose prints the additions of a compatible
// change, and that a plain run prints none.
func TestDiffVerbose(t *testing.T) {
	dir := filepath.Join(fixtureDir, "compatible-addition")
	oldPath, nextPath := filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json")
	if _, output := run("manifest", "diff", oldPath, nextPath); strings.Contains(output, "added") {
		t.Errorf("a plain run prints an addition:\n%s", output)
	}
	code, output := run("manifest", "diff", "--verbose", oldPath, nextPath)
	if code != exitOK {
		t.Errorf("exit code = %d, want %d\n%s", code, exitOK, output)
	}
	if !strings.Contains(output, "tool create_bookmark added") {
		t.Errorf("--verbose printed no added tool:\n%s", output)
	}
}

// TestValidate checks `hearth manifest validate` on a valid and an invalid
// manifest.
func TestValidate(t *testing.T) {
	valid := filepath.Join(fixtureDir, "removed-operation", "old.json")
	code, output := run("manifest", "validate", valid)
	if code != exitOK || !strings.Contains(output, "valid") {
		t.Errorf("validate of a valid manifest = %d, %q, want 0 and \"valid\"", code, output)
	}
	invalid := filepath.Join(fixtureDir, "invalid-new", "new.json")
	code, output = run("manifest", "validate", invalid)
	if code != exitError || !strings.Contains(output, "does not match schema") {
		t.Errorf("validate of an invalid manifest = %d, %q, want 2 and the schema's name", code, output)
	}
}

// TestUsage checks the CLI's wrong usage: each of these exits 2.
func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"nonsense"},
		{"manifest"},
		{"manifest", "nonsense"},
		{"manifest", "diff"},
		{"manifest", "diff", "1.json"},
		{"manifest", "validate"},
		{"manifest", "validate", "1.json", "2.json"},
		{"manifest", "diff", "--nonsense", "1.json", "2.json"},
	} {
		if code, output := run(args...); code != exitError {
			t.Errorf("Run(%q) = %d, want %d\n%s", args, code, exitError, output)
		}
	}
	if code, output := run("help"); code != exitOK || !strings.Contains(output, "Usage") {
		t.Errorf("Run(help) = %d, %q, want 0 and the usage", code, output)
	}
}

// TestMissingManifest checks that a path that cannot be read exits 2 with the
// reason.
func TestMissingManifest(t *testing.T) {
	dir := filepath.Join(fixtureDir, "removed-operation")
	code, output := run("manifest", "diff", filepath.Join(dir, "old.json"), filepath.Join(dir, "absent.json"))
	if code != exitError || !strings.Contains(output, "absent.json") {
		t.Errorf("diff with a missing new manifest = %d, %q, want 2 and its path", code, output)
	}
}
