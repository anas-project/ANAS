package main

import (
	"context"
	"crypto/tls"
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
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/consolelistener"
	"github.com/anas-project/ANAS/internal/consolestate"
	"github.com/anas-project/ANAS/internal/consoletls"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
	"github.com/anas-project/ANAS/internal/jobexecutor"
	"github.com/anas-project/ANAS/internal/runner"
)

const defaultServiceConfigPath = "/etc/anas/anasd.yml"

const consoleBootstrapTransactionID = "bootstrap"

type options struct {
	configPath string
}

func main() {
	logger := log.New(os.Stderr, "anasd: ", log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(os.Stdout, "Usage: anasd [--config %s]\n\nThe service configuration must be an absolute root-owned file with mode 0600 or stricter.\n", defaultServiceConfigPath)
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
	config, err := consoleconfig.Load(opts.configPath, consoleconfig.RootOwnedFilePolicy())
	if err != nil {
		return err
	}
	return runConfigured(ctx, config, logger)
}

func parseOptions(args []string) (options, error) {
	opts := options{configPath: defaultServiceConfigPath}
	flags := flag.NewFlagSet("anasd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.configPath, "config", opts.configPath, "absolute root-owned service configuration path")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if opts.configPath == "" || opts.configPath[0] != '/' {
		return options{}, errors.New("--config must be an absolute path")
	}
	return opts, nil
}

func runConfigured(ctx context.Context, config consoleconfig.Config, logger *log.Logger) error {
	return runConfiguredWithListener(ctx, config, logger, nil)
}

func runConfiguredWithListener(ctx context.Context, config consoleconfig.Config, logger *log.Logger, listen consolelistener.ListenFunc) error {
	workspaces := make([]httpapi.Workspace, len(config.Workspaces))
	for index, workspace := range config.Workspaces {
		workspaces[index] = httpapi.Workspace{ID: workspace.ID, Path: workspace.Path}
	}
	registry, err := httpapi.NewRegistry(workspaces)
	if err != nil {
		return fmt.Errorf("configure workspaces: %w", err)
	}

	// Audit, capability state, authentication, and durable jobs are deliberately
	// initialized before any wildcard socket is opened. Security-sensitive
	// operations fail closed when durable state or the audit writer is unavailable.
	auditWriter, err := audit.Open(config.ConsoleStore)
	if err != nil {
		return fmt.Errorf("initialize console audit: %w", err)
	}
	defer auditWriter.Close()
	leaseContext, cancelLease := context.WithTimeout(ctx, consolejobs.DefaultLockTimeout)
	executionLease, err := consolejobs.AcquireExecutionLease(leaseContext, config.ConsoleStore)
	cancelLease()
	if err != nil {
		return fmt.Errorf("acquire console job execution lease: %w", err)
	}
	defer executionLease.Close()
	stateStore, err := consolestate.Open(ctx, config.ConsoleStore, consoleaudit.StateSink{Writer: auditWriter})
	if err != nil {
		return fmt.Errorf("initialize console capability state: %w", err)
	}
	currentAuthState := func(ctx context.Context) (consoleauth.ConsoleState, error) {
		state, err := stateStore.Current(ctx)
		if err != nil {
			return "", err
		}
		return authCapabilityState(state)
	}
	authStore, err := consoleauth.Open(config.ConsoleStore, consoleaudit.AuthSink{Writer: auditWriter, Actor: "direct-client"}, consoleauth.StoreOptions{
		CurrentState: currentAuthState,
	})
	if err != nil {
		return fmt.Errorf("initialize console authentication: %w", err)
	}
	if err := authStore.RecoverAdvanceToEnrollment(ctx, currentAuthState); err != nil {
		return fmt.Errorf("recover bootstrap enrollment transaction: %w", err)
	}
	if err := authStore.RecoverInitialOwnerCommit(ctx, currentAuthState); err != nil {
		return fmt.Errorf("recover owner enrollment transaction: %w", err)
	}
	jobOpenContext, cancelJobOpen := context.WithTimeout(ctx, consolejobs.DefaultLockTimeout)
	jobStore, err := consolejobs.OpenContext(jobOpenContext, config.ConsoleStore, consolejobs.Options{})
	cancelJobOpen()
	if err != nil {
		return fmt.Errorf("initialize console jobs: %w", err)
	}
	defer jobStore.Close()
	deploymentAudit := deploymentAuditSink{writer: auditWriter, logger: logger}
	jobRecoveryContext, cancelJobRecovery := context.WithTimeout(ctx, consolejobs.DefaultLockTimeout)
	err = jobStore.RecoverInterruptedJobsObserved(jobRecoveryContext, executionLease, deploymentaudit.ObserveJobCommit(deploymentAudit, deploymentaudit.Event{
		Stage: deploymentaudit.StageJobInterruptedAuthorized, FailureCode: "daemon_restarted",
	}))
	cancelJobRecovery()
	if err != nil {
		return fmt.Errorf("recover interrupted console jobs: %w", err)
	}
	executorWorkspaces := make([]jobexecutor.Workspace, len(config.Workspaces))
	for index, workspace := range config.Workspaces {
		executorWorkspaces[index] = jobexecutor.Workspace{ID: workspace.ID, Path: workspace.Path}
	}
	executor, err := jobexecutor.New(jobexecutor.Options{
		Store: jobStore, Audit: deploymentAudit, Workspaces: executorWorkspaces,
		DeploymentFactory: runner.NewWorkspaceDeploymentServiceWithEvents,
		ModuleFactory:     runner.NewWorkspaceModuleManagementService,
		OnError: func(err error) {
			if logger != nil {
				logger.Printf("console job executor: %v", err)
			}
		},
	})
	if err != nil {
		return fmt.Errorf("configure console job executor: %w", err)
	}
	executorContext, cancelExecutor := context.WithCancel(ctx)
	executorDone := make(chan error, 1)
	go func() { executorDone <- executor.Run(executorContext) }()
	defer func() {
		cancelExecutor()
		if executorErr := <-executorDone; executorErr != nil && logger != nil {
			logger.Printf("console job executor stopped: %v", executorErr)
		}
	}()

	tlsManager, err := newTLSManager(config.TLS, logger)
	if err != nil {
		return fmt.Errorf("configure console TLS: %w", err)
	}
	enrollmentOrigin, enrollmentOriginErr := configuredEnrollmentOrigin(config)
	currentEnrollmentTarget := func(ctx context.Context) (httpapi.EnrollmentTarget, error) {
		if enrollmentOriginErr != nil {
			return httpapi.EnrollmentTarget{}, enrollmentOriginErr
		}
		return currentLegoEnrollmentTarget(ctx, tlsManager, enrollmentOrigin)
	}
	if enrollmentOriginErr == nil {
		advanced, err := advanceToEnrollmentIfReady(ctx, stateStore, authStore, currentEnrollmentTarget)
		if err != nil {
			return fmt.Errorf("advance console to enrollment: %w", err)
		}
		if advanced && logger != nil {
			logger.Printf("validated lego certificate detected; console entered enrollment at %s", enrollmentOrigin)
		}
	}

	hostMode, err := directHostMode(config.Mode)
	if err != nil {
		return err
	}
	hostPolicy := httpapi.DirectHostPolicy{Mode: hostMode, AllowedDNSHosts: config.AllowedDNSHosts}
	queryFactory := func(workspacePath string) httpapi.QueryService {
		return runner.NewWorkspaceQueryService(workspacePath)
	}
	stateProvider := func(ctx context.Context) (httpapi.ConsoleState, error) {
		state, err := stateStore.Current(ctx)
		if err != nil {
			return "", err
		}
		return httpCapabilityState(state)
	}
	enrollmentOptions := httpapi.EnrollmentOptions{
		Workflow:      authStore,
		CurrentTarget: currentEnrollmentTarget,
		CurrentConnectionTarget: func(ctx context.Context) (httpapi.EnrollmentTarget, error) {
			if enrollmentOriginErr != nil {
				return httpapi.EnrollmentTarget{}, enrollmentOriginErr
			}
			return selectedConnectionEnrollmentTarget(ctx, enrollmentOrigin)
		},
		CompleteTransition: func(ctx context.Context, _ string) error {
			_, err := stateStore.Transition(ctx, consolestate.StateFull, "enrollment-owner")
			return err
		},
	}
	jobOptions := httpapi.JobQueryOptions{Store: jobStore, Cancel: executor.Cancel}
	configOptions := httpapi.ConfigOptions{Factory: runner.NewWorkspaceConfigService, Audit: configAuditSink{writer: auditWriter, logger: logger}}
	deploymentOptions := httpapi.DeploymentOptions{
		PlanFactory: runner.NewWorkspaceDeploymentPlanService, ServiceFactory: runner.NewWorkspaceDeploymentService,
		ModuleFactory: runner.NewWorkspaceModuleManagementService,
		Store:         jobStore, Audit: deploymentAudit,
		StepUp: authStore, Notify: executor.Notify,
	}
	auditOptions := httpapi.AuditQueryOptions{Store: auditWriter}
	consoleStatus, err := runner.ConfiguredConsoleStatus(config, net.InterfaceAddrs)
	if err != nil {
		return fmt.Errorf("discover direct management addresses: %w", err)
	}
	systemOptions := httpapi.SystemOptions{
		CurrentCertificate: func(ctx context.Context) (httpapi.CertificateMaterial, error) {
			return currentConsoleCertificate(ctx, tlsManager)
		},
		CanonicalHTTPSOrigin: func() string {
			if enrollmentOriginErr != nil {
				return ""
			}
			return enrollmentOrigin
		}(),
		DirectRecoveryURLs: consoleStatus.DirectRecoveryURLs,
		ProxyURL:           consoleStatus.ProxyURL,
	}
	handler, err := httpapi.NewHandlerWithEnrollmentJobsConfigAndDeployment(registry, queryFactory, httpapi.SecurityOptions{
		State:       stateProvider,
		HostAllowed: hostPolicy.Allowed,
		Listener:    httpapi.ListenerDirect,
		Authorize:   httpapi.DirectSessionAuthorizer(authStore),
	}, authStore, enrollmentOptions, jobOptions, configOptions, deploymentOptions, auditOptions, systemOptions)
	if err != nil {
		return fmt.Errorf("configure HTTP routes: %w", err)
	}
	tlsConfig := dynamicTLSConfig(tlsManager)
	var proxyHandler http.Handler
	var proxyTLSConfig *tls.Config
	if config.TrustedProxy != nil {
		authorizeProxy, authErr := httpapi.TrustedProxyAuthorizer(httpapi.TrustedProxyOptions{
			Authenticator: authStore, ExpectedIssuer: config.TrustedProxy.OIDCIssuer,
			ExpectedDirectoryGroup: config.TrustedProxy.PlatformAdminGroup,
		})
		if authErr != nil {
			return fmt.Errorf("configure trusted proxy authentication: %w", authErr)
		}
		proxyHostPolicy := httpapi.ExactDNSHostPolicy{AllowedDNSHosts: config.TrustedProxy.AllowedDNSHosts}
		proxyHandler, err = httpapi.NewHandlerWithEnrollmentJobsConfigAndDeployment(registry, queryFactory, httpapi.SecurityOptions{
			State: stateProvider, HostAllowed: proxyHostPolicy.Allowed,
			Listener: httpapi.ListenerTrustedProxy, Authorize: authorizeProxy,
		}, authStore, enrollmentOptions, jobOptions, configOptions, deploymentOptions, auditOptions, systemOptions)
		if err != nil {
			return fmt.Errorf("configure trusted proxy HTTP routes: %w", err)
		}
		proxyTLSConfig, err = newTrustedProxyTLSConfig(tlsConfig, *config.TrustedProxy)
		if err != nil {
			return fmt.Errorf("configure trusted proxy TLS: %w", err)
		}
	}

	specs, err := consolelistener.Specs(config.Mode, config.Port)
	if err != nil {
		return err
	}
	listeners, err := consolelistener.ListenAll(ctx, specs, listen)
	if err != nil {
		return err
	}
	if logger != nil {
		for _, listener := range listeners {
			logger.Printf("direct management listener %s (%s mode; plaintext bootstrap and TLS share this port)", listener.Addr(), config.Mode)
		}
	}
	if enrollmentOriginErr == nil {
		go monitorEnrollmentCertificate(ctx, stateStore, authStore, currentEnrollmentTarget, logger)
	}
	if config.TrustedProxy == nil {
		return serveConsolePort(ctx, listeners, handler, tlsConfig, logger)
	}
	proxyNetwork := "tcp6"
	if net.ParseIP(config.TrustedProxy.BindAddress).To4() != nil {
		proxyNetwork = "tcp4"
	}
	proxyListeners, err := consolelistener.ListenAll(ctx, []consolelistener.Spec{{
		Network: proxyNetwork, Address: net.JoinHostPort(config.TrustedProxy.BindAddress, strconv.Itoa(config.TrustedProxy.Port)),
	}}, listen)
	if err != nil {
		closeRootListeners(listeners)
		return err
	}
	proxyListener := proxyListeners[0]
	if logger != nil {
		logger.Printf("trusted proxy management listener %s (TLS-only mTLS)", proxyListener.Addr())
	}
	serviceContext, cancelServices := context.WithCancel(ctx)
	defer cancelServices()
	results := make(chan error, 2)
	go func() { results <- serveConsolePort(serviceContext, listeners, handler, tlsConfig, logger) }()
	go func() {
		results <- serveTrustedProxyTLS(serviceContext, proxyListener, proxyHandler, proxyTLSConfig, config.TrustedProxy.AllowedSourceIPs, logger)
	}()
	first := <-results
	cancelServices()
	second := <-results
	if first != nil {
		return first
	}
	return second
}

func advanceToEnrollmentIfReady(
	ctx context.Context,
	stateStore *consolestate.Store,
	authStore *consoleauth.Store,
	currentTarget func(context.Context) (httpapi.EnrollmentTarget, error),
) (bool, error) {
	if currentTarget == nil {
		return false, errors.New("enrollment target provider is required")
	}
	state, err := stateStore.Current(ctx)
	if err != nil {
		return false, err
	}
	if state != consolestate.StateBootstrap {
		return false, nil
	}
	if _, err := currentTarget(ctx); err != nil {
		return false, nil
	}
	transactionID := consoleBootstrapTransactionID
	if current, err := authStore.CurrentBootstrapTransaction(ctx, consoleauth.StateBootstrap); err == nil {
		transactionID = current
	} else if !errors.Is(err, consoleauth.ErrSessionUnauthorized) && !errors.Is(err, consoleauth.ErrCredentialExpired) {
		return false, err
	}
	err = authStore.AdvanceToEnrollment(ctx, transactionID, consoleauth.EnrollmentRecoveryRoutePatterns(), func(ctx context.Context) error {
		_, err := stateStore.Transition(ctx, consolestate.StateEnrollment, "lego-certificate-monitor")
		return err
	})
	return err == nil, err
}

func monitorEnrollmentCertificate(
	ctx context.Context,
	stateStore *consolestate.Store,
	authStore *consoleauth.Store,
	currentTarget func(context.Context) (httpapi.EnrollmentTarget, error),
	logger *log.Logger,
) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state, err := stateStore.Current(ctx)
			if err != nil {
				if logger != nil {
					logger.Printf("read console state for certificate monitor: %v", err)
				}
				return
			}
			if state != consolestate.StateBootstrap {
				return
			}
			advanced, err := advanceToEnrollmentIfReady(ctx, stateStore, authStore, currentTarget)
			if err != nil {
				if logger != nil {
					logger.Printf("console enrollment transition failed: %v", err)
				}
				return
			}
			if advanced {
				if logger != nil {
					logger.Printf("validated lego certificate detected; console entered enrollment")
				}
				return
			}
		}
	}
}

