// incus-host-preflight is an unprivileged developer diagnostic. It is not the
// production `anas host` API, a root helper, an installer or an action executor.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/incushost"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, out, diagnostic io.Writer) int {
	flags := flag.NewFlagSet("incus-host-preflight", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	iface := flags.String("interface", "incus_container", "explicit isolation tier")
	skip := flags.Bool("skip", false, "keep compute disabled")
	recipes := flags.Bool("recipes", false, "print compiled recipe metadata, without host reads")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprintln(diagnostic, "Usage: incus-host-preflight [--interface incus_container|incus_vm] [--skip] | --recipes\nRead-only metadata inspection. Does not install, start, connect to or configure Incus.")
			return 0
		}
		fmt.Fprintln(diagnostic, "invalid preflight arguments")
		return 2
	}
	if ctx == nil || out == nil || flags.NArg() != 0 || (*iface != "incus_container" && *iface != "incus_vm") || (*recipes && (flags.NFlag() != 1)) {
		fmt.Fprintln(diagnostic, "invalid preflight arguments")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		fmt.Fprintln(diagnostic, "host preflight cancelled; compute remains disabled")
		return 1
	}
	var value any
	var err error
	if *recipes {
		value, err = incushost.Recipes()
	} else {
		value, err = incushost.LocalPreflight(ctx, incushost.Options{Interface: *iface, Skip: *skip})
	}
	if err != nil {
		fmt.Fprintln(diagnostic, "host preflight unavailable; compute remains disabled")
		return 1
	}
	if json.NewEncoder(out).Encode(value) != nil {
		fmt.Fprintln(diagnostic, "preflight output failed")
		return 1
	}
	return 0
}
