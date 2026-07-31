package main

import (
	"fmt"
	"log"
	"os"

	"github.com/whlsxl/anas/internal/runner"
)

func main() {
	if err := runner.Main(os.Args[1:]); err != nil {
		// A command that speaks the JSON contract has already written its
		// failure document to stdout. Printing the message again on stderr
		// would be harmless but repeating it here would also mean the exit
		// code came from log.Fatal rather than from the contract's table.
		if !runner.Reported(err) {
			log.SetFlags(0)
			log.Printf("anas: %v", err)
		}
		os.Exit(runner.ExitCode(err))
	}
	fmt.Fprintln(os.Stderr, "done")
}
