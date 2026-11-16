package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/frostyard/hearth/manifest"
)

// The CLI's exit codes (docs/specs/manifest.md, Exit codes).
const (
	// exitOK: nothing needs the app's next major version.
	exitOK = 0
	// exitBreaking: a breaking change needs the app's next major version.
	exitBreaking = 1
	// exitError: the command could not run, or a manifest is not readable.
	exitError = 2
)

// usageText is the CLI's help.
const usageText = `hearth generates and checks an app's hearth manifest.

Usage:
  hearth manifest diff OLD NEW       compare 2 manifests for breaking changes
  hearth manifest validate FILE      check 1 manifest against the schema

The diff prints 1 line for each breaking change, then the verdict. It exits 0
when there is no breaking change or the app's next major version allows it, 1
when a breaking change needs the next major version, and 2 when a manifest
cannot be read or is not valid.
`

// Run runs the CLI with args (without the program name) and returns its exit
// code. It writes its result to stdout and its problems to stderr.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return exitError
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return exitOK
	case "manifest":
		return runManifest(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "hearth: unknown command %q\n\n%s", args[0], usageText)
	return exitError
}

// runManifest runs the "manifest" command's subcommand.
func runManifest(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "hearth: manifest needs a subcommand: diff or validate\n\n%s", usageText)
		return exitError
	}
	switch args[0] {
	case "diff":
		return runDiff(args[1:], stdout, stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "hearth: unknown manifest subcommand %q\n\n%s", args[0], usageText)
	return exitError
}

// runDiff compares 2 manifests and prints the breaking changes and the verdict.
func runDiff(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("hearth manifest diff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	verbose := flags.Bool("verbose", false, "print the additions as well")
	flags.Usage = func() {
		fmt.Fprint(stderr, usageText)
	}
	if err := flags.Parse(args); err != nil {
		return exitError
	}
	if flags.NArg() != 2 {
		fmt.Fprintf(stderr, "hearth: manifest diff needs 2 paths: OLD NEW\n\n%s", usageText)
		return exitError
	}
	oldPath, nextPath := flags.Arg(0), flags.Arg(1)

	if _, err := os.Stat(oldPath); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stderr, "hearth: %v\n", err)
			return exitError
		}
		fmt.Fprintln(stdout, "no old manifest: nothing to compare")
		return exitOK
	}
	oldManifest, err := manifest.Read(oldPath)
	if err != nil {
		fmt.Fprintf(stderr, "hearth: old manifest: %v\n", err)
		return exitError
	}
	nextManifest, err := manifest.Read(nextPath)
	if err != nil {
		fmt.Fprintf(stderr, "hearth: new manifest: %v\n", err)
		return exitError
	}

	result := manifest.Diff(oldManifest, nextManifest)
	for _, line := range result.Breaking {
		fmt.Fprintln(stdout, line)
	}
	if *verbose {
		for _, line := range result.Additions {
			fmt.Fprintln(stdout, line)
		}
	}
	from := oldManifest.Version
	to := nextManifest.Version
	if len(result.Breaking) == 0 {
		fmt.Fprintf(stdout, "compatible: %s -> %s\n", from, to)
		return exitOK
	}
	nextMajor, err := manifest.NextMajor(from, to)
	if err != nil {
		fmt.Fprintf(stderr, "hearth: %v\n", err)
		return exitError
	}
	if nextMajor {
		fmt.Fprintf(stdout, "the major version allows the breaking changes: %s -> %s\n", from, to)
		return exitOK
	}
	fmt.Fprintf(stdout, "the breaking changes need the next major version: %s -> %s\n", from, to)
	return exitBreaking
}

// runValidate checks 1 manifest against the format's schema.
func runValidate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, "hearth: manifest validate needs 1 path\n\n%s", usageText)
		return exitError
	}
	if _, err := manifest.Read(args[0]); err != nil {
		fmt.Fprintf(stderr, "hearth: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stdout, "%s: valid\n", args[0])
	return exitOK
}
