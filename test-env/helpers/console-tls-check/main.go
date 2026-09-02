package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/anas-project/ANAS/internal/consoletls"
)

type output struct {
	Source     consoletls.Source `json:"source"`
	SPKISHA256 string            `json:"spki_sha256"`
	DNSNames   []string          `json:"dns_names"`
}

func main() {
	var candidate consoletls.Candidate
	flag.StringVar(&candidate.CertificatePath, "certificate", "", "serving certificate path")
	flag.StringVar(&candidate.PrivateKeyPath, "private-key", "", "serving private key path")
	flag.StringVar(&candidate.IssuerPath, "issuer", "", "issuer certificate path")
	flag.StringVar(&candidate.TrustBundlePath, "trust-bundle", "", "trust bundle path")
	flag.StringVar(&candidate.InternalCAPath, "internal-ca", "", "internal CA path")
	flag.StringVar(&candidate.IssuerMarkerPath, "issuer-marker", "", "issuer marker path")
	flag.StringVar(&candidate.BaseDomain, "base-domain", "", "lego base domain")
	flag.Parse()

	manager, err := consoletls.NewManager(consoletls.Options{
		Lego:      &candidate,
		CheckFile: consoletls.RootOwnedFileSecurityCheck,
	})
	if err != nil {
		fatal(err)
	}
	if err := manager.Reload(); err != nil {
		fatal(err)
	}
	snapshot, ok := manager.Current()
	if !ok {
		fatal(consoletls.ErrNoCertificate)
	}
	if err := json.NewEncoder(os.Stdout).Encode(output{
		Source:     snapshot.Source(),
		SPKISHA256: snapshot.SPKISHA256Hex(),
		DNSNames:   snapshot.DNSNames(),
	}); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
