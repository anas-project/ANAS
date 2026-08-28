// Command gen-test-case-docs validates test case traceability and generates
// human-readable catalogs from test-env/cases/*/cases.yml.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/testcasecatalog"
)

func main() {
	check := flag.Bool("check", false, "fail if generated test case documentation is stale")
	printDigests := flag.Bool("print-digests", false, "print current requirement and implementation digests without writing files")
	reviewBase := flag.String("review-diff", "", "print requirement, case, and test implementation diffs from a Git revision")
	root := flag.String("root", "", "repository root (auto-detected by default)")
	flag.Parse()

	if *root == "" {
		var err error
		*root, err = findRepoRoot()
		if err != nil {
			fatal(err)
		}
	}
	selectedModes := 0
	for _, selected := range []bool{*check, *printDigests, *reviewBase != ""} {
		if selected {
			selectedModes++
		}
	}
	if selectedModes > 1 {
		fatal(errors.New("--check, --print-digests, and --review-diff cannot be used together"))
	}
	if err := testcasecatalog.Run(testcasecatalog.Options{
		Root:         *root,
		Check:        *check,
		PrintDigests: *printDigests,
		ReviewBase:   *reviewBase,
		Output:       os.Stdout,
	}); err != nil {
		fatal(err)
	}
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if exists(filepath.Join(dir, "go.mod")) && exists(filepath.Join(dir, "test-env", "cases")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find repository root")
		}
		dir = parent
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
