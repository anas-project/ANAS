package application_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/anas-project/ANAS/"

// CONSOLE-R-001, CONSOLE-R-184.
//
// The console's layering rule is a direction, not a directory: the shared use
// cases and their ports live in internal/application, and adapters depend on
// them. internal/runner is the CLI adapter and still hosts several service
// implementations, which is recorded debt (requirement §3.2). What must never
// happen is the arrow reversing -- once the shared layer imports the CLI
// package, the CLI stops being an adapter and anasd inherits its behaviour by
// construction rather than by choice.
//
// The daemon's own packages are held to the same rule so a new HTTP route
// cannot reach a CLI code path directly instead of through a typed service.
func TestSharedLayerAndDaemonAdaptersNeverImportTheCLIPackage(t *testing.T) {
	root := repositoryRoot(t)
	guarded := []string{
		"internal/application",
		"internal/api/httpapi",
		"internal/jobexecutor",
		"internal/deployment",
		"internal/configschema",
		"internal/securefs",
	}
	forbidden := modulePath + "internal/runner"
	for _, pkg := range guarded {
		for file, imports := range packageImports(t, root, pkg) {
			for _, imported := range imports {
				if imported == forbidden || strings.HasPrefix(imported, forbidden+"/") {
					t.Errorf("%s imports %s;\n"+
						"the shared layer and the daemon's adapters must depend on internal/application, never on the CLI package.\n"+
						"Move the use case into internal/application and let internal/runner call it.",
						file, imported)
				}
			}
		}
	}
}

// The CLI must reach shared behaviour through the same package the daemon uses,
// which is what keeps CONSOLE-R-003 (unchanged CLI output) meaningful: both
// adapters render one service's results.
func TestCLIAdapterDependsOnTheSharedLayer(t *testing.T) {
	root := repositoryRoot(t)
	found := false
	for _, imports := range packageImports(t, root, "internal/runner") {
		for _, imported := range imports {
			if imported == modulePath+"internal/application" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("internal/runner no longer imports internal/application; the CLI must consume the shared service layer")
	}
}

func packageImports(t *testing.T, root, dir string) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, filepath.Join(root, dir), func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	result := map[string][]string{}
	for _, pkg := range packages {
		for name, file := range pkg.Files {
			key := dir + "/" + filepath.Base(name)
			for _, imported := range file.Imports {
				path, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					continue
				}
				result[key] = append(result[key], path)
			}
			sort.Strings(result[key])
		}
	}
	return result
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}
