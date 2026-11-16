package workflowcheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// bufGenFile is the part of 1 buf.gen.yaml that this repository guards.
type bufGenFile struct {
	Version string `yaml:"version"`
	Plugins []struct {
		Remote string `yaml:"remote"`
		Local  any    `yaml:"local"`
	} `yaml:"plugins"`
	// The type's name is only for the YAML decoder: a template with remote
	// plugins is a problem wherever it is.
	Managed struct {
		Enabled bool `yaml:"enabled"`
		Disable []struct {
			Remote string `yaml:"remote"`
		} `yaml:"disable"`
	} `yaml:"managed"`
}

// remotePlugins returns each remote plugin of 1 buf.gen.yaml's text.
func remotePlugins(text string) ([]string, error) {
	var cfg bufGenFile
	if err := yaml.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, err
	}
	var out []string
	for _, plugin := range cfg.Plugins {
		if plugin.Remote != "" {
			out = append(out, plugin.Remote)
		}
	}
	for _, plugin := range cfg.Managed.Disable {
		if plugin.Remote != "" {
			out = append(out, plugin.Remote)
		}
	}
	return out, nil
}

// bufModule is 1 module of buf.yaml.
type bufModule struct {
	Path string `yaml:"path"`
	Deps []any  `yaml:"deps"`
}

// bufConfig is the part of buf.yaml that this repository guards.
type bufConfig struct {
	Version string      `yaml:"version"`
	Deps    []any       `yaml:"deps"`
	Modules []bufModule `yaml:"modules"`
}

// depNames returns the module names of 1 dependency list. A dependency is a
// name, or a mapping with a name.
func depNames(deps []any) []string {
	out := make([]string, 0, len(deps))
	for _, dep := range deps {
		switch value := dep.(type) {
		case string:
			out = append(out, value)
		case map[string]any:
			if name, ok := value["name"].(string); ok {
				out = append(out, name)
			}
		}
	}
	return out
}

// declaredDeps returns each module dependency that 1 buf.yaml's text declares.
func declaredDeps(text string) ([]string, error) {
	var cfg bufConfig
	if err := yaml.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, err
	}
	out := depNames(cfg.Deps)
	for _, module := range cfg.Modules {
		out = append(out, depNames(module.Deps)...)
	}
	return out, nil
}

// bufLock is the part of buf.lock that this repository guards.
type bufLock struct {
	Version string `yaml:"version"`
	Deps    []struct {
		Name   string `yaml:"name"`
		Commit string `yaml:"commit"`
		Digest string `yaml:"digest"`
	} `yaml:"deps"`
}

// lockProblems returns each problem of the buf lock files: 1 buf.lock is
// required beside 1 buf.yaml, each dependency that buf.yaml declares needs an
// entry with a digest, and each entry needs a digest.
func lockProblems(hasConfig bool, declared []string, lockText string, hasLock bool) ([]string, error) {
	if !hasConfig {
		return nil, nil
	}
	var problems []string
	if !hasLock {
		return append(problems, "buf.yaml exists, and buf.lock does not: run buf dep update and commit buf.lock"), nil
	}
	var lock bufLock
	if err := yaml.Unmarshal([]byte(lockText), &lock); err != nil {
		return nil, err
	}
	digests := make(map[string]string, len(lock.Deps))
	for _, dep := range lock.Deps {
		if dep.Digest == "" {
			problems = append(problems, "buf.lock: the dependency "+dep.Name+" has no digest")
		}
		digests[dep.Name] = dep.Digest
	}
	for _, name := range declared {
		if _, ok := digests[name]; !ok {
			problems = append(problems, "buf.yaml declares the dependency "+name+", and buf.lock has no entry for it")
		}
	}
	return problems, nil
}

// TestBufNoRemotePlugin guards frostyard/core ADR-0085: no remote BSR plugin
// runs, so every generated file comes from a plugin that mise.lock pins.
func TestBufNoRemotePlugin(t *testing.T) {
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
	for _, template := range templates {
		data, err := os.ReadFile(template)
		if err != nil {
			t.Fatal(err)
		}
		remotes, err := remotePlugins(string(data))
		if err != nil {
			t.Fatalf("%s: %v", template, err)
		}
		for _, remote := range remotes {
			t.Errorf("%s runs the remote plugin %s; a plugin comes from mise.lock only", template, remote)
		}
	}
	t.Logf("%d buf.gen template(s), no remote plugin", len(templates))
}

