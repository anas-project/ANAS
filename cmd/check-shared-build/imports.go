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
	body, err := os.ReadFile(filepath.Join(root, image.Dockerfile))
	if err != nil {
		return err
	}
	local, err := localCopySources(root, image.Context, string(body))
	if err != nil {
		return fmt.Errorf("%s: %w", image.Dockerfile, err)
	}
	available := append(append([]string(nil), copied...), local...)
	for _, directory := range available {
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

// A build context can hold other applications that are never copied into this
// image. Only COPY inputs belong in its dependency closure; scanning the whole
// context reports dependencies of those unrelated applications as missing.
func localCopySources(root, contextPath, body string) ([]string, error) {
	var sources []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\\\n", " "), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.ToUpper(fields[0]) != "COPY" {
			continue
		}
		fields = fields[1:]
		fromStage := false
		for len(fields) > 0 && strings.HasPrefix(fields[0], "--") {
			if strings.HasPrefix(fields[0], "--from=") {
				fromStage = true
			}
			fields = fields[1:]
		}
		if fromStage {
			continue
		}
		if len(fields) < 2 {
			return nil, fmt.Errorf("unsupported local COPY: %s", line)
		}
		for _, source := range fields[:len(fields)-1] {
			if !filepath.IsLocal(source) || strings.ContainsAny(source, "[]\"$") {
				return nil, fmt.Errorf("unsupported local COPY source %s", source)
			}
			matches, err := filepath.Glob(filepath.Join(root, contextPath, source))
			if err != nil || len(matches) == 0 {
				return nil, fmt.Errorf("missing local COPY source %s", source)
			}
			for _, match := range matches {
				relative, err := filepath.Rel(root, match)
				if err != nil {
					return nil, err
				}
				sources = append(sources, relative)
			}
		}
	}
	return sources, nil
}
