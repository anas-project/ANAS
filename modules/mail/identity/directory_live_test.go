package identity

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// Explicitly opt-in. It performs only service-bind/search operations against the
// existing authorized finance directory, never edits AD or binds as personnel.
func TestLiveReadOnlyDirectory(t *testing.T) {
	file := os.Getenv("MAIL_IDENTITY_TEST_CONFIG")
	if file == "" {
		t.Skip("requires an explicitly authorized real LDAPS fixture; not real-host acceptance")
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private directory test configuration required")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("directory test configuration unavailable")
	}
	var cfg struct {
		Directory                 DirectoryConfig
		CAPEM, Anchor, ServerName string
	}
	if json.Unmarshal(data, &cfg) != nil || cfg.Directory.BaseDN != "DC=finance,DC=hlong,DC=wang" {
		t.Fatal("refusing directory outside the authorized finance fixture")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(cfg.CAPEM)) {
		t.Fatal("trusted fixture CA required")
	}
	d, err := NewDirectory(cfg.Directory, &tls.Config{RootCAs: roots, ServerName: cfg.ServerName, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal("directory test binding invalid")
	}
	p, err := d.Person(t.Context(), cfg.Anchor)
	if err != nil || p.Anchor != cfg.Anchor || p.Mail == "" || len(p.Groups) == 0 {
		t.Fatal("real LDAPS admission failed", err)
	}
	if _, err := d.Person(t.Context(), "ffffffff-ffff-ffff-ffff-ffffffffffff"); err != ErrAdmission {
		t.Fatal("unknown real-directory identity admitted", err)
	}
	wrong, err := NewDirectory(cfg.Directory, &tls.Config{RootCAs: roots, ServerName: "wrong.example.invalid", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal("negative TLS fixture invalid")
	}
	if _, err := wrong.Person(t.Context(), cfg.Anchor); !errors.Is(err, ErrDirectoryUnavailable) {
		t.Fatal("incorrect real TLS identity accepted", err)
	}
}