func httpCapabilityState(state consolestate.State) (httpapi.ConsoleState, error) {
	switch state {
	case consolestate.StateBootstrap:
		return httpapi.StateBootstrap, nil
	case consolestate.StateEnrollment:
		return httpapi.StateEnrollment, nil
	case consolestate.StateFull:
		return httpapi.StateFull, nil
	default:
		return "", fmt.Errorf("unsupported persisted console state %q", state)
	}
}

func authCapabilityState(state consolestate.State) (consoleauth.ConsoleState, error) {
	switch state {
	case consolestate.StateBootstrap:
		return consoleauth.StateBootstrap, nil
	case consolestate.StateEnrollment:
		return consoleauth.StateEnrollment, nil
	case consolestate.StateFull:
		return consoleauth.StateFull, nil
	default:
		return "", fmt.Errorf("unsupported persisted console state %q", state)
	}
}

func directHostMode(mode consoleconfig.Mode) (httpapi.DirectMode, error) {
	switch mode {
	case consoleconfig.ModeLAN:
		return httpapi.DirectModeLAN, nil
	case consoleconfig.ModeLoopback:
		return httpapi.DirectModeLoopback, nil
	default:
		return "", fmt.Errorf("unsupported direct-listener mode %q", mode)
	}
}

func newTLSManager(config consoleconfig.TLSConfig, logger *log.Logger) (*consoletls.Manager, error) {
	options := consoletls.Options{
		CheckFile: consoletls.RootOwnedFileSecurityCheck,
		OnReloadError: func(err error) {
			if logger != nil {
				logger.Printf("console TLS reload failed; a previously validated certificate remains available: %v", err)
			}
		},
	}
	if config.Lego != nil {
		options.Lego = &consoletls.Candidate{
			CertificatePath:  config.Lego.Certificate,
			PrivateKeyPath:   config.Lego.PrivateKey,
			IssuerPath:       config.Lego.Issuer,
			TrustBundlePath:  config.Lego.TrustBundle,
			InternalCAPath:   config.Lego.InternalCA,
			IssuerMarkerPath: config.Lego.IssuerMark,
			BaseDomain:       config.Lego.BaseDomain,
		}
	}
	if config.Temporary != nil {
		options.Temporary = &consoletls.Candidate{
			CertificatePath:     config.Temporary.Certificate,
			PrivateKeyPath:      config.Temporary.PrivateKey,
			IssuerPath:          config.Temporary.Certificate,
			TrustBundlePath:     config.Temporary.Certificate,
			Source:              consoletls.SourceTemporary,
			RequiredDNSNames:    config.Temporary.DNSNames,
			RequiredIPAddresses: config.Temporary.IPAddresses,
		}
	}
	if options.Lego == nil && options.Temporary == nil {
		return nil, nil
	}
	manager, err := consoletls.NewManager(options)
	if err != nil {
		return nil, err
	}
	if err := manager.Reload(); err != nil && logger != nil {
		if current, ok := manager.Current(); ok {
			logger.Printf("preferred console TLS certificate is unavailable; starting with validated %s material: %v", current.Source(), err)
		} else {
			logger.Printf("no validated console TLS certificate is available yet: %v", err)
		}
	}
	return manager, nil
}

