package main

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
)

const (
	hostBundleSchema      = "anas.incus-connection-bundle/v1"
	defaultHostBundlePath = "/var/lib/anas/incus-host/connection.json"
	autoSourceSecretKey   = "INCUS_HOST_CONNECTION_SOURCE"
	autoBindingSecretKey  = "INCUS_HOST_CONNECTION_BINDING"
	autoSourceValue       = "host-bundle:v1"
)

var hostBundleTestPath string
var hostBundleTrustedUID *uint32

type hostConnectionBundle struct {
	Schema                string `json:"schema"`
	Endpoint              string `json:"endpoint"`
	ServerCertificatePEM  string `json:"server_certificate_pem"`
	AdminCertificatePEM   string `json:"admin_certificate_pem"`
	AdminPrivateKeyPEM    string `json:"admin_private_key_pem"`
	ControlNetwork        string `json:"control_network"`
	ControlSubnet         string `json:"control_subnet"`
	ControlGateway        string `json:"control_gateway"`
	RelayService          string `json:"relay_service"`
	ManagementFingerprint string `json:"management_fingerprint"`
	Architecture          string `json:"architecture"`
	StoragePool           string `json:"storage_pool"`
}

func loadDefaultHostConnectionBundle() (hostConnectionBundle, error) {
	if runtime.GOOS != "linux" && hostBundleTestPath == "" {
		return hostConnectionBundle{}, fmt.Errorf("incus host auto-connection is only available on installed Linux hosts")
	}
	path := defaultHostBundlePath
	if hostBundleTestPath != "" {
		path = hostBundleTestPath
	}
	body, err := readSafeHostBundleFile(path, 64*1024)
	if err != nil {
		return hostConnectionBundle{}, fmt.Errorf("incus host connection bundle is unavailable or unsafe")
	}
	defer clear(body)
	if err := validateNoDuplicateJSONFields(body); err != nil {
		return hostConnectionBundle{}, fmt.Errorf("incus host connection bundle is unavailable or unsafe")
	}
	var bundle hostConnectionBundle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&bundle) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return hostConnectionBundle{}, fmt.Errorf("incus host connection bundle is unavailable or unsafe")
	}
	canonical, marshalErr := json.Marshal(bundle)
	var compact bytes.Buffer
	if marshalErr != nil || json.Compact(&compact, body) != nil || !bytes.Equal(compact.Bytes(), canonical) {
		return hostConnectionBundle{}, fmt.Errorf("incus host connection bundle is unavailable or unsafe")
	}
	if err := validateHostConnectionBundle(bundle); err != nil {
		return hostConnectionBundle{}, err
	}
	return bundle, nil
}

func applyHostConnectionBundle(e map[string]string, secrets *secretStore, bundle hostConnectionBundle) error {
	if secrets == nil {
		return fmt.Errorf("incus host auto-connection requires the module secret store")
	}
	binding, err := bundleBinding(bundle)
	if err != nil {
		return err
	}
	if secrets.values[autoSourceSecretKey] == autoSourceValue && secrets.values[autoBindingSecretKey] != binding {
		return fmt.Errorf("incus host connection bundle changed since the previous automatic binding")
	}
	e["INCUS_ENDPOINT"] = bundle.Endpoint
	e["INCUS_SERVER_CERTIFICATE_B64"] = base64.StdEncoding.EncodeToString([]byte(bundle.ServerCertificatePEM))
	e["INCUS_ADMIN_CERTIFICATE_B64"] = base64.StdEncoding.EncodeToString([]byte(bundle.AdminCertificatePEM))
	e["INCUS_ADMIN_KEY_B64"] = base64.StdEncoding.EncodeToString([]byte(bundle.AdminPrivateKeyPEM))
	e["INCUS_IMAGE_ARCHITECTURE"] = bundle.Architecture
	e["INCUS_STORAGE_POOL"] = bundle.StoragePool
	for _, key := range []string{"INCUS_ENDPOINT", "INCUS_SERVER_CERTIFICATE_B64", "INCUS_ADMIN_CERTIFICATE_B64", "INCUS_ADMIN_KEY_B64"} {
		secrets.values[key] = e[key]
	}
	secrets.values[autoSourceSecretKey] = autoSourceValue
	secrets.values[autoBindingSecretKey] = binding
	return nil
}

