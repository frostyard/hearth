package repocheck

import (
	"bytes"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	// A concrete tailnet or device name. Placeholders such as
	// <tailnet>.ts.net do not match.
	tailnetName = regexp.MustCompile(`(?i)\b[a-z0-9-]+\.ts\.net\b`)
	// A host name on a home network.
	lanName = regexp.MustCompile(`(?i)\b[a-z0-9-]+\.(?:local|lan|home\.arpa|internal)\b`)
	ipv4    = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	// Addresses safe to write down: loopback, unspecified, and RFC 5737's
	// documentation ranges.
	publicExamples = []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
	}
)

// exampleAddress reports whether s is an address this repository may write
// down.
func exampleAddress(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return true // 999.1.1.1 is no address
	}
	if a.IsLoopback() || a.IsUnspecified() {
		return true
	}
	for _, p := range publicExamples {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// coreManaged reports whether dir is a skill directory that frostyard/core's
// skills sync manages: a direct child of skills (the repository's
// .agents/skills) that holds the sync's .synced-from-core marker. Core owns
// and reviews that content, and this repository never edits it (core
// ADR-0026).
func coreManaged(skills, dir string) bool {
	if filepath.Dir(dir) != filepath.Clean(skills) {
		return false
	}
	info, err := os.Lstat(filepath.Join(dir, ".synced-from-core"))
	return err == nil && info.Mode().IsRegular()
}

// TestNoPrivateHosts checks that the repository names no private host: no
// tailnet or device names, no home-network names and no addresses but loopback
// and the documentation ranges (AGENTS.md, Invariants). A skill directory that
// core's sync manages is core's text, not this repository's, and is skipped.
func TestNoPrivateHosts(t *testing.T) {
	skills := filepath.Join(root, ".agents", "skills")
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) || coreManaged(skills, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || d.Name() == "go.sum" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return nil // binary
		}
		files++
		for i, line := range strings.Split(string(data), "\n") {
			where := path + ":" + strconv.Itoa(i+1)
			for _, m := range tailnetName.FindAllString(line, -1) {
				t.Errorf("%s: %q names a tailnet host; write <tailnet>.ts.net", where, m)
			}
			for _, m := range lanName.FindAllString(line, -1) {
				t.Errorf("%s: %q names a home-network host; write a placeholder such as example.com", where, m)
			}
			for _, m := range ipv4.FindAllString(line, -1) {
				if !exampleAddress(m) {
					t.Errorf("%s: %s is a real address; use loopback or a documentation range (192.0.2.0/24)", where, m)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no files checked")
	}
}

// TestCoreManaged checks that only a direct child of .agents/skills with a
// regular .synced-from-core file counts as a core-managed skill.
func TestCoreManaged(t *testing.T) {
	skills := filepath.Join(t.TempDir(), ".agents", "skills")
	mkdir := func(dir string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	marker := func(dir string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ".synced-from-core"), []byte("Managed by frostyard/core\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	managed := marker(mkdir(filepath.Join(skills, "golang-security")))
	local := mkdir(filepath.Join(skills, "new-hearth-app"))
	nested := marker(mkdir(filepath.Join(managed, "references")))
	outside := marker(mkdir(filepath.Join(filepath.Dir(filepath.Dir(skills)), "docs")))
	linked := mkdir(filepath.Join(skills, "linked"))
	if err := os.Symlink(filepath.Join(managed, ".synced-from-core"), filepath.Join(linked, ".synced-from-core")); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]bool{
		managed: true,
		local:   false,
		nested:  false,
		outside: false,
		linked:  false,
		skills:  false,
	} {
		if got := coreManaged(skills, dir); got != want {
			t.Errorf("coreManaged(%s) = %v, want %v", dir, got, want)
		}
	}
}

// TestBoundaryPatterns checks the boundary patterns on the forms this
// repository's files can hold.
func TestBoundaryPatterns(t *testing.T) {
	for line, want := range map[string]bool{
		"https://hearth.<tailnet>.ts.net/":                                 false,
		"`*.<tailnet>.ts.net` shares one site":                             false,
		"ts.net is on the Public Suffix List":                              false,
		"https://hearth." + "tail0000" + ".ts.net/":                        true,
		"http://example.com/v1":                                            false,
		"http://" + "model" + ".local:8080/v1":                             true,
		"~/.local/share/mise":                                              false,
		"127.0.0.1:8443 and 192.0.2.10":                                    false,
		"http://" + strings.Join([]string{"10", "0", "0", "7"}, ".") + "/": true,
		"golangci-lint 2.15.0 and go1.27.4":                                false,
	} {
		got := tailnetName.MatchString(line) || lanName.MatchString(line)
		for _, m := range ipv4.FindAllString(line, -1) {
			got = got || !exampleAddress(m)
		}
		if got != want {
			t.Errorf("flagged(%q) = %v, want %v", line, got, want)
		}
	}
}
