// Command gen-dns-registry writes each engine's slice of the DNS platform
// registry into its module bundle. Run it after editing
// internal/dns/providers.yml; TestProjectionsMatchCommittedFiles fails until
// you do.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/dns"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	reg, err := dns.Load()
	if err != nil {
		fail(err)
	}
	projections, err := reg.Projections()
	if err != nil {
		fail(err)
	}
	for _, projection := range projections {
		path := filepath.Join(root, projection.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(path, projection.Source, 0644); err != nil {
			fail(err)
		}
		fmt.Printf("wrote %s (%s)\n", projection.Path, projection.Engine)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gen-dns-registry:", err)
	os.Exit(1)
}
