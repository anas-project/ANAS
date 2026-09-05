package main

import (
	"go/ast"
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

// CONSOLE-R-021, CONSOLE-R-146, CONSOLE-R-147.
//
// Every external command a request can reach must run under a cancellable
// context with an explicitly constructed environment. The daemon-safe paths
// already do, but the CLI-only variants still exist in the same packages and
// are selected by a runtime flag, so a new call site that forgets the flag
// compiles, passes review, and silently reintroduces os.Environ() inheritance.
//
// This gate turns that into a build failure. It walks the import graph from
// cmd/anasd, finds every exec.Command (the form without a context) and
// os.Environ call in the reachable packages, and compares them to the inventory
// below. Adding a route that pulls in a new package widens the graph
// automatically; adding a call site anywhere in it fails until the inventory
// records why the site cannot serve a request.
//
// Entries are declaration-scoped rather than line-scoped so ordinary edits do
// not churn the inventory. Add one only with a reason that holds for the
// daemon, never to make the test pass.
var daemonReachableSubprocessInventory = map[string][]string{
	// Compose keeps a CLI pair and a daemon pair side by side. runCompose and
	// outputCompose select between them; only the *Context variants take a
	// context and an explicit environment.
	"internal/compose/compose.go": {
		"(CLI).fileCommand exec.Command",
		"(CLI).fileCommand os.Environ",
		"(CLI).outputFile exec.Command",
		"(CLI).outputFile os.Environ",
		"Detect exec.Command",
	},
	// The compute client's CLI runner. Daemon callers go through the context
	// runner with a constructed environment.
	"internal/computeclient/client.go": {"(execRunner).Run os.Environ"},
	// Reads the operator's macOS locale for CLI text. Never reached by a
	// request: the daemon resolves locale from its own service configuration.
	"internal/localization/localization.go": {"appleLocale exec.Command"},
	// Module packaging shells out to go and git. It is a developer and release
	// tool invoked from the CLI; no HTTP route builds a module package.
	"internal/modulepackage/package.go": {
		"Build exec.Command",
		"Build os.Environ",
		"buildCommandExecutors exec.Command",
		"buildCommandExecutors os.Environ",
		"gitOutput exec.Command",
		"runtimeTrackedFiles exec.Command",
	},
	// stty needs a real controlling TTY, which a daemon does not have. The
	// rotate API only ever generates a random password (CONSOLE-R-047).
	"internal/runner/admin_cli.go": {"newLocalAdminPassword exec.Command"},
	// Backup execution is terminal-only: §4.2 registers no backup create,
	// restore, or verify write route, and MaintenanceService exposes none.
	"internal/runner/backup_transfer.go": {
		"copyDirectory exec.Command",
		"sendSubvolumeInto exec.Command",
		"sendSubvolumeToFile exec.Command",
		"var btrfsStdinCommand exec.Command",
	},
	// The CLI half of the ownership probe. Daemon callers use
	// dockerComposeProjectOwnersContext.
	"internal/runner/compose_scope.go": {
		"dockerComposeProjectOwners exec.Command",
		"dockerComposeProjectOwners os.Environ",
	},
	// Snapshot restore and delete are terminal-only. The snapshot routes the
	// daemon does serve go through runSnapshotBtrfs, which branches on the
	// restricted flag before reaching this variable.
	"internal/runner/deployment.go": {"var btrfsCommand exec.Command"},
	// Host network setup runs during anas init and macvlan provisioning, both
	// terminal operations; the daemon refuses sudo paths instead
	// (CONSOLE-R-151).
	"internal/runner/hostnet.go":   {"defaultRoute exec.Command"},
	"internal/runner/mountinfo.go": {"reflinkProbe exec.Command"},
	"internal/runner/network.go": {
		"ensureMacvlan exec.Command",
		"inspectMacvlan exec.Command",
		"probeCommand exec.Command",
		"removeMacvlan exec.Command",
		"runHostNetHelper exec.Command",
	},
	// Building a module command binary is a developer path; the daemon
	// consumes prebuilt executors.
	"internal/runner/module_commands.go": {
		"(*app).ensureModuleCommandBinary exec.Command",
		"(*app).ensureModuleCommandBinary os.Environ",
	},
	// The single switch that decides inheritance. Its os.Environ call is the
	// CLI branch; the restricted branch builds the environment from an
	// allowlist and drops PATH, HOME, and LANG coming from workspace values.
	"internal/runner/process_environment.go": {"(*app).commandEnvironment os.Environ"},
	// CLI tree copy. copyDeploymentTree passes a context and explicit
	// environment for daemon callers.
	"internal/runner/snapshot.go": {"copyDeploymentTreeContext exec.Command"},
}

func TestDaemonReachableSubprocessInventory(t *testing.T) {
	root := repositoryRoot(t)
	packages := daemonReachablePackages(t, root)
	if len(packages) < 20 {
		t.Fatalf("walked %d packages from cmd/anasd, want the full daemon import graph", len(packages))
	}
	found := map[string][]string{}
	for _, pkg := range packages {
		for file, sites := range unguardedSubprocessSites(t, root, pkg) {
			found[file] = append(found[file], sites...)
		}
	}
	for file := range found {
		sort.Strings(found[file])
	}
	expected := map[string][]string{}
	for file, sites := range daemonReachableSubprocessInventory {
		copied := append([]string(nil), sites...)
		sort.Strings(copied)
		expected[file] = copied
	}
	for file, sites := range found {
		want, ok := expected[file]
		if !ok {
			t.Errorf("%s reaches the daemon and calls exec.Command/os.Environ at %v, but is not in the inventory;\n"+
				"use exec.CommandContext with an explicit environment, or add the file with a reason it cannot serve a request",
				file, sites)
			continue
		}
		for _, site := range sites {
			if !contains(want, site) {
				t.Errorf("%s: new daemon-reachable call site %q is not in the inventory;\n"+
					"use exec.CommandContext with an explicit environment, or record why this declaration is CLI-only",
					file, site)
			}
		}
	}
	for file, sites := range expected {
		got, ok := found[file]
		if !ok {
			t.Errorf("inventory lists %s, but it is no longer daemon-reachable or no longer calls exec.Command/os.Environ; remove the entry", file)
			continue
		}
		for _, site := range sites {
			if !contains(got, site) {
				t.Errorf("inventory lists %s %q, which no longer exists; remove the entry", file, site)
			}
		}
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

const modulePath = "github.com/anas-project/ANAS/"

// daemonReachablePackages walks first-party imports from cmd/anasd. Resolving
// them by path keeps the gate hermetic: it needs no build cache, no network,
// and no go list subprocess of its own.
func daemonReachablePackages(t *testing.T, root string) []string {
	t.Helper()
	seen := map[string]bool{}
	var walk func(string)
	walk = func(dir string) {
		if seen[dir] {
			return
		}
		seen[dir] = true
		for _, file := range parsePackageDir(t, root, dir, parser.ImportsOnly) {
			for _, imported := range file.Imports {
				path, err := strconv.Unquote(imported.Path.Value)
				if err != nil || !strings.HasPrefix(path, modulePath) {
					continue
				}
				walk(strings.TrimPrefix(path, modulePath))
			}
		}
	}
	walk("cmd/anasd")
	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

func unguardedSubprocessSites(t *testing.T, root, dir string) map[string][]string {
	t.Helper()
	sites := map[string][]string{}
	for name, file := range parsePackageDirNamed(t, root, dir) {
		key := dir + "/" + filepath.Base(name)
		for _, decl := range file.Decls {
			owner := declarationName(decl)
			seen := map[string]bool{}
			ast.Inspect(decl, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				call := pkg.Name + "." + selector.Sel.Name
				if call != "exec.Command" && call != "os.Environ" {
					return true
				}
				site := owner + " " + call
				if !seen[site] {
					seen[site] = true
					sites[key] = append(sites[key], site)
				}
				return true
			})
		}
	}
	return sites
}

func declarationName(decl ast.Decl) string {
	switch node := decl.(type) {
	case *ast.FuncDecl:
		if node.Recv != nil && len(node.Recv.List) == 1 {
			return "(" + typeName(node.Recv.List[0].Type) + ")." + node.Name.Name
		}
		return node.Name.Name
	case *ast.GenDecl:
		for _, spec := range node.Specs {
			if value, ok := spec.(*ast.ValueSpec); ok && len(value.Names) > 0 {
				return "var " + value.Names[0].Name
			}
		}
	}
	return "unnamed declaration"
}

func typeName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.StarExpr:
		return "*" + typeName(node.X)
	case *ast.Ident:
		return node.Name
	}
	return "?"
}

func parsePackageDir(t *testing.T, root, dir string, mode parser.Mode) []*ast.File {
	t.Helper()
	files := []*ast.File{}
	for _, file := range parsePackageDirMode(t, root, dir, mode) {
		files = append(files, file)
	}
	return files
}

func parsePackageDirNamed(t *testing.T, root, dir string) map[string]*ast.File {
	t.Helper()
	return parsePackageDirMode(t, root, dir, 0)
}

func parsePackageDirMode(t *testing.T, root, dir string, mode parser.Mode) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, filepath.Join(root, dir), func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, mode)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	files := map[string]*ast.File{}
	for _, pkg := range packages {
		for name, file := range pkg.Files {
			files[name] = file
		}
	}
	return files
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}
