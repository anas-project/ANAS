package runner

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/consolestate"
	"github.com/anas-project/ANAS/internal/tempconsolecert"
)

const (
	defaultConsoleConfigPath      = "/etc/anas/anasd.yml"
	consoleBootstrapTransactionID = "bootstrap"
)

// consoleBootstrapRoutePatterns is shared by both issuance commands. Values
// are route-policy paths rather than request-specific IDs, so a session stays
// bound to the closed bootstrap surface as jobs and workspaces are selected.
func consoleBootstrapRoutePatterns() []string {
	return []string{
		"/api/v1/system",
		"/api/v1/system/ca",
		"/api/v1/auth/session",
		"/api/v1/workspaces",
		"/api/v1/catalog/modules",
		"/api/v1/workspaces/{ws}/modules",
		"/api/v1/workspaces/{ws}/config",
		"/api/v1/workspaces/{ws}/config/validate",
		"/api/v1/workspaces/{ws}/plans",
		"/api/v1/workspaces/{ws}/actions/apply",
		"/api/v1/jobs",
		"/api/v1/jobs/{id}",
		"/api/v1/jobs/{id}/events",
		"/api/v1/auth/enrollment/handoffs",
	}
}

func runConsole(args []string, jsonMode bool) error {
	return runConsoleWithPolicy(args, jsonMode, consoleconfig.RootOwnedFilePolicy())
}

func runConsoleWithPolicy(args []string, jsonMode bool, policy consoleconfig.FileSecurityPolicy) error {
	if len(args) == 0 {
		return usageErrorf("usage: anas console status | token | tls --self-signed [--config PATH] [--ttl DURATION]")
	}
	switch args[0] {
	case "status":
		return runConsoleStatus(args[1:], jsonMode, policy)
	case "token":
		return runConsoleToken(args[1:], jsonMode, policy)
	case "tls":
		return runConsoleTLS(args[1:], jsonMode, policy)
	default:
		return usageErrorf("usage: anas console status | token | tls --self-signed [--config PATH] [--ttl DURATION]")
	}
}

// ConsoleStatus describes the operator-visible direct recovery and trusted
// proxy entry points derived from the root-managed service configuration.
type ConsoleStatus struct {
	Mode               consoleconfig.Mode
	DirectRecoveryURLs []string
	ProxyURL           string
	BindsAllInterfaces bool
}

func runConsoleStatus(args []string, jsonMode bool, policy consoleconfig.FileSecurityPolicy) error {
	flags := flag.NewFlagSet("console status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", defaultConsoleConfigPath, "root-managed anasd service configuration")
	flags.StringVar(configPath, "c", defaultConsoleConfigPath, "root-managed anasd service configuration")
	registerJSONFlag(flags)
	positional, err := parseInterspersed(flags, args)
	if err != nil || len(positional) != 0 {
		return usageErrorf("usage: anas console status [--config PATH] [--json]")
	}
	config, err := loadConsoleCLIConfig(*configPath, policy)
	if err != nil {
		return err
	}
	status, err := ConfiguredConsoleStatus(config, net.InterfaceAddrs)
	if err != nil {
		return failuref("console_status_failed", "discover direct management addresses: %v", err)
	}
	if jsonMode {
		return emitOK(map[string]any{
			"mode": status.Mode, "direct_recovery_urls": status.DirectRecoveryURLs,
			"proxy_url": status.ProxyURL, "recovery_entry": "direct",
			"binds_all_interfaces": status.BindsAllInterfaces,
		})
	}
	fmt.Printf("Direct recovery (local owner):\n")
	for _, address := range status.DirectRecoveryURLs {
		fmt.Printf("  %s\n", address)
	}
	if status.ProxyURL != "" {
		fmt.Printf("Traefik / OIDC: %s\n", status.ProxyURL)
	}
	if status.BindsAllInterfaces {
		fmt.Println("Exposure: LAN mode binds every IPv4/IPv6 interface; firewall and interface isolation are the administrator's responsibility.")
	}
	return nil
}