func configuredEnrollmentOrigin(config consoleconfig.Config) (string, error) {
	if config.TLS.Lego == nil {
		return "", errors.New("lego TLS is not configured")
	}
	host := "anas." + config.TLS.Lego.BaseDomain
	if config.Port == 443 {
		return consoleauth.NormalizeOrigin("https://" + host)
	}
	return consoleauth.NormalizeOrigin("https://" + net.JoinHostPort(host, strconv.Itoa(config.Port)))
}

func currentLegoEnrollmentTarget(ctx context.Context, manager *consoletls.Manager, origin string) (httpapi.EnrollmentTarget, error) {
	if err := ctx.Err(); err != nil {
		return httpapi.EnrollmentTarget{}, err
	}
	if manager == nil {
		return httpapi.EnrollmentTarget{}, consoletls.ErrNoCertificate
	}
	reloadErr := manager.Reload()
	snapshot, ok := manager.Current()
	if !ok || snapshot.Source() != consoletls.SourceInternal && snapshot.Source() != consoletls.SourceACME {
		if reloadErr != nil {
			return httpapi.EnrollmentTarget{}, reloadErr
		}
		return httpapi.EnrollmentTarget{}, errors.New("validated lego certificate is not ready")
	}
	return httpapi.EnrollmentTarget{Origin: origin, SPKISHA256: snapshot.SPKISHA256Hex()}, nil
}

