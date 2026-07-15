package main

import (
	"fmt"
	"log"
	"os"

	"github.com/whlsxl/anas/internal/runner"
)

func main() {
	if err := runner.Main(os.Args[1:]); err != nil {
		log.SetFlags(0)
		log.Fatalf("anas: %v", err)
	}
	fmt.Fprintln(os.Stderr, "done")
}
