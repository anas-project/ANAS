package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/anas-project/ANAS/internal/consoleauth"
)

func (h *handler) listWorkspaces(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	pagination, ok := parseListPagination(w, r)
	if !ok {
		return
	}
	ids := h.registry.IDs()
	items := make([]workspaceListItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, workspaceListItem{ID: id})
	}
	items, next, err := paginateList(items, pagination, "workspaces", func(item workspaceListItem) string { return item.ID })
	if err != nil {
		writePaginatedListError(w)
		return
	}
	writeJSON(w, http.StatusOK, workspaceListResponse{APIVersion: APIVersion, Items: items, NextCursor: next})
}

const internalCAFilename = "anas-internal-ca.crt"

type CertificateIssuer string

const (
	CertificateIssuerNone      CertificateIssuer = "none"
	CertificateIssuerTemporary CertificateIssuer = "temporary"
	CertificateIssuerInternal  CertificateIssuer = "internal"
	CertificateIssuerACME      CertificateIssuer = "acme"
)

// CertificateMaterial is a public-only view of the currently validated TLS
// snapshot. InternalCAPEM is the stable lego trust root, never a private key;
// it may remain available while an ACME certificate is serving traffic.
type CertificateMaterial struct {
	Issuer        CertificateIssuer
	InternalCAPEM []byte
}

// SystemOptions binds public certificate status and the canonical HTTPS
// destination to server-owned configuration. Redirects never derive their
// destination from Host, Origin, forwarding headers, or request parameters.
type SystemOptions struct {
	CurrentCertificate   func(context.Context) (CertificateMaterial, error)
	CanonicalHTTPSOrigin string
	DirectRecoveryURLs   []string
	ProxyURL             string
	BackupTargetIDs      []string
}

type systemHTTPState struct {
	currentCertificate   func(context.Context) (CertificateMaterial, error)
	canonicalHTTPSOrigin string
	directRecoveryURLs   []string
	proxyURL             string
	backupTargetIDs      []string
}

func newSystemHTTPState(options SystemOptions) (*systemHTTPState, error) {
	state := &systemHTTPState{currentCertificate: options.CurrentCertificate}
	if options.CanonicalHTTPSOrigin != "" {
		origin, err := consoleauth.NormalizeOrigin(options.CanonicalHTTPSOrigin)
		if err != nil || origin != options.CanonicalHTTPSOrigin || len(origin) < len("https://") || origin[:len("https://")] != "https://" {
			return nil, errors.New("canonical HTTPS origin must be an exact normalized https origin")
		}
		state.canonicalHTTPSOrigin = origin
	}
	seenRecoveryURLs := make(map[string]struct{}, len(options.DirectRecoveryURLs))
	for _, value := range options.DirectRecoveryURLs {
		origin, err := consoleauth.NormalizeOrigin(value)
		if err != nil || origin != value {
			return nil, errors.New("direct recovery URL must be an exact normalized HTTP(S) origin")
		}
		if _, exists := seenRecoveryURLs[origin]; exists {
			return nil, errors.New("direct recovery URLs must be unique")
		}
		seenRecoveryURLs[origin] = struct{}{}
		state.directRecoveryURLs = append(state.directRecoveryURLs, origin)
	}
	if options.ProxyURL != "" {
		origin, err := consoleauth.NormalizeOrigin(options.ProxyURL)
		if err != nil || origin != options.ProxyURL || len(origin) < len("https://") || origin[:len("https://")] != "https://" {
			return nil, errors.New("proxy URL must be an exact normalized https origin")
		}
		state.proxyURL = origin
	}
	seenBackupTargets := make(map[string]struct{}, len(options.BackupTargetIDs))
	for _, id := range options.BackupTargetIDs {
		if id == "" || len(id) > 64 {
			return nil, errors.New("backup target ID is invalid")
		}
		if _, exists := seenBackupTargets[id]; exists {
			return nil, errors.New("backup target IDs must be unique")
		}
		seenBackupTargets[id] = struct{}{}
		state.backupTargetIDs = append(state.backupTargetIDs, id)
	}
	return state, nil
}

func (state *systemHTTPState) certificate(ctx context.Context) (CertificateMaterial, error) {
	if state == nil || state.currentCertificate == nil {
		return CertificateMaterial{Issuer: CertificateIssuerNone}, nil
	}
	material, err := state.currentCertificate(ctx)
	if err != nil {
		return CertificateMaterial{}, err
	}
	switch material.Issuer {
	case CertificateIssuerNone, CertificateIssuerTemporary:
		if len(material.InternalCAPEM) != 0 {
			return CertificateMaterial{}, errors.New("certificate material exposes an internal CA without a lego certificate")
		}
	case CertificateIssuerInternal, CertificateIssuerACME:
		if len(material.InternalCAPEM) == 0 {
			return CertificateMaterial{}, errors.New("lego certificate material omits the internal CA")
		}
	default:
		return CertificateMaterial{}, errors.New("certificate material has an unsupported issuer")
	}
	material.InternalCAPEM = append([]byte{}, material.InternalCAPEM...)
	return material, nil
}

func (h *handler) downloadInternalCA(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	material, err := h.systemHTTP.certificate(r.Context())
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "certificate_unavailable", "certificate information is unavailable")
		return
	}
	if len(material.InternalCAPEM) == 0 {
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+internalCAFilename+`"`)
	w.Header().Set("Content-Type", "application/pem-certificate-chain")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(material.InternalCAPEM)
}

func (h *handler) redirectToCanonicalHTTPS(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if h.systemHTTP == nil || h.systemHTTP.canonicalHTTPSOrigin == "" || r.URL.RawQuery != "" ||
		plaintextCarriesCredentialOrBody(r) {
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
		return
	}
	w.Header().Set("Location", h.systemHTTP.canonicalHTTPSOrigin+"/")
	w.WriteHeader(http.StatusPermanentRedirect)
}

func plaintextCarriesCredentialOrBody(r *http.Request) bool {
	return r != nil && (len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Authorization")) != 0 ||
		r.ContentLength != 0 || len(r.TransferEncoding) != 0)
}
