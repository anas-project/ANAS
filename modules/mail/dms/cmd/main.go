package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

type config struct {
	Host, Token, CertFile, KeyFile string
	CapacityBytes                  int64
	Runtime                        dms.RuntimeConfig
	Outbound                       smtpconfig.Config
}

var hostPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*:9443$`)

func load(path string) (config, error) {
	var cfg config
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		return cfg, errors.New("DMS private configuration unavailable")
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg, errors.New("DMS private configuration unavailable")
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 16385))
	d.DisallowUnknownFields()
	if d.Decode(&cfg) != nil {
		return config{}, errors.New("DMS private configuration invalid")
	}
	var extra any
	if d.Decode(&extra) != io.EOF || !hostPattern.MatchString(cfg.Host) || cfg.CertFile == "" || cfg.KeyFile == "" || len(cfg.Token) != 64 || strings.Trim(cfg.Token, "0123456789abcdef") != "" || cfg.CapacityBytes < 1<<20 || cfg.CapacityBytes > 1<<40 {
		return config{}, errors.New("DMS private configuration invalid")
	}
	if _, _, err := dms.RuntimeFiles(cfg.Runtime); err != nil || cfg.Token == cfg.Runtime.DovecotToken || cfg.Token == cfg.Runtime.SenderToken {
		return config{}, errors.New("DMS runtime configuration invalid")
	}
	if err := cfg.Outbound.Validate(); err != nil {
		return config{}, errors.New("DMS outbound configuration invalid")
	}
	return cfg, nil
}
func run() error {
	if len(os.Args) > 2 || len(os.Args) == 2 && os.Args[1] != "prepare" {
		return errors.New("DMS adapter command invalid")
	}
	path := os.Getenv("MAIL_DMS_CONFIG")
	if path == "" {
		path = "/run/anas/mail-dms/config.json"
	}
	cfg, err := load(path)
	if err != nil {
		return err
	}
	if len(os.Args) == 2 {
		return prepare(cfg)
	}
	certificate, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return errors.New("DMS TLS identity unavailable")
	}
	const stateDir = "/var/mail-state/anas-control"
	if err = os.MkdirAll(stateDir, 0700); err != nil {
		return errors.New("DMS state unavailable")
	}
	if err = os.Chmod(stateDir, 0700); err != nil {
		return errors.New("DMS state unavailable")
	}
	a := &dms.Adapter{StateDir: stateDir, ConfigDir: "/tmp/docker-mailserver", CapacityBytes: cfg.CapacityBytes}
	server := &http.Server{Addr: ":9443", Handler: dms.Handler{Adapter: a, Token: cfg.Token, Host: cfg.Host}, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	err = server.ListenAndServeTLS("", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return errors.New("DMS adapter listener failed")
}

func prepare(cfg config) error {
	dovecot, lua, err := dms.RuntimeFiles(cfg.Runtime)
	if err != nil {
		return err
	}
	pem, err := os.ReadFile(cfg.Runtime.CAFile)
	roots := x509.NewCertPool()
	if err != nil || len(pem) > 4<<20 || !roots.AppendCertsFromPEM(pem) {
		return errors.New("DMS trust unavailable")
	}
	group, err := user.LookupGroup("_rspamd")
	if err != nil {
		return errors.New("DMS sender adapter identity unavailable")
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return errors.New("DMS sender adapter identity unavailable")
	}
	if err = dms.PrepareRuntime(dovecot, lua, pem, gid); err != nil {
		return errors.New("DMS runtime preparation failed")
	}
	if err := dms.PrepareOutbound(cfg.Outbound); err != nil {
		return err
	}
	a := &dms.Adapter{StateDir: "/var/mail-state/anas-control", ConfigDir: "/tmp/docker-mailserver", CapacityBytes: cfg.CapacityBytes}
	return a.PrepareInputs()
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
