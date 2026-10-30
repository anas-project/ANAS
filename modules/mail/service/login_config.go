package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

// The module supplies this private binding. Read it before dropping privileges;
// retrying online authorization initialization must not reread mounted secrets.
type loginConfig struct {
	URL                  string
	OIDC                 identity.OIDCConfig
	Directory            identity.DirectoryConfig
	CAFile               string
	Webmail              *webmailConfig
	Protocol             *protocolConfig
	DMS                  *dms.ClientConfig
	Language, PortalURL  string
	DirectoryEvents      bool
	CompanyDirectory     bool
	AdmissionGroups      []admissionGroupBinding
	Sources              []ingress.RuntimeSource
	IngressCapacityBytes int64
	Addresses            *identity.AddressConfig
	AllowedDomains       []string
}

func configuredLogin(ctx context.Context, db *sql.DB) (http.Handler, error) {
	cfg, err := readLoginConfig()
	if err != nil || cfg == nil {
		return nil, err
	}
	return loginFromConfig(ctx, *cfg, store.Store{DB: db})
}

func readLoginConfig() (*loginConfig, error) {
	path := os.Getenv("MAIL_IDENTITY_CONFIG")
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("mail identity configuration unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return nil, errors.New("mail identity configuration must be a private regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 65537))
	decoder.DisallowUnknownFields()
	var cfg loginConfig
	if decoder.Decode(&cfg) != nil || cfg.OIDC.Callback != cfg.URL+"/oidc/callback" {
		return nil, errors.New("mail identity configuration invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("mail identity configuration invalid")
	}
	return &cfg, nil
}

func loginFromConfig(ctx context.Context, cfg loginConfig, data loginStore) (http.Handler, error) {
	trust, kicker, err := prepareLoginConfig(cfg)
	if err != nil {
		return nil, err
	}
	return loginFromPrepared(ctx, cfg, data, trust, kicker)
}

