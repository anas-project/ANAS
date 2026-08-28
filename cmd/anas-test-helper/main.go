// Command anas-test-helper is the fixed, root-owned remote-test privilege
// boundary. It accepts named operations and validated scalar arguments only;
// it never evaluates a caller-provided shell command or path.
package main

// TEST_CASES: TESTAUTO-T-012

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/anas-project/ANAS/internal/remotetest"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "anas-test-helper: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: anas-test-helper preflight [validated requirements]")
	}
	if os.Geteuid() != 0 {
		return errors.New("must run through the configured non-interactive sudo rule")
	}
	switch args[0] {
	case "preflight":
		return runPreflight(args[1:])
	default:
		return fmt.Errorf("unknown operation %q; only preflight is available before the M3 lifecycle is installed", args[0])
	}
}

func runPreflight(args []string) error {
	flags := flag.NewFlagSet("preflight", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	runID := flags.String("run-id", "", "unique remote test run id")
	format := flags.String("format", "", "output format (json)")
	architecture := flags.String("architecture", "", "required architecture")
	minDisk := flags.Uint64("min-disk-bytes", 0, "minimum available disk")
	minMemory := flags.Uint64("min-memory-bytes", 0, "minimum available memory")
	var ports integerFlags
	var dns, routes, capabilities stringFlags
	flags.Var(&ports, "port", "required free port")
	flags.Var(&dns, "dns", "DNS name that must resolve")
	flags.Var(&routes, "route", "route or CIDR that must be reachable")
	flags.Var(&capabilities, "capability", "required target capability")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *format != "json" {
		return errors.New("preflight accepts only named flags and --format json")
	}
	requirements := remotetest.PreflightRequirements{
		Architecture: *architecture, MinDiskBytes: *minDisk, MinMemoryBytes: *minMemory,
		Ports: ports, DNSNames: dns, Routes: routes, Capabilities: capabilities,
	}
	if err := remotetest.ValidatePreflightRequirements(requirements); err != nil {
		return err
	}
	config, err := remotetest.LoadHelperConfig(remotetest.RemoteHelperConfigPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	facts, err := remotetest.CollectHostFacts(ctx, config, requirements, *runID)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(facts)
}

type stringFlags []string

func (values *stringFlags) String() string { return fmt.Sprint([]string(*values)) }
func (values *stringFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type integerFlags []int

func (values *integerFlags) String() string { return fmt.Sprint([]int(*values)) }
func (values *integerFlags) Set(value string) error {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	*values = append(*values, parsed)
	return nil
}