// ConfiguredConsoleStatus derives canonical operator entry points without
// consulting request-controlled Host or forwarding headers.
func ConfiguredConsoleStatus(config consoleconfig.Config, interfaceAddrs func() ([]net.Addr, error)) (ConsoleStatus, error) {
	scheme := "http"
	if config.TLS.Lego != nil || config.TLS.Temporary != nil {
		scheme = "https"
	}
	seen := map[string]struct{}{}
	direct := make([]string, 0, len(config.AllowedDNSHosts)+4)
	appendURL := func(host string) {
		origin := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(config.Port))
		if _, exists := seen[origin]; !exists {
			seen[origin] = struct{}{}
			direct = append(direct, origin)
		}
	}
	for _, host := range config.AllowedDNSHosts {
		appendURL(host)
	}
	addresses, err := interfaceAddrs()
	if err != nil {
		return ConsoleStatus{}, err
	}
	var ips []string
	for _, address := range addresses {
		var ip net.IP
		switch value := address.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		default:
			raw, _, found := strings.Cut(address.String(), "/")
			if found {
				ip = net.ParseIP(raw)
			}
		}
		if ip == nil || ip.IsUnspecified() || config.Mode == consoleconfig.ModeLoopback && !ip.IsLoopback() {
			continue
		}
		ips = append(ips, ip.String())
	}
	sort.Strings(ips)
	for _, ip := range ips {
		appendURL(ip)
	}
	if len(direct) == 0 {
		if config.Mode == consoleconfig.ModeLoopback {
			appendURL("127.0.0.1")
		} else {
			return ConsoleStatus{}, errors.New("no configured DNS host or local IP address is available")
		}
	}
	proxyURL := ""
	if config.TrustedProxy != nil {
		proxyURL = config.TrustedProxy.PublicURL
	}
	return ConsoleStatus{Mode: config.Mode, DirectRecoveryURLs: direct, ProxyURL: proxyURL, BindsAllInterfaces: config.Mode == consoleconfig.ModeLAN}, nil
}

type consoleCommandFlags struct {
	configPath *string
	ttl        *time.Duration
}

func newConsoleCommandFlags(name string) (*flag.FlagSet, consoleCommandFlags) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", defaultConsoleConfigPath, "root-managed anasd service configuration")
	flags.StringVar(configPath, "c", defaultConsoleConfigPath, "root-managed anasd service configuration")
	ttl := flags.Duration("ttl", consoleauth.DefaultBootstrapTokenTTL, "bootstrap token lifetime (15m to 30m)")
	registerJSONFlag(flags)
	return flags, consoleCommandFlags{configPath: configPath, ttl: ttl}
}

func runConsoleToken(args []string, jsonMode bool, policy consoleconfig.FileSecurityPolicy) error {
	flags, values := newConsoleCommandFlags("console token")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return usageErrorf("console token: %v", err)
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas console token [--config PATH] [--ttl DURATION] [--json]")
	}
	config, err := loadConsoleCLIConfig(*values.configPath, policy)
	if err != nil {
		return err
	}
	issued, err := issueConfiguredBootstrapToken(context.Background(), config, *values.ttl)
	if err != nil {
		return failuref("console_token_failed", "issue console bootstrap token: %v", err)
	}
	return printConsoleToken(issued, jsonMode)
}

func runConsoleTLS(args []string, jsonMode bool, policy consoleconfig.FileSecurityPolicy) error {
	flags, values := newConsoleCommandFlags("console tls")
	selfSigned := flags.Bool("self-signed", false, "generate the configured disposable bootstrap certificate")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return usageErrorf("console tls: %v", err)
	}
	if len(positional) != 0 || !*selfSigned {
		return usageErrorf("usage: anas console tls --self-signed [--config PATH] [--ttl DURATION] [--json]")
	}
	config, err := loadConsoleCLIConfig(*values.configPath, policy)
	if err != nil {
		return err
	}
	temporary, directory, err := configuredTemporaryCertificate(config)
	if err != nil {
		return err
	}
	result, err := tempconsolecert.Generate(tempconsolecert.Options{
		Directory: directory, DNSNames: temporary.DNSNames, IPAddresses: temporary.IPAddresses,
	})
	if err != nil {
		return failuref("temporary_tls_generation_failed", "generate temporary console certificate: %v", err)
	}
	if result.CertificatePath != temporary.Certificate || result.PrivateKeyPath != temporary.PrivateKey {
		return failuref("temporary_tls_path_mismatch", "generated certificate paths do not match the root-managed service configuration")
	}

	// Do not print a certificate success result until the audited token is also
	// committed. A token failure leaves a usable disposable pair on disk, but
	// the command reports failure and exposes no partial credential result.
	issued, err := issueConfiguredBootstrapToken(context.Background(), config, *values.ttl)
	if err != nil {
		return failuref("console_token_failed", "temporary certificate was generated but bootstrap token issuance failed: %v", err)
	}
	return printConsoleTLSResult(result, issued, jsonMode)
}

