package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
)

const defaultListenAddress = "127.0.0.1:8080"

type options struct {
	listen     string
	workspaces []httpapi.Workspace
}

type repeatedValues []string

func (values *repeatedValues) String() string { return strings.Join(*values, ",") }

func (values *repeatedValues) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	logger := log.New(os.Stderr, "anasd: ", log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, `Usage: anasd [--listen 127.0.0.1:8080] [--workspace id=/absolute/path]...

M0 is read-only and unauthenticated, so --listen accepts loopback IP addresses only.
`)
			return
		}
		logger.Printf("%v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logger *log.Logger) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	registry, err := httpapi.NewRegistry(opts.workspaces)
	if err != nil {
		return fmt.Errorf("configure workspaces: %w", err)
	}
	handler := httpapi.NewHandler(registry, func(workspacePath string) httpapi.QueryService {
		return application.NewService(workspacePath)
	})
	server := newHTTPServer(opts.listen, handler)

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", opts.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.listen, err)
	}
	defer listener.Close()
	if logger != nil {
		logger.Printf("listening on http://%s", listener.Addr())
	}
	return serve(ctx, server, listener)
}

func parseOptions(args []string) (options, error) {
	opts := options{listen: defaultListenAddress}
	var workspaceValues repeatedValues
	flags := flag.NewFlagSet("anasd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.listen, "listen", opts.listen, "loopback listen address")
	flags.Var(&workspaceValues, "workspace", "registered workspace as id=/absolute/path (repeatable)")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if err := validateLoopbackAddress(opts.listen); err != nil {
		return options{}, err
	}
	for _, value := range workspaceValues {
		workspace, err := httpapi.ParseWorkspace(value)
		if err != nil {
			return options{}, fmt.Errorf("invalid --workspace %q: %w", value, err)
		}
		opts.workspaces = append(opts.workspaces, workspace)
	}
	return opts, nil
}

func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("listen address %q must be a loopback host and port", address)
	}
	ipHost := host
	if before, _, found := strings.Cut(host, "%"); found {
		ipHost = before
	}
	ip := net.ParseIP(ipHost)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q is not loopback; M0 has no authentication", address)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return fmt.Errorf("listen address %q must use a numeric port between 0 and 65535", address)
	}
	return nil
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func serve(ctx context.Context, server *http.Server, listener net.Listener) error {
	result := make(chan error, 1)
	go func() {
		result <- server.Serve(listener)
	}()

	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		err := <-result
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
