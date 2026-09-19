// anas-hostd handles one preinstalled systemd activation. It does not install
// itself, accept arbitrary paths/commands, or own a separate job journal.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/anas-project/ANAS/internal/buildinfo"
	"github.com/anas-project/ANAS/internal/hostaction"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, hostaction.ServeInstalledActivation)
	stop()
	os.Exit(code)
}
func run(ctx context.Context, args []string, out, diagnostic io.Writer, serve func(context.Context) error) int {
	if len(args) != 1 {
		fmt.Fprintln(diagnostic, "usage: anas-hostd --serve | --actions | --version")
		return 2
	}
	switch args[0] {
	case "--help", "-h":
		fmt.Fprintln(out, "usage: anas-hostd --serve | --actions | --version")
		return 0
	case "--version":
		if json.NewEncoder(out).Encode(map[string]string{"version": buildinfo.Version, "commit": buildinfo.Commit}) != nil {
			return 1
		}
		return 0
	case "--actions":
		if json.NewEncoder(out).Encode(map[string]any{"source": "compiled-host", "installation_verified": false, "version": buildinfo.Version, "commit": buildinfo.Commit, "actions": hostaction.Catalog()}) != nil {
			return 1
		}
		return 0
	case "--serve":
		if ctx == nil || serve == nil || serve(ctx) != nil {
			fmt.Fprintln(diagnostic, "host action execution could not be confirmed")
			return 1
		}
		return 0
	default:
		fmt.Fprintln(diagnostic, "unsupported host action executable option")
		return 2
	}
}
