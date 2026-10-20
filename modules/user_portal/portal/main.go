package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		client := &http.Client{Timeout: 12 * time.Second}
		response, err := client.Get("http://127.0.0.1:8080/health")
		if err != nil {
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 1 {
		log.Fatal("unsupported portal command")
	}
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if err := prepareStorage(c.AuditDir); err != nil {
		log.Fatal(err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		log.Fatal("system trust store unavailable")
	}
	ca, err := os.ReadFile(c.CAFile)
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		log.Fatal("portal trust bundle unavailable")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	o, err := newOIDC(ctx, c, client)
	if err != nil {
		log.Fatal(err)
	}
	catalog, err := appcatalog.Read(c.Catalog)
	if err != nil {
		log.Fatal("invalid private application catalog")
	}
	ldapURL, _ := url.Parse(c.LDAPURL)
	directory := &LDAPDirectory{config: c, tls: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, ServerName: ldapURL.Hostname()}}
	if err := directory.resolveAdministration(); err != nil {
		log.Fatal("invalid administration role or managed group registry")
	}
	c = directory.config
	if err := validateReferences(c, directory); err != nil {
		log.Fatal("invalid or unavailable delegation references")
	}
	journal, err := openJournal(c.AuditDir, c.AuditMaxBytes)
	if err != nil {
		log.Fatal("portal audit unavailable")
	}
	defer journal.close()
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", c.ProxyHost)
	if err != nil || len(addresses) == 0 {
		log.Fatal("trusted proxy unavailable")
	}
	peers := []netip.Addr{}
	for _, a := range addresses {
		peers = append(peers, a.Unmap())
	}
	policy, _ := json.Marshal(struct {
		Rules                                        []Rule
		Users, Groups, Names                         []string
		Administration, DomainAdministration         bool
		AdminGroup, AdminGUID, ProfileWriter         string
		CreateWriter, UsersDN, MailDomain, UPNSuffix string
		Managed                                      map[string]string
	}{c.Rules, c.ProtectedUsers, c.ProtectedGroups, c.ProtectedGroupNames, c.Administration, c.DomainAdministration, c.AdminGroup, c.AdminGroupGUID, c.ProfileBindDN, c.CreateBindDN, c.UsersDN, c.MailDomain, c.UPNSuffix, c.ManagedGroups})
	digest := sha256.Sum256(policy)
	s := &Server{config: c, oidc: o, directory: directory, audit: journal, catalog: catalog, store: newStore(), limiter: newLimiter(), preferences: openPreferences(filepath.Join(filepath.Dir(c.AuditDir), "preferences")), peers: peers, digest: hex.EncodeToString(digest[:])}
	httpServer := &http.Server{Addr: ":8080", Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	log.Print("user portal listening on :8080")
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("portal listener failed")
	}
}

func validateReferences(c Config, d Directory) error {
	groups := append([]string{}, c.ProtectedGroups...)
	users := append([]string{}, c.ProtectedUsers...)
	for _, rule := range c.Rules {
		groups = append(groups, rule.OperatorGroups...)
		groups = append(groups, rule.TargetUserGroups...)
		groups = append(groups, rule.EditableGroups...)
		users = append(users, rule.TargetUsers...)
	}
	for _, g := range groups {
		if _, err := d.Group(g); err != nil {
			return err
		}
	}
	for _, u := range users {
		if _, err := d.Person(u); err != nil {
			return err
		}
	}
	return nil
}
