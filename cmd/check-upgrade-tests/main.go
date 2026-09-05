package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/anas-project/ANAS/internal/upgradetest"
)

func main() {
	var catalogPath string
	var baseRef string
	var scopeList string
	var printModuleConfigs bool
	flag.StringVar(&catalogPath, "catalog", "test-env/upgrades/catalog.yml", "upgrade test catalog")
	flag.StringVar(&baseRef, "base-ref", "", "Git release base to compare with the worktree")
	flag.StringVar(&scopeList, "scope", "all", "comma-separated core,web,modules scopes")
	flag.BoolVar(&printModuleConfigs, "print-module-configs", false, "print validated Module suite configs, one per line")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("check-upgrade-tests accepts flags only")
	}
	root, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	scopes := map[string]bool{}
	for _, scope := range strings.Split(scopeList, ",") {
		scope = strings.TrimSpace(scope)
		switch scope {
		case "all", "core", "web", "modules":
			scopes[scope] = true
		case "":
		default:
			log.Fatalf("unknown scope %q", scope)
		}
	}
	result, err := upgradetest.Validate(upgradetest.Options{
		Root: root, CatalogPath: catalogPath, BaseRef: baseRef, Scopes: scopes,
	})
	if err != nil {
		log.Fatal(err)
	}
	if printModuleConfigs {
		for _, config := range result.ModuleConfigs {
			fmt.Println(config)
		}
		return
	}
	fmt.Printf("upgrade test catalog valid: %d products, %d modules, %d suites, %d transitions\n",
		result.Products, result.Modules, result.Suites, result.Transitions)
}