func validateHostConnectionBundle(bundle hostConnectionBundle) error {
	if bundle.Schema != hostBundleSchema {
		return fmt.Errorf("incus host connection bundle is unavailable or unsafe")
	}
	if bundle.Architecture != "amd64" && bundle.Architecture != "arm64" {
		return fmt.Errorf("incus host connection bundle does not declare a supported architecture")
	}
	if bundle.StoragePool != "anas-btrfs" {
		return fmt.Errorf("incus host connection bundle does not declare the managed storage pool")
	}
	if bundle.ControlNetwork != "anas-incus-control" || bundle.RelayService != "anas-incus-control-relay.service" {
		return fmt.Errorf("incus host connection bundle does not match the managed control channel")
	}
	gateway := net.ParseIP(bundle.ControlGateway)
	if gateway == nil || gateway.To4() == nil || bundle.Endpoint != "https://"+gateway.String()+":18443" {
		return fmt.Errorf("incus host connection bundle endpoint does not match the control gateway")
	}
	prefix, prefixErr := netip.ParsePrefix(bundle.ControlSubnet)
	address, addressErr := netip.ParseAddr(bundle.ControlGateway)
	if prefixErr != nil || addressErr != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() || prefix != prefix.Masked() ||
		prefix.String() != bundle.ControlSubnet || !prefix.Contains(address) || address != prefix.Addr().Next() {
		return fmt.Errorf("incus host connection bundle control subnet is invalid")
	}
	endpoint, err := url.Parse(bundle.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.Opaque != "" || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawPath != "" {
		return fmt.Errorf("incus host connection bundle endpoint is invalid")
	}
	if _, err := parseCertificatePEM(bundle.ServerCertificatePEM); err != nil {
		return fmt.Errorf("incus host connection bundle server certificate is invalid")
	}
	adminCert, err := parseCertificatePEM(bundle.AdminCertificatePEM)
	if err != nil {
		return fmt.Errorf("incus host connection bundle admin certificate is invalid")
	}
	adminKey, err := parsePrivateKeyPEM(bundle.AdminPrivateKeyPEM)
	if err != nil || !publicKeysEqual(adminCert.PublicKey, adminKey.Public()) {
		return fmt.Errorf("incus host connection bundle admin credential pair is invalid")
	}
	sum := sha256.Sum256(adminCert.Raw)
	if bundle.ManagementFingerprint != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("incus host connection bundle management fingerprint is invalid")
	}
	return nil
}

func bundleBinding(bundle hostConnectionBundle) (string, error) {
	body, err := json.Marshal(struct {
		Schema                string `json:"schema"`
		Endpoint              string `json:"endpoint"`
		ServerCertificatePEM  string `json:"server_certificate_pem"`
		AdminCertificatePEM   string `json:"admin_certificate_pem"`
		AdminPrivateKeyPEM    string `json:"admin_private_key_pem"`
		ControlNetwork        string `json:"control_network"`
		ControlSubnet         string `json:"control_subnet"`
		ControlGateway        string `json:"control_gateway"`
		RelayService          string `json:"relay_service"`
		ManagementFingerprint string `json:"management_fingerprint"`
		Architecture          string `json:"architecture"`
		StoragePool           string `json:"storage_pool"`
	}{
		Schema: bundle.Schema, Endpoint: bundle.Endpoint, ServerCertificatePEM: bundle.ServerCertificatePEM,
		AdminCertificatePEM: bundle.AdminCertificatePEM, AdminPrivateKeyPEM: bundle.AdminPrivateKeyPEM,
		ControlNetwork: bundle.ControlNetwork, ControlSubnet: bundle.ControlSubnet, ControlGateway: bundle.ControlGateway,
		RelayService: bundle.RelayService, ManagementFingerprint: bundle.ManagementFingerprint,
		Architecture: bundle.Architecture, StoragePool: bundle.StoragePool,
	})
	if err != nil {
		return "", fmt.Errorf("incus host connection bundle is unavailable or unsafe")
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func validAutoBindingDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func parseCertificatePEM(value string) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(strings.TrimSpace(value)))
	if block == nil || block.Type != "CERTIFICATE" || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("invalid certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parsePrivateKeyPEM(value string) (crypto.Signer, error) {
	block, rest := pem.Decode([]byte(strings.TrimSpace(value)))
	if block == nil || !strings.HasSuffix(block.Type, "PRIVATE KEY") || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("invalid private key")
	}
	for _, parse := range []func([]byte) (any, error){
		func(der []byte) (any, error) { return x509.ParsePKCS8PrivateKey(der) },
		func(der []byte) (any, error) { return x509.ParseECPrivateKey(der) },
		func(der []byte) (any, error) { return x509.ParsePKCS1PrivateKey(der) },
	} {
		key, err := parse(block.Bytes)
		if err == nil {
			signer, ok := key.(crypto.Signer)
			if ok {
				return signer, nil
			}
		}
	}
	return nil, errors.New("invalid private key")
}

