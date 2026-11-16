package repocheck

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The 2 manifests of the manifest gate's test: the base commit's, and the
// working tree's. The stub of the manifest diff records both, so that a case
// tells which 1 the recipe read.
const (
	baseManifest = `{"hearth":1,"app":"notelite","version":"0.1.0"}`
	treeManifest = `{"hearth":1,"app":"notelite","version":"0.2.0"}`
)

// manifestGateTool is the tools of the manifest gate's test, and the repository
// that the recipe runs in.
type manifestGateTool struct {
	// dir is the repository, which holds the example's Makefile.
	dir string
	// makePath and shell are the tools that the test runs.
	makePath string
	shell    string
	// gitPath is the real git.
	gitPath string
	// first is the commit that holds no manifest.
	first string
	// second is the commit that holds 1.
	second string
	// record is the file that the stub of the manifest diff writes.
	record string
}

// TestManifestGate checks the example's manifest-check recipe: it compares the
// app's manifest with the 1 at BASE, and it fails closed when it cannot look the
// manifest up (AGENTS.md, A gate recipe fails closed). The recipe runs in 1
// temporary git repository, with a stub in place of the manifest diff, and git
// stubs for the failures.
func TestManifestGate(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not on PATH, so the manifest gate cannot run")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH, so the manifest gate cannot run")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not on PATH, so the manifest gate cannot run")
	}
	tool := setupManifestGate(t, makePath, gitPath, shell)

	// The stubs of the 2 git failures.
	lsTree := tool.gitStub(t, "ls-tree")
	show := tool.gitStub(t, "show")

	for _, tc := range []struct {
		name string
		// base is the recipe's BASE, and gitStub is the stub that replaces git,
		// or "" for the real git.
		base    string
		gitStub string
		// code is the exit code of the stub of the manifest diff.
		code int
		// want is the recipe's exit code: 0, 2, or -1 for any non-zero code.
		want int
		// compared reports whether the stub of the manifest diff must have run.
		compared bool
		// message is a substring of the recipe's output.
		message string
	}{
		{name: "no base", base: "", want: 0, compared: false, message: "BASE is not set"},
		{name: "a base that is no commit", base: "no-such-commit", want: 2, compared: false, message: "is not a commit"},
		{name: "no manifest at the base", base: "first", want: 0, compared: false, message: "no manifest"},
		{name: "ls-tree fails", base: "second", gitStub: lsTree, want: 2, compared: false, message: "ls-tree"},
		{name: "show fails", base: "second", gitStub: show, want: 2, compared: false, message: "show"},
		{name: "a manifest at the base", base: "second", want: 0, compared: true},
		{name: "a breaking change", base: "second", code: 1, want: -1, compared: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Remove(tool.record); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("remove the record: %v", err)
			}
			base := tc.base
			switch base {
			case "first":
				base = tool.first
			case "second":
				base = tool.second
			}
			git := tool.gitPath
			if tc.gitStub != "" {
				git = tc.gitStub
			}
			hearth := tool.hearthStub(t, tc.code)

			command := exec.Command(tool.makePath, "manifest-check", "BASE="+base, "GIT="+git, "HEARTH="+hearth)
			command.Dir = tool.dir
			// A git of the test's own: no global or system configuration.
			command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
			out, err := command.CombinedOutput()
			code := exitCode(err)
			switch {
			case tc.want == 0 && code != 0:
				t.Errorf("manifest-check with %s = exit %d, want 0:\n%s", tc.name, code, out)
			case tc.want > 0 && code != tc.want:
				t.Errorf("manifest-check with %s = exit %d, want %d:\n%s", tc.name, code, tc.want, out)
			case tc.want < 0 && code == 0:
				t.Errorf("manifest-check with %s = exit 0, want a failure:\n%s", tc.name, out)
			}
			if tc.message != "" && !strings.Contains(string(out), tc.message) {
				t.Errorf("the output does not hold %q:\n%s", tc.message, out)
			}
			_, err = os.Stat(tool.record)
			compared := err == nil
			if compared != tc.compared {
				t.Errorf("the manifest diff ran = %v, want %v:\n%s", compared, tc.compared, out)
			}
			if !tc.compared {
				return
			}
			record, err := os.ReadFile(tool.record)
			if err != nil {
				t.Fatalf("read the record: %v", err)
			}
			// The stub got the base commit's manifest and the working
			// tree's.
			for _, want := range []string{"arg manifest", "arg diff", baseManifest, treeManifest} {
				if !strings.Contains(string(record), want) {
					t.Errorf("the manifest diff did not get %q:\n%s", want, record)
				}
			}
		})
	}
}

