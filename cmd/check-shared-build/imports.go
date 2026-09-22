package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Inspect every platform's source imports, not just the checker's host GOOS.
// The manifests and COPY lines can agree while both omit a new dependency.
// This is deliberately a source-closure check; it does not claim Docker build
// stages, external dependencies, generated sources or embed inputs are valid.
func checkSharedGoImports(root string, image imageEntry, copied []string) error {
	available := append(append([]string(nil), copied...), image.Context)
	for _, directory := range available {
		info, err := os.Stat(filepath.Join(root, directory))
		if err != nil {
			return err
		}
		if !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("%s: cannot parse Go build input", image.Dockerfile)
			}
			for _, imp := range file.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				const prefix = "github.com/anas-project/ANAS/"
				if !strings.HasPrefix(name, prefix) {
					continue
				}
				dependency := strings.TrimPrefix(name, prefix)
				if !covers(available, dependency) {
					return fmt.Errorf("%s: transitive Go package %s is absent from build inputs", image.Dockerfile, dependency)
				}
				if info, err := os.Stat(filepath.Join(root, dependency)); err != nil || !info.IsDir() {
					return fmt.Errorf("%s: missing source package %s", image.Dockerfile, dependency)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
