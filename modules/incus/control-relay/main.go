// Incus control relay is a non-root, fixed-destination transport component.
// Installation, network authorization and service lifecycle belong to the
// separately reviewed host action channel; this program performs none of them.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runRelayCommand(ctx, os.Args[1:], os.Stdout); err != nil {
		// Errors from configuration/network operations are deliberately stable;
		// paths, endpoint values, TLS bytes and credentials are never echoed.
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func runRelayCommand(ctx context.Context, arguments []string, output io.Writer) error {
	if ctx == nil || output == nil {
		return errConfiguration
	}
	flags := flag.NewFlagSet("anas-incus-control-relay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configurationPath := flags.String("config", "/etc/anas/incus-control-relay.json", "root-owned installation configuration")
	err := flags.Parse(arguments)
	if errors.Is(err, flag.ErrHelp) {
		_, err := fmt.Fprintln(output, "Usage: anas-incus-control-relay --config /etc/anas/incus-control-relay.json\nNon-root fixed TCP relay to 127.0.0.1:8443. Requires a separately authorized host installation; not installed or enabled automatically.")
		return err
	}
	if err != nil || flags.NArg() != 0 {
		return errConfiguration
	}
	if ctx.Err() != nil {
		return nil
	}
	body, err := readRelayConfiguration(*configurationPath)
	if err != nil {
		return err
	}
	settings, err := decodeRelayConfiguration(body)
	if err != nil {
		return err
	}
	return serveRelay(ctx, settings)
}
