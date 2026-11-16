package repocheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// exampleDir is the example app's directory, from this package's directory.
var exampleDir = filepath.Join(root, "examples", "notes-lite")

var (
	// exampleMarker is the line that marks 1 hand-written file of the example
	// in its README.
	exampleMarker = regexp.MustCompile(`^\s*<!-- file: (\S+) -->\s*$`)
	// exampleFence is the line that opens and closes a fenced code block.
	exampleFence = "```"
)

// exampleBlock is 1 hand-written file of the example, as its README holds it.
type exampleBlock struct {
	// Path is the file's path from the example's directory.
	Path string
	// Body is the block's content: its lines, with 1 final newline.
	Body string
	// Line is the line of the README that marks the block.
	Line int
}

// exampleBlocks returns the marked code blocks of 1 README: a line
// "<!-- file: PATH -->", 1 blank line, and then 1 fenced code block that holds
// the file.
func exampleBlocks(text string) ([]exampleBlock, error) {
	var (
		blocks []exampleBlock
		lines  = strings.Split(text, "\n")
	)
	for i := 0; i < len(lines); i++ {
		m := exampleMarker.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		block := exampleBlock{Path: m[1], Line: i + 1}
		// The marker, 1 blank line, and the opening fence.
		open := i + 2
		if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) != "" {
			return nil, fmt.Errorf("README.md line %d: the marker of %s needs 1 blank line after it, and then the code block", block.Line, block.Path)
		}
		if open >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[open]), exampleFence) {
			return nil, fmt.Errorf("README.md line %d: the marker of %s is not followed by 1 fenced code block", block.Line, block.Path)
		}
		var body []string
		closed := false
		for j := open + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), exampleFence) {
				closed = true
				i = j
				break
			}
			body = append(body, lines[j])
		}
		if !closed {
			return nil, fmt.Errorf("README.md line %d: the code block of %s has no closing ```", block.Line, block.Path)
		}
		if len(body) > 0 {
			block.Body = strings.Join(body, "\n") + "\n"
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

// exampleFiles returns every hand-written file of the example, as a path from
// the example's directory: every file but README.md, go.mod, go.sum, buf.lock,
// third_party/ (which 1 command vendors) and gen/ (which 1 command writes).
func exampleFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(exampleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "gen", "third_party", ".git":
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(exampleDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch rel {
		case "README.md", "go.mod", "go.sum", "buf.lock":
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// TestExampleReadmeFiles checks the decision of ADR-0009: each hand-written
// file of examples/notes-lite is 1 code block of its README, and each block is
// the file's exact content. The README is the tutorial, so it is also its
// source.
func TestExampleReadmeFiles(t *testing.T) {
	readme := filepath.Join(exampleDir, "README.md")
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("%s: %v", readme, err)
	}
	blocks, err := exampleBlocks(string(data))
	if err != nil {
		t.Fatalf("%s: %v\nFix: write the marker, 1 blank line, and the fenced block of the file.", readme, err)
	}
	if len(blocks) == 0 {
		t.Fatalf("%s holds no \"<!-- file: PATH -->\" block: the example's hand-written files are its tutorial", readme)
	}

	marked := make(map[string]bool, len(blocks))
	for _, block := range blocks {
		marked[block.Path] = true
		path := filepath.Join(exampleDir, filepath.FromSlash(block.Path))
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s line %d: the block names %s, and that file is not there: add the file, or remove the block", readme, block.Line, block.Path)
			continue
		}
		if block.Body != string(content) {
			t.Errorf("%s line %d: the block differs from %s: change the file and the block in 1 commit, so that the tutorial is the file's exact content", readme, block.Line, block.Path)
		}
	}

	for _, file := range exampleFiles(t) {
		if !marked[file] {
			t.Errorf("%s/%s is a hand-written file of the example, and %s has no block for it: add \"<!-- file: %s -->\", 1 blank line, and the file's content in 1 fenced block", exampleDir, file, filepath.Base(readme), file)
		}
	}
}

// TestExampleVendoredOptions checks that the example's copy of hearth's options
// file is hearth's file: the README vendors it with 1 command, and the copy is
// what the example's Protobuf files import (ADR-0009).
func TestExampleVendoredOptions(t *testing.T) {
	const file = "frostyard/hearth/v1/options.proto"
	own, err := os.ReadFile(filepath.Join(root, "proto", filepath.FromSlash(file)))
	if err != nil {
		t.Fatalf("proto/%s: %v", file, err)
	}
	copied, err := os.ReadFile(filepath.Join(exampleDir, "third_party", filepath.FromSlash(file)))
	if err != nil {
		t.Fatalf("examples/notes-lite/third_party/%s: %v\nFix: run the README's copy command.", file, err)
	}
	if string(own) != string(copied) {
		t.Errorf("examples/notes-lite/third_party/%s differs from proto/%s: copy hearth's file again with the README's command, and change nothing else", file, file)
	}
}

// TestExampleBlocks checks the block parser on in-memory READMEs.
func TestExampleBlocks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		text   string
		blocks int
		body   string
		error  string
	}{
		{name: "no block", text: "# The example\n\nsome text\n"},
		{
			name:   "1 block",
			text:   "Before.\n<!-- file: notes.go -->\n\n```go\npackage notes\n```\nAfter.\n",
			blocks: 1,
			body:   "package notes\n",
		},
		{
			name:   "1 block with an empty line",
			text:   "<!-- file: a -->\n\n```\n1\n\n2\n```\n",
			blocks: 1,
			body:   "1\n\n2\n",
		},
		{
			name:   "2 blocks",
			text:   "<!-- file: a -->\n\n```\n1\n```\n<!-- file: b -->\n\n```\n2\n```\n",
			blocks: 2,
			body:   "1\n",
		},
		{name: "a marker with no block", text: "<!-- file: a -->\n\ntext\n", error: "is not followed by 1 fenced code block"},
		{name: "a marker with no blank line", text: "<!-- file: a -->\n```\n1\n```\n", error: "needs 1 blank line"},
		{name: "a block with no end", text: "<!-- file: a -->\n\n```\n1\n", error: "has no closing"},
	} {
		blocks, err := exampleBlocks(tc.text)
		switch {
		case tc.error != "":
			if err == nil {
				t.Errorf("%s: exampleBlocks = %d block(s), want the error %q", tc.name, len(blocks), tc.error)
			} else if !strings.Contains(err.Error(), tc.error) {
				t.Errorf("%s: the error = %q, want it to hold %q", tc.name, err, tc.error)
			}
			continue
		case err != nil:
			t.Errorf("%s: exampleBlocks: %v", tc.name, err)
			continue
		}
		if len(blocks) != tc.blocks {
			t.Fatalf("%s: exampleBlocks = %d block(s), want %d", tc.name, len(blocks), tc.blocks)
		}
		if tc.blocks > 0 && blocks[0].Body != tc.body {
			t.Errorf("%s: the first block's body = %q, want %q", tc.name, blocks[0].Body, tc.body)
		}
	}
}