func prepareLoginConfig(cfg loginConfig) (*tls.Config, *dms.Client, error) {
	if cfg.OIDC.Callback != cfg.URL+"/oidc/callback" || cfg.CAFile == "" {
		return nil, nil, errors.New("mail identity binding incomplete")
	}
	pem, err := os.ReadFile(cfg.CAFile)
	roots := x509.NewCertPool()
	if err != nil || len(pem) > 4<<20 || !roots.AppendCertsFromPEM(pem) {
		return nil, nil, errors.New("mail identity trust unavailable")
	}
	trust := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	if _, err := loginOrigin(cfg.URL); err != nil {
		return nil, nil, err
	}
	issuer, err := url.Parse(cfg.OIDC.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Hostname() == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.ForceQuery || issuer.Fragment != "" || cfg.OIDC.ClientID == "" || len(cfg.OIDC.ClientID) > 256 || strings.ContainsAny(cfg.OIDC.Issuer+cfg.OIDC.ClientID, "\x00\r\n") {
		return nil, nil, errors.New("mail IAM binding invalid")
	}
	if cfg.Language != "" && !uiLanguages[cfg.Language] {
		return nil, nil, errors.New("mail interface language invalid")
	}
	if cfg.PortalURL != "" {
		if _, err := loginOrigin(cfg.PortalURL); err != nil {
			return nil, nil, errors.New("mail portal origin invalid")
		}
	}
	if identity.ValidateDirectoryEndpoint(cfg.Directory, trust) != nil {
		return nil, nil, errors.New("mail directory binding invalid")
	}
	var kicker *dms.Client
	if cfg.DMS != nil {
		kicker, err = dms.NewClient(*cfg.DMS, trust)
		if err != nil {
			return nil, nil, errors.New("mail DMS binding invalid")
		}
		if cfg.Webmail != nil && cfg.DMS.ServiceToken == cfg.Webmail.ServiceToken || cfg.Protocol != nil && (cfg.DMS.ServiceToken == cfg.Protocol.DovecotToken || cfg.DMS.ServiceToken == cfg.Protocol.SenderToken) {
			return nil, nil, errors.New("mail service credentials must differ")
		}
	}
	return trust, kicker, nil
}

var errAuthorizationDependency = errors.New("mail authorization dependency unavailable")

func loginFromPrepared(ctx context.Context, cfg loginConfig, data loginStore, trust *tls.Config, kicker *dms.Client) (http.Handler, error) {
	if err := bindAdmissionGroups(ctx, &cfg, data, func(ctx context.Context, dn string) (string, error) {
		return identity.ResolveAdmissionGroup(ctx, cfg.Directory, trust, dn)
	}); err != nil {
		return nil, err
	}
	directory, err := identity.NewDirectory(cfg.Directory, trust)
	if err != nil {
		return nil, errors.New("mail directory binding invalid")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = trust
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	iam, err := identity.NewOIDC(ctx, cfg.OIDC, client)
	if err != nil {
		return nil, fmt.Errorf("%w: IAM discovery", errAuthorizationDependency)
	}
	h, err := newLoginHandler(cfg.URL, cfg.OIDC.Issuer, data, iam, directory)
	if err != nil {
		return nil, err
	}
	h.language, h.portalURL = cfg.Language, cfg.PortalURL
	if cfg.Addresses != nil {
		h.addresses, err = identity.NewAddressManager(cfg.Directory, *cfg.Addresses, trust)
		if err != nil {
			return nil, err
		}
		if _, ok := data.(addressAuditStore); !ok {
			return nil, errors.New("mail address audit unavailable")
		}
		reauth := cfg.OIDC
		reauth.ClientID += "-reauth"
		reauth.Callback = cfg.URL + "/oidc/reauth-callback"
		h.reauthIAM, err = identity.NewOIDC(ctx, reauth, client)
		if err != nil {
			return nil, err
		}
	}
	if h.install, err = configuredMailInstaller(cfg.AllowedDomains, trust); err != nil {
		return nil, err
	}
	if cfg.Webmail != nil {
		handoffs, ok := data.(handoffStore)
		if !ok || h.configureWebmail(*cfg.Webmail, handoffs) != nil {
			return nil, errors.New("mail webmail binding invalid")
		}
	}
	if cfg.Protocol != nil {
		protocols, ok := data.(protocolStore)
		if !ok || h.configureProtocol(*cfg.Protocol, protocols, directory) != nil {
			return nil, errors.New("mail protocol binding invalid")
		}
	}
	if cfg.DMS != nil {
		if _, ok := data.(revocationStore); !ok {
			return nil, errors.New("mail revocation storage binding invalid")
		}
		h.dms = kicker
		if h.dms == nil {
			return nil, errors.New("mail DMS binding invalid")
		}
		projections, ok := data.(projectionStore)
		if !ok {
			return nil, errors.New("mail projection storage binding invalid")
		}
		h.coordinator = &mailCoordinator{store: projections, directory: directory, dms: h.dms}
	}
	if cfg.CompanyDirectory {
		if h.coordinator == nil || cfg.Directory.GroupsDN == "" {
			return nil, errors.New("mail company directory binding incomplete")
		}
		if _, ok := data.(companyDirectoryWriter); !ok {
			return nil, errors.New("mail company directory storage unavailable")
		}
		if _, ok := data.(companyDirectoryStore); !ok {
			return nil, errors.New("mail company directory read binding unavailable")
		}
		h.companyEnabled = true
		h.coordinator.companyEnabled = true
	}
	if cfg.DirectoryEvents {
		if h.coordinator == nil {
			return nil, errors.New("mail directory event coordinator unavailable")
		}
		if _, ok := data.(directoryCursorStore); !ok {
			return nil, errors.New("mail directory event cursor unavailable")
		}
		h.directoryEvents = true
	}
	return h, nil
}