func currentConsoleCertificate(ctx context.Context, manager *consoletls.Manager) (httpapi.CertificateMaterial, error) {
	if err := ctx.Err(); err != nil {
		return httpapi.CertificateMaterial{}, err
	}
	if manager == nil {
		return httpapi.CertificateMaterial{Issuer: httpapi.CertificateIssuerNone}, nil
	}
	snapshot, ok := manager.Current()
	if !ok {
		return httpapi.CertificateMaterial{Issuer: httpapi.CertificateIssuerNone}, nil
	}
	material := httpapi.CertificateMaterial{InternalCAPEM: snapshot.InternalCAPEM()}
	switch snapshot.Source() {
	case consoletls.SourceInternal:
		material.Issuer = httpapi.CertificateIssuerInternal
	case consoletls.SourceACME:
		material.Issuer = httpapi.CertificateIssuerACME
	case consoletls.SourceTemporary:
		material.Issuer = httpapi.CertificateIssuerTemporary
	default:
		return httpapi.CertificateMaterial{}, fmt.Errorf("unsupported console certificate source %q", snapshot.Source())
	}
	return material, nil
}

func selectedConnectionEnrollmentTarget(ctx context.Context, origin string) (httpapi.EnrollmentTarget, error) {
	digest, err := selectedConnectionSPKI(ctx)
	if err != nil {
		return httpapi.EnrollmentTarget{}, err
	}
	return httpapi.EnrollmentTarget{Origin: origin, SPKISHA256: digest}, nil
}

func dynamicTLSConfig(manager *consoletls.Manager) *tls.Config {
	return &tls.Config{
		MinVersion:             tls.VersionTLS12,
		NextProtos:             []string{"http/1.1"},
		SessionTicketsDisabled: true,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if manager == nil {
				return nil, consoletls.ErrNoCertificate
			}
			certificate, err := manager.GetCertificate(hello)
			if err != nil {
				return nil, err
			}
			if err := recordSelectedCertificate(hello, certificate); err != nil {
				return nil, err
			}
			return certificate, nil
		},
	}
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