func loadConsoleCLIConfig(path string, policy consoleconfig.FileSecurityPolicy) (consoleconfig.Config, error) {
	config, err := consoleconfig.Load(path, policy)
	if err != nil {
		return consoleconfig.Config{}, preconditionErrorf("console_config_unavailable", "%v", err)
	}
	return config, nil
}

func configuredTemporaryCertificate(config consoleconfig.Config) (*consoleconfig.TemporaryTLSPaths, string, error) {
	if config.TLS.Temporary == nil {
		return nil, "", preconditionErrorf("temporary_tls_not_configured", "root-managed service configuration has no tls.temporary block")
	}
	temporary := config.TLS.Temporary
	certificateDirectory := filepath.Dir(temporary.Certificate)
	if certificateDirectory != filepath.Dir(temporary.PrivateKey) ||
		filepath.Base(temporary.Certificate) != tempconsolecert.CertificateFilename ||
		filepath.Base(temporary.PrivateKey) != tempconsolecert.PrivateKeyFilename {
		return nil, "", preconditionErrorf(
			"temporary_tls_path_mismatch",
			"tls.temporary paths must share one directory and end in %s and %s",
			tempconsolecert.CertificateFilename, tempconsolecert.PrivateKeyFilename,
		)
	}
	return temporary, certificateDirectory, nil
}

func issueConfiguredBootstrapToken(ctx context.Context, config consoleconfig.Config, ttl time.Duration) (consoleauth.IssuedBootstrapToken, error) {
	writer, err := audit.Open(config.ConsoleStore)
	if err != nil {
		return consoleauth.IssuedBootstrapToken{}, err
	}
	stateStore, stateErr := consolestate.Open(ctx, config.ConsoleStore, consoleaudit.StateSink{Writer: writer})
	if stateErr != nil {
		return consoleauth.IssuedBootstrapToken{}, errors.Join(stateErr, writer.Close())
	}
	currentState := func(ctx context.Context) (consoleauth.ConsoleState, error) {
		state, err := stateStore.Current(ctx)
		if err != nil {
			return "", err
		}
		return consoleCLIAuthState(state)
	}
	store, openErr := consoleauth.Open(config.ConsoleStore, consoleaudit.AuthSink{
		Writer: writer,
		Actor:  "console-cli",
	}, consoleauth.StoreOptions{CurrentState: currentState})
	if openErr != nil {
		return consoleauth.IssuedBootstrapToken{}, errors.Join(openErr, writer.Close())
	}
	if recoveryErr := store.RecoverAdvanceToEnrollment(ctx, currentState); recoveryErr != nil {
		return consoleauth.IssuedBootstrapToken{}, errors.Join(recoveryErr, writer.Close())
	}
	if recoveryErr := store.RecoverInitialOwnerCommit(ctx, currentState); recoveryErr != nil {
		return consoleauth.IssuedBootstrapToken{}, errors.Join(recoveryErr, writer.Close())
	}
	capabilityState, stateErr := stateStore.Current(ctx)
	if stateErr != nil {
		return consoleauth.IssuedBootstrapToken{}, errors.Join(stateErr, writer.Close())
	}
	authState := consoleauth.StateBootstrap
	routes := consoleBootstrapRoutePatterns()
	transactionID := consoleBootstrapTransactionID
	switch capabilityState {
	case consolestate.StateBootstrap:
	case consolestate.StateEnrollment:
		authState = consoleauth.StateEnrollment
		routes = consoleauth.EnrollmentRecoveryRoutePatterns()
		transactionID, stateErr = currentEnrollmentTransaction(ctx, store)
		if stateErr != nil {
			return consoleauth.IssuedBootstrapToken{}, errors.Join(stateErr, writer.Close())
		}
	case consolestate.StateFull:
		return consoleauth.IssuedBootstrapToken{}, errors.Join(
			fmt.Errorf("%w: bootstrap credentials are permanently disabled in full state", consoleauth.ErrStateMismatch),
			writer.Close(),
		)
	default:
		return consoleauth.IssuedBootstrapToken{}, errors.Join(
			fmt.Errorf("unsupported console capability state %q", capabilityState), writer.Close(),
		)
	}
	issued, issueErr := store.IssueBootstrapToken(ctx, consoleauth.IssueBootstrapTokenRequest{
		TTL:           ttl,
		TransactionID: transactionID,
		State:         authState,
		AllowedRoutes: routes,
	})
	closeErr := writer.Close()
	if issueErr != nil || closeErr != nil {
		return consoleauth.IssuedBootstrapToken{}, errors.Join(issueErr, closeErr)
	}
	return issued, nil
}

