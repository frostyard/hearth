package workflowcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

var (
	commitAction = regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	dockerAction = regexp.MustCompile(`^docker://[^@]+@sha256:[0-9a-f]{64}$`)
)

// immutable reports whether an action reference names one version for good: a
// local action, a commit, or an image digest.
func immutable(ref string) bool {
	return strings.HasPrefix(ref, "./") || commitAction.MatchString(ref) || dockerAction.MatchString(ref)
}

// TestPinnedReferencesAreImmutable checks the action reference rule of row B5.
func TestPinnedReferencesAreImmutable(t *testing.T) {
	sha, digest := strings.Repeat("a", 40), strings.Repeat("b", 64)
	for ref, want := range map[string]bool{
		"actions/checkout@" + sha:                   true,
		"owner/repo/.github/actions/publish@" + sha: true,
		"docker://alpine@sha256:" + digest:          true,
		"./.github/actions/check":                   true,
		"actions/checkout@v7":                       false,
		"owner/repo/.github/actions/publish@main":   false,
		"actions/checkout@deadbeef":                 false,
		"actions/checkout@${{inputs.ref}}":          false,
		"docker://alpine:3.22":                      false,
		"actions/checkout@" + strings.ToUpper(sha):  false,
	} {
		if got := immutable(ref); got != want {
			t.Errorf("immutable(%q) = %v, want %v", ref, got, want)
		}
	}
}

// workflows parses every workflow in .github/workflows.
func workflows(t *testing.T) map[string]*yaml.Node {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no workflows found")
	}
	out := map[string]*yaml.Node{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
			t.Fatalf("parse %s: %v", p, err)
		}
		out[filepath.Base(p)] = doc.Content[0]
	}
	return out
}

// get returns the value of key in a mapping node, or nil.
func get(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// walk calls f for every mapping node under n.
func walk(n *yaml.Node, f func(*yaml.Node)) {
	if n.Kind == yaml.MappingNode {
		f(n)
	}
	for _, c := range n.Content {
		walk(c, f)
	}
}

// TestWorkflowsFollowADR0021 checks that every external action is pinned to a
// full commit SHA (the version a trailing comment names is for people; the SHA
// is what runs), that every workflow starts from declared permissions, and
// that every checkout drops the token it would otherwise leave in
// .git/config.
func TestWorkflowsFollowADR0021(t *testing.T) {
	uses := 0
	for name, root := range workflows(t) {
		if get(root, "permissions") == nil {
			t.Errorf("%s: no top-level permissions (start from permissions: {})", name)
		}
		walk(root, func(m *yaml.Node) {
			ref := get(m, "uses")
			if ref == nil {
				return
			}
			uses++
			if ref.Kind != yaml.ScalarNode || !immutable(ref.Value) {
				t.Errorf("%s:%d: %q must name a full commit SHA or image digest", name, ref.Line, ref.Value)
			}
			if strings.HasPrefix(ref.Value, "actions/checkout@") {
				if pc := get(get(m, "with"), "persist-credentials"); pc == nil || pc.Value != "false" {
					t.Errorf("%s:%d: checkout must set persist-credentials: false", name, ref.Line)
				}
			}
		})
	}
	if uses == 0 {
		t.Fatal("no action references found")
	}
}

// TestRequiredCI checks that the pull request gate is the job verify, without a
// name key, so that the required status check stays "verify", and that the
// workflows run on a pull request, on main and in the merge queue.
func TestRequiredCI(t *testing.T) {
	root, ok := workflows(t)["ci.yml"]
	if !ok {
		t.Fatal("no .github/workflows/ci.yml")
	}
	jobs := get(root, "jobs")
	if jobs == nil || get(jobs, "verify") == nil {
		t.Fatal("ci.yml has no job verify")
	}
	if name := get(get(jobs, "verify"), "name"); name != nil {
		t.Errorf("the job verify has the name %q; it must have none, so that the required check stays \"verify\"", name.Value)
	}
	on := get(root, "on")
	for _, trigger := range []string{"pull_request", "push", "merge_group"} {
		if get(on, trigger) == nil {
			t.Errorf("ci.yml does not run on %s", trigger)
		}
	}
	// A pull request gate needs the base commit, for the corrections check.
	found := false
	walk(root, func(m *yaml.Node) {
		if run := get(m, "run"); run != nil && strings.Contains(run.Value, "make ci") {
			found = true
		}
	})
	if !found {
		t.Error("ci.yml runs no make ci")
	}
}

// TestSiteWorkflow checks the documentation site's workflow: it builds on a
// pull request, deploys from main, and runs in the merge queue.
func TestSiteWorkflow(t *testing.T) {
	root, ok := workflows(t)["site.yml"]
	if !ok {
		t.Fatal("no .github/workflows/site.yml")
	}
	on := get(root, "on")
	for _, trigger := range []string{"pull_request", "push", "merge_group"} {
		if get(on, trigger) == nil {
			t.Errorf("site.yml does not run on %s", trigger)
		}
	}
	found := false
	walk(root, func(m *yaml.Node) {
		if run := get(m, "run"); run != nil && strings.Contains(run.Value, "make site") {
			found = true
		}
	})
	if !found {
		t.Error("site.yml runs no make site")
	}
}