// setupManifestGate makes 1 git repository with the example's Makefile, 1 commit
// that holds no manifest and 1 that does.
func setupManifestGate(t *testing.T, makePath, gitPath, shell string) *manifestGateTool {
	t.Helper()
	tool := &manifestGateTool{
		dir:      t.TempDir(),
		makePath: makePath,
		shell:    shell,
		gitPath:  gitPath,
		record:   filepath.Join(t.TempDir(), "manifest-diff.txt"),
	}
	makefile, err := os.ReadFile(filepath.Join(exampleDir, "Makefile"))
	if err != nil {
		t.Fatalf("read the example's Makefile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tool.dir, "Makefile"), makefile, 0o644); err != nil {
		t.Fatalf("write the Makefile: %v", err)
	}
	tool.git(t, "init", "-q", ".")
	tool.git(t, "add", "Makefile")
	tool.git(t, "commit", "-q", "-m", "the gate")
	tool.first = tool.git(t, "rev-parse", "HEAD")

	if err := os.MkdirAll(filepath.Join(tool.dir, "gen"), 0o755); err != nil {
		t.Fatalf("make gen/: %v", err)
	}
	tool.writeTree(t, baseManifest)
	tool.git(t, "add", "gen/manifest.json")
	tool.git(t, "commit", "-q", "-m", "the manifest")
	tool.second = tool.git(t, "rev-parse", "HEAD")

	// The working tree's manifest is the newer 1, so a case tells the 2 apart.
	tool.writeTree(t, treeManifest)
	return tool
}

// git runs 1 git command in the repository, with an identity of the test's own,
// and returns its trimmed output.
func (tool *manifestGateTool) git(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=hearth",
		"-c", "user.email=hearth@example.com",
		"-c", "commit.gpgsign=false",
	}, args...)
	command := exec.Command(tool.gitPath, full...)
	command.Dir = tool.dir
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeTree writes 1 manifest into the repository's working tree.
func (tool *manifestGateTool) writeTree(t *testing.T, manifest string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tool.dir, "gen", "manifest.json"), []byte(manifest+"\n"), 0o644); err != nil {
		t.Fatalf("write gen/manifest.json: %v", err)
	}
}

// hearthStub writes a stub of the manifest diff that records its arguments, and
// the content of each of them that is a file, and exits with code.
func (tool *manifestGateTool) hearthStub(t *testing.T, code int) string {
	t.Helper()
	path := filepath.Join(tool.dir, fmt.Sprintf("hearth-stub-%d.sh", code))
	script := fmt.Sprintf(`#!/bin/sh
# Records the arguments of 1 manifest diff, with the content of each file, and
# then gives the exit code of the case.
{
  for arg in "$@"; do
    if [ -f "$arg" ]; then
      printf 'file %%s\n' "$arg"
      cat "$arg"
      printf '\n'
    else
      printf 'arg %%s\n' "$arg"
    fi
  done
} > '%s'
exit %d
`, tool.record, code)
	tool.writeScript(t, path, script)
	return path
}

// gitStub writes a stub of git that runs the real git, but exits 1 for 1
// subcommand.
func (tool *manifestGateTool) gitStub(t *testing.T, fail string) string {
	t.Helper()
	path := filepath.Join(tool.dir, "git-stub-"+fail+".sh")
	script := fmt.Sprintf(`#!/bin/sh
# Runs the real git, and fails for 1 subcommand.
if [ "$1" = '%s' ]; then
  exit 1
fi
exec '%s' "$@"
`, fail, tool.gitPath)
	tool.writeScript(t, path, script)
	return path
}

// writeScript writes 1 executable script.
func (tool *manifestGateTool) writeScript(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write the stub %s: %v", path, err)
	}
}

// exitCode returns the exit code of 1 command's error, or 0.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}
