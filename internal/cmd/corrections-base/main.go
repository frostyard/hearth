// Command corrections-base checks the corrections inbox against the base of a
// pull request: no record file is deleted or renamed since then, and no record
// changes in a field other than an empty promoted_to that is now filled
// (frostyard/core ADR-0081, row B2 of its repository baseline).
//
//	make ci BASE=origin/main
//
// The command passes when the base has no corrections folder, because such a
// base has no record to keep.
package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// correctionsDir is the repository's corrections inbox.
const correctionsDir = ".memory/corrections"

func main() {
	base := flag.String("base", "", "the git reference to compare with, such as origin/main")
	flag.Parse()
	if *base == "" {
		fmt.Fprintf(os.Stderr, "%s: -base REF is required\n", os.Args[0])
		os.Exit(2)
	}
	if err := run(context.Background(), *base, correctionsDir, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "corrections-base: %v\n", err)
		os.Exit(1)
	}
}

// run checks the records of dir against base. It writes what it checked to
// stdout, and returns an error that names each problem.
func run(ctx context.Context, base, dir string, stdout, stderr io.Writer) error {
	present, err := hasDir(ctx, base, dir)
	if err != nil {
		return err
	}
	if !present {
		fmt.Fprintf(stdout, "%s has no %s: nothing to compare\n", base, dir)
		return nil
	}
	output, err := git(ctx, "diff", "--name-status", "-M", base, "--", dir)
	if err != nil {
		return err
	}
	changes := parseChanges(output)
	if len(changes) == 0 {
		fmt.Fprintf(stdout, "corrections-base: no record changed since %s\n", base)
		return nil
	}
	var problems []string
	records := 0
	for _, c := range changes {
		switch {
		case c.Status == "A":
			records++
		case len(c.Paths) == 2:
			problems = append(problems, fmt.Sprintf("%s was renamed to %s; a record file is permanent", c.Paths[0], c.Paths[1]))
		case c.Status == "D":
			problems = append(problems, c.Paths[0]+" was deleted; a record file is permanent")
		case c.Status == "M" || c.Status == "T":
			records++
			before, err := git(ctx, "show", base+":"+c.Paths[0])
			if err != nil {
				return err
			}
			after, err := os.ReadFile(c.Paths[0])
			if err != nil {
				return fmt.Errorf("read %s: %w", c.Paths[0], err)
			}
			problems = append(problems, recordProblems(c.Paths[0], []byte(before), after)...)
		default:
			problems = append(problems, c.Status+" "+strings.Join(c.Paths, " ")+" is not a change this check knows")
		}
	}
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(stderr, "corrections-base: "+problem)
		}
		return fmt.Errorf("%d problem(s) with the corrections inbox since %s", len(problems), base)
	}
	fmt.Fprintf(stdout, "corrections-base: %d record file(s) checked since %s\n", records, base)
	return nil
}

// hasDir reports whether base has the folder dir.
func hasDir(ctx context.Context, base, dir string) (bool, error) {
	output, err := git(ctx, "ls-tree", "--name-only", base, dir)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

// git runs 1 git command in the current directory and returns its standard
// output.
func git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// change is 1 line of git diff --name-status -M.
type change struct {
	// Status is the change's letter: A, M, D, T, or R### or C### with the
	// similarity score.
	Status string
	// Paths is the changed path, or the old path and the new path of a rename
	// or a copy.
	Paths []string
}

// parseChanges reads the output of git diff --name-status -M.
func parseChanges(output string) []change {
	var changes []change
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 2 {
			continue
		}
		changes = append(changes, change{Status: fields[0], Paths: fields[1:]})
	}
	return changes
}

// record is the part of 1 correction record that this check compares.
type record struct {
	Date       string `json:"date"`
	Scope      string `json:"scope"`
	Correction string `json:"correction"`
	Evidence   string `json:"evidence"`
	PromotedTo string `json:"promoted_to"`
}

// recordProblems returns each change of 1 record file that the inbox does not
// allow: a changed field, except an empty promoted_to that is now filled.
func recordProblems(path string, before, after []byte) []string {
	var old, next record
	if err := unmarshal(before, &old); err != nil {
		return []string{fmt.Sprintf("%s: the base's record cannot be read: %v", path, err)}
	}
	if err := unmarshal(after, &next); err != nil {
		return []string{fmt.Sprintf("%s: the record cannot be read: %v", path, err)}
	}
	var problems []string
	for _, field := range []struct {
		name     string
		old, new string
	}{
		{"date", old.Date, next.Date},
		{"scope", old.Scope, next.Scope},
		{"correction", old.Correction, next.Correction},
		{"evidence", old.Evidence, next.Evidence},
	} {
		if field.old != field.new {
			problems = append(problems, fmt.Sprintf("%s: %s changed from %q to %q; a record's text is permanent", path, field.name, field.old, field.new))
		}
	}
	if old.PromotedTo != next.PromotedTo && (old.PromotedTo != "" || next.PromotedTo == "") {
		problems = append(problems, fmt.Sprintf("%s: promoted_to changed from %q to %q; only an empty promoted_to may be filled", path, old.PromotedTo, next.PromotedTo))
	}
	return problems
}

// unmarshal reads 1 record.
func unmarshal(data []byte, rec *record) error {
	if err := json.Unmarshal(data, rec); err != nil {
		return fmt.Errorf("read the record: %w", err)
	}
	return nil
}
