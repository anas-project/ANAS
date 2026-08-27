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
	printDigests := flag.Bool("print-digests", false, "print reviewed requirement digests without writing files")
	root := flag.String("root", "", "repository root (auto-detected by default)")
	flag.Parse()

	if *root == "" {
		var err error
		*root, err = findRepoRoot()
		if err != nil {
			fatal(err)
		}
	}
	if *check && *printDigests {
		fatal(errors.New("--check and --print-digests cannot be used together"))
	}
	if err := testcasecatalog.Run(testcasecatalog.Options{
		Root:         *root,
		Check:        *check,
		PrintDigests: *printDigests,
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
