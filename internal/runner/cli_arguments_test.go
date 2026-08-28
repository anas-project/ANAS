package runner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every CLI entry point must do something with the words left over after flag
// parsing -- reject them, or use them. Ignoring them is the failure mode this
// guards: `anas plan extra` would look like it worked while quietly doing
// something other than what was typed, and nothing about the code reads as
// wrong, which is why it survived long enough to be found in review.
//
// This is a source scan rather than a behavioural test because the alternative
// would be invoking every command for real. The defect is visible in the shape
// of the function, so that is where it is checked.
func TestCLIEntryPointsAccountForLeftoverArguments(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	checked := 0
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Body == nil {
				continue
			}
			if !strings.HasPrefix(function.Name.Name, "run") && !strings.HasPrefix(function.Name.Name, "parse") {
				continue
			}

			body := source(t, fileSet, function)
			if !strings.Contains(body, "flag.NewFlagSet") {
				continue
			}
			checked++

			// parseInterspersed returns the positional words, so a caller that
			// wanted to drop them would have to write `_`, which reads as a
			// decision rather than an omission.
			if strings.Contains(body, "parseInterspersed(") {
				if strings.Contains(body, "_, err := parseInterspersed(") {
					t.Errorf("%s in %s discards the positional arguments; reject them or use them",
						function.Name.Name, name)
				}
				continue
			}

			// fs.Parse leaves them on the FlagSet, where ignoring them is silent.
			if strings.Contains(body, "fs.Parse(") &&
				!strings.Contains(body, "fs.NArg()") &&
				!strings.Contains(body, "fs.Args()") {
				t.Errorf("%s in %s calls fs.Parse but never looks at fs.NArg or fs.Args; "+
					"an unexpected positional argument would be ignored instead of reported",
					function.Name.Name, name)
			}
		}
	}

	// A scan that matched nothing would pass forever without checking anything.
	if checked < 10 {
		t.Fatalf("only %d command entry points were scanned; the scan is not finding them", checked)
	}
}

func source(t *testing.T, fileSet *token.FileSet, node ast.Node) string {
	t.Helper()
	position := fileSet.Position(node.Pos())
	end := fileSet.Position(node.End())
	body, err := os.ReadFile(filepath.Clean(position.Filename))
	if err != nil {
		t.Fatal(err)
	}
	return string(body[position.Offset:end.Offset])
}
