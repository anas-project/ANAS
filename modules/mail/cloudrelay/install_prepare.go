package cloudrelay

import (
	"crypto/x509"
	"errors"
	"reflect"
)

var (
	ErrInstallPreflight      = errors.New("relay installation preflight failed")
	ErrInstallOutcomeUnknown = errors.New("relay installation preparation outcome unknown")
)

// PrepareInstallRuntimeFile validates a single private configuration snapshot,
// checks local prerequisites and initializes the source. It does not install a
// service, start it, change DNS or prove reachability or delivery.
func PrepareInstallRuntimeFile(path string) (InstallPreflight, error) {
	cfg, err := LoadRuntimeConfig(path)
	if err != nil {
		return invalidPreflight(), ErrInstallPreflight
	}
	roots, _ := x509.SystemCertPool()
	return prepareInstallRuntime(path, cfg, roots)
}

func prepareInstallRuntime(path string, cfg RuntimeConfig, roots *x509.CertPool) (InstallPreflight, error) {
	result := preflightRuntime(cfg, roots)
	if result.Configuration != "passed" || result.Certificate != "passed" || result.CertificateTrust != "passed" || cfg.SMTPListen == "" || (result.Directory != "available" && result.Directory != "occupied") {
		return result, ErrInstallPreflight
	}
	// An existing source is verified by initializeRuntime's sealed, locked
	// recovery path. A running instance legitimately owns the listener ports;
	// insufficient remaining free space also need not equal lost reserved mail.
	if result.Directory == "available" && (result.Disk != "sufficient-now" || result.HTTPSPort != "available-now" || result.SMTPPort != "available-now") {
		return result, ErrInstallPreflight
	}
	current, err := LoadRuntimeConfig(path)
	if err != nil || !reflect.DeepEqual(current, cfg) {
		return result, ErrInstallPreflight
	}
	if err := initializeRuntime(cfg); err != nil {
		return result, ErrInstallOutcomeUnknown
	}
	current, err = LoadRuntimeConfig(path)
	if err != nil || !reflect.DeepEqual(current, cfg) {
		return result, ErrInstallOutcomeUnknown
	}
	return result, nil
}