func publicKeysEqual(a, b crypto.PublicKey) bool {
	switch ak := a.(type) {
	case *ecdsa.PublicKey:
		bk, ok := b.(*ecdsa.PublicKey)
		return ok && ak.Equal(bk)
	case *rsa.PublicKey:
		bk, ok := b.(*rsa.PublicKey)
		return ok && ak.Equal(bk)
	case ed25519.PublicKey:
		bk, ok := b.(ed25519.PublicKey)
		return ok && ak.Equal(bk)
	default:
		return reflect.DeepEqual(a, b)
	}
}

func readSafeHostBundleFile(path string, max int64) (body []byte, result error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("relative path")
	}
	trustedUID := uint32(0)
	if hostBundleTrustedUID != nil {
		trustedUID = *hostBundleTrustedUID
	}
	if err := validateSafeAncestors(filepath.Dir(path), trustedUID); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := validateSafeBundleFileInfo(before, trustedUID, max); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if file.Close() != nil {
			clear(body)
			body = nil
			result = errors.New("bundle close failed")
		}
	}()
	opened, err := file.Stat()
	if err != nil || validateSafeBundleFileInfo(opened, trustedUID, max) != nil || !sameFileIdentity(before, opened) {
		return nil, errors.New("bundle changed during open")
	}
	body, err = io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		clear(body)
		return nil, errors.New("bundle read failed")
	}
	if int64(len(body)) > max {
		clear(body)
		return nil, errors.New("too large")
	}
	finished, err := file.Stat()
	if err != nil || !sameFileIdentity(opened, finished) || validateSafeAncestors(filepath.Dir(path), trustedUID) != nil {
		clear(body)
		return nil, errors.New("bundle changed during read")
	}
	after, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := validateSafeBundleFileInfo(after, trustedUID, max); err != nil {
		return nil, err
	}
	if !sameFileIdentity(before, after) {
		clear(body)
		return nil, errors.New("file changed")
	}
	return body, nil
}

func validateSafeAncestors(dir string, trustedUID uint32) error {
	clean := filepath.Clean(dir)
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("unsafe ancestor")
		}
		if err := validateOwnerAndMode(info, trustedUID, 022); err != nil {
			return err
		}
	}
	return nil
}

func validateSafeBundleFileInfo(info os.FileInfo, trustedUID uint32, max int64) error {
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0600 || info.Size() < 0 || info.Size() > max {
		return errors.New("unsafe bundle file")
	}
	if err := validateOwnerAndMode(info, trustedUID, 077); err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return errors.New("unsafe bundle file")
	}
	return nil
}

func validateOwnerAndMode(info os.FileInfo, trustedUID uint32, writableMask os.FileMode) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && stat.Uid != trustedUID) || info.Mode().Perm()&writableMask != 0 {
		return errors.New("unsafe ownership")
	}
	return nil
}

func sameFileIdentity(a, b os.FileInfo) bool {
	as, aok := a.Sys().(*syscall.Stat_t)
	bs, bok := b.Sys().(*syscall.Stat_t)
	if !aok || !bok {
		return false
	}
	return as.Dev == bs.Dev && as.Ino == bs.Ino && as.Uid == bs.Uid && as.Gid == bs.Gid && as.Mode == bs.Mode && as.Size == bs.Size && a.ModTime().Equal(b.ModTime())
}

func validateNoDuplicateJSONFields(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return rejectDuplicateJSONFields(json.NewDecoder(bytes.NewReader(body)))
}

func rejectDuplicateJSONFields(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := token.(string)
				if !ok {
					return errors.New("invalid object")
				}
				if seen[name] {
					return errors.New("duplicate field")
				}
				seen[name] = true
				if err := rejectDuplicateJSONFields(decoder); err != nil {
					return err
				}
			}
			_, err := decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := rejectDuplicateJSONFields(decoder); err != nil {
					return err
				}
			}
			_, err := decoder.Token()
			return err
		}
	}
	return nil
}
