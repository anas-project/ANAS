// Command package-module assembles one self-contained Module runtime bundle.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/modulepackage"
)

func main() {
	module := flag.String("module", "", "Module name from .github/modules.json")
	platform := flag.String("platform", modulepackage.DefaultPlatform(), "target platform (all, linux/amd64 or linux/arm64)")
	output := flag.String("output", "", "output .tar.gz path")
	catalog := flag.String("catalog", filepath.Join(".github", "modules.json"), "Module package catalog")
	repo := flag.String("repo", ".", "repository root")
	skipHook := flag.Bool("skip-hook-build", false, "omit the precompiled hook (validation only)")
	flag.Parse()
	if *module == "" || *output == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: package-module --module NAME [--platform all|linux/amd64|linux/arm64] --output FILE")
		os.Exit(2)
	}
	result, err := modulepackage.Build(modulepackage.BuildOptions{
		RepoRoot: *repo, CatalogPath: *catalog, Module: *module,
		Platform: *platform, OutputPath: *output, SkipHookBuild: *skipHook,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "package-module:", err)
		os.Exit(1)
	}
	body, _ := json.MarshalIndent(map[string]any{"path": result.Path, "package": result.Metadata}, "", "  ")
	fmt.Println(string(body))
}