// TestBufDepsPinned guards frostyard/core ADR-0085: buf.lock pins every module
// dependency by digest. The repository has more than 1 buf workspace (the
// example app is a module of its own, ADR-0009), so every buf.yaml is checked
// against the buf.lock beside it.
func TestBufDepsPinned(t *testing.T) {
	var configs []string
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
		if d.Name() == "buf.yaml" {
			configs = append(configs, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) == 0 {
		t.Fatal("no buf.yaml found")
	}
	slices.Sort(configs)
	for _, config := range configs {
		data, err := os.ReadFile(config)
		if err != nil {
			t.Fatalf("%s: %v", config, err)
		}
		declared, err := declaredDeps(string(data))
		if err != nil {
			t.Fatalf("%s: %v", config, err)
		}
		lockPath := filepath.Join(filepath.Dir(config), "buf.lock")
		lockText, err := os.ReadFile(lockPath)
		hasLock := err == nil
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("%s: %v", lockPath, err)
		}
		problems, err := lockProblems(true, declared, string(lockText), hasLock)
		if err != nil {
			t.Fatalf("%s: %v", lockPath, err)
		}
		for _, problem := range problems {
			t.Errorf("%s: %s", config, problem)
		}
		t.Logf("%s: %d declared dependency(ies), buf.lock present: %v", config, len(declared), hasLock)
	}
}

// TestLockProblems checks the dependency rules on in-memory files.
func TestLockProblems(t *testing.T) {
	const lock = `version: v2
deps:
  - name: buf.build/googleapis/googleapis
    commit: a1b2c3
    digest: sha256:abc
`
	for _, tc := range []struct {
		name      string
		hasConfig bool
		declared  []string
		text      string
		hasLock   bool
		want      string // a substring of the 1 problem; empty for no problem
	}{
		{"a pinned dependency", true, []string{"buf.build/googleapis/googleapis"}, lock, true, ""},
		{"no dependency at all", true, nil, "version: v2\n", true, ""},
		{"a buf.yaml with no buf.lock", true, nil, "", false, "buf.lock does not"},
		{"no buf.yaml at all", false, nil, "", false, ""},
		{"a dependency with no lock entry", true, []string{"buf.build/googleapis/googleapis"}, "version: v2\n", true, "buf.lock has no entry"},
		{"an entry with no digest", true, []string{"buf.build/googleapis/googleapis"}, `version: v2
deps:
  - name: buf.build/googleapis/googleapis
    commit: a1b2c3
`, true, "has no digest"},
		{"an undeclared entry with no digest", true, nil, `version: v2
deps:
  - name: buf.build/googleapis/googleapis
`, true, "has no digest"},
	} {
		problems, err := lockProblems(tc.hasConfig, tc.declared, tc.text, tc.hasLock)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		switch {
		case tc.want == "" && len(problems) > 0:
			t.Errorf("%s: lockProblems = %v, want none", tc.name, problems)
		case tc.want != "" && len(problems) != 1:
			t.Errorf("%s: lockProblems = %v, want exactly 1 problem that contains %q", tc.name, problems, tc.want)
		case tc.want != "" && !strings.Contains(problems[0], tc.want):
			t.Errorf("%s: lockProblems = %q, want it to contain %q", tc.name, problems[0], tc.want)
		}
	}
}

// TestDeclaredDeps checks that a dependency is read at the top of buf.yaml and
// in a module.
func TestDeclaredDeps(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"no dependency", "version: v2\nmodules:\n  - path: proto\n", nil},
		{"a top-level dependency", "version: v2\ndeps:\n  - buf.build/a/b\nmodules:\n  - path: proto\n", []string{"buf.build/a/b"}},
		{"a module dependency", "version: v2\nmodules:\n  - path: proto\n    deps:\n      - buf.build/a/b\n", []string{"buf.build/a/b"}},
	} {
		got, err := declaredDeps(tc.text)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: declaredDeps = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRemotePlugins checks the plugin rule on in-memory templates.
func TestRemotePlugins(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"a local plugin", "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: gen\n", nil},
		{"a local command", "version: v2\nplugins:\n  - local: [\"go\", \"run\", \"./cmd/x\"]\n    out: gen\n", nil},
		{"a remote plugin", "version: v2\nplugins:\n  - remote: buf.build/protocolbuffers/go\n    out: gen\n", []string{"buf.build/protocolbuffers/go"}},
		{"no plugin", "version: v2\n", nil},
	} {
		got, err := remotePlugins(tc.text)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: remotePlugins = %q, want %q", tc.name, got, tc.want)
		}
	}
}
