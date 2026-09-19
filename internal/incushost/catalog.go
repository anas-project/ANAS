// Package incushost prepares Incus host-installation decisions. It does not
// install packages, invoke commands, connect to sockets or enable compute.
package incushost

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"slices"
	"time"
)

//go:embed recipes.json
var recipeData []byte

var ErrInvalid = errors.New("invalid Incus host preflight input")
var ErrObservation = errors.New("Incus host facts could not be safely observed")

// Recipe is compiled installation data, not an executable script. The package
// version is a dated packaging observation, never an apt pin or runtime
// compatibility assertion. APT origin/signature checks still belong to the
// future installer; a source label here does not authenticate an installed pkg.
type Recipe struct {
	ID                     string   `json:"id"`
	Distribution           string   `json:"distribution"`
	Version                string   `json:"version"`
	Codename               string   `json:"codename"`
	Architectures          []string `json:"architectures"`
	PackageManager         string   `json:"package_manager"`
	Repository             string   `json:"repository"`
	Packages               []string `json:"packages"`
	EvidenceDate           string   `json:"evidence_date"`
	ObservedPackageVersion string   `json:"observed_package_version"`
	EvidenceURL            string   `json:"evidence_url"`
}

var distroID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
var versionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~^-]{0,62}$`)
var packageID = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,62}$`)

func Recipes() ([]Recipe, error) { return parseRecipes(recipeData) }

func parseRecipes(body []byte) ([]Recipe, error) {
	if len(body) > 32<<10 {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var rows []Recipe
	if d.Decode(&rows) != nil || d.Decode(&struct{}{}) != io.EOF || len(rows) == 0 || len(rows) > 32 {
		return nil, ErrInvalid
	}
	// The embedded format is canonical apart from whitespace. This also
	// rejects duplicate keys and aliases instead of accepting last-wins data.
	encoded, err := json.Marshal(rows)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, body) != nil || !bytes.Equal(encoded, compact.Bytes()) {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range rows {
		key := r.Distribution + ":" + r.Version
		u, err := url.Parse(r.EvidenceURL)
		if !distroID.MatchString(r.ID) || !distroID.MatchString(r.Distribution) || !versionID.MatchString(r.Version) || !distroID.MatchString(r.Codename) || seen[key] || r.PackageManager != "apt" || (r.Repository != "official" && r.Repository != "universe") || err != nil || u.Scheme != "https" || u.User != nil || (u.Host != "packages.debian.org" && u.Host != "packages.ubuntu.com") || u.RawQuery != "" || u.Fragment != "" || len(r.ObservedPackageVersion) > 128 || r.ObservedPackageVersion == "" {
			return nil, ErrInvalid
		}
		if _, err := time.Parse("2006-01-02", r.EvidenceDate); err != nil {
			return nil, ErrInvalid
		}
		seen[key] = true
		if len(r.Architectures) == 0 || len(r.Architectures) > 2 || len(r.Packages) == 0 || len(r.Packages) > 16 {
			return nil, ErrInvalid
		}
		for i, arch := range r.Architectures {
			if (arch != "amd64" && arch != "arm64") || slices.Contains(r.Architectures[:i], arch) {
				return nil, ErrInvalid
			}
		}
		for i, pkg := range r.Packages {
			if !packageID.MatchString(pkg) || slices.Contains(r.Packages[:i], pkg) {
				return nil, ErrInvalid
			}
		}
	}
	return rows, nil
}