func consoleCLIAuthState(state consolestate.State) (consoleauth.ConsoleState, error) {
	switch state {
	case consolestate.StateBootstrap:
		return consoleauth.StateBootstrap, nil
	case consolestate.StateEnrollment:
		return consoleauth.StateEnrollment, nil
	case consolestate.StateFull:
		return consoleauth.StateFull, nil
	default:
		return "", fmt.Errorf("unsupported console capability state %q", state)
	}
}

func currentEnrollmentTransaction(ctx context.Context, store *consoleauth.Store) (string, error) {
	transactionID, err := store.CurrentBootstrapTransaction(ctx, consoleauth.StateEnrollment)
	if err == nil {
		return transactionID, nil
	}
	if !errors.Is(err, consoleauth.ErrSessionUnauthorized) && !errors.Is(err, consoleauth.ErrCredentialExpired) {
		return "", err
	}
	transactionID, err = store.CurrentEnrollmentTransaction(ctx)
	if err == nil {
		return transactionID, nil
	}
	if errors.Is(err, consoleauth.ErrSessionUnauthorized) || errors.Is(err, consoleauth.ErrCredentialExpired) {
		return consoleBootstrapTransactionID, nil
	}
	return "", err
}

func printConsoleToken(issued consoleauth.IssuedBootstrapToken, jsonMode bool) error {
	if jsonMode {
		return emitOK(consoleTokenFields(issued))
	}
	fmt.Printf("Bootstrap token: %s\nTransaction: %s\nExpires at: %s\n",
		issued.Token, issued.TransactionID, issued.ExpiresAt.Format(time.RFC3339))
	return nil
}

func printConsoleTLSResult(result tempconsolecert.Result, issued consoleauth.IssuedBootstrapToken, jsonMode bool) error {
	if jsonMode {
		fields := consoleTokenFields(issued)
		fields["certificate_path"] = result.CertificatePath
		fields["private_key_path"] = result.PrivateKeyPath
		fields["certificate_sha256_fingerprint"] = result.CertificateSHA256Fingerprint
		fields["spki_sha256_fingerprint"] = result.SPKISHA256Fingerprint
		fields["certificate_not_before"] = result.NotBefore
		fields["certificate_not_after"] = result.NotAfter
		fields["dns_names"] = result.DNSNames
		fields["ip_addresses"] = result.IPAddresses
		return emitOK(fields)
	}
	fmt.Printf("Bootstrap token: %s\nTransaction: %s\nToken expires at: %s\nCertificate: %s\nPrivate key: %s\nCertificate SHA-256: %s\nSPKI SHA-256: %s\nCertificate expires at: %s\n",
		issued.Token, issued.TransactionID, issued.ExpiresAt.Format(time.RFC3339),
		result.CertificatePath, result.PrivateKeyPath,
		result.CertificateSHA256Fingerprint, result.SPKISHA256Fingerprint,
		result.NotAfter.Format(time.RFC3339))
	return nil
}

func consoleTokenFields(issued consoleauth.IssuedBootstrapToken) map[string]any {
	return map[string]any{
		"token":          issued.Token,
		"transaction_id": issued.TransactionID,
		"state":          string(issued.State),
		"issued_at":      issued.IssuedAt,
		"expires_at":     issued.ExpiresAt,
	}
}
