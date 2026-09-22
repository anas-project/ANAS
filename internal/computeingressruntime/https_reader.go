package computeingressruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

// pinnedGETClient is private to the trusted readers. It has no write, redirect,
// proxy, cookie, Unix-socket or caller-supplied request API. Client-side GET
// restriction does not establish the credential's server-side permissions.
type pinnedGETClient struct {
	origin     string
	http       *http.Client
	validFrom  time.Time
	validUntil time.Time
	username   string
	password   string
	// Installed readers pin the private credential artifact for every GET,
	// including withdrawals using the old epoch after Core authority changes.
	check func(context.Context) error
}

func newPinnedGETClient(endpoint string, serverPEM []byte, certificates []tls.Certificate) (*pinnedGETClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 2048 || u.Scheme != "https" || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(endpoint, "%\\\r\n\t #?") {
		return nil, fmt.Errorf("observer endpoint must be an explicit HTTPS origin")
	}
	pinned, err := singleCertificate(serverPEM)
	if err != nil {
		return nil, fmt.Errorf("observer requires one valid pinned server certificate")
	}
	validFrom, validUntil := pinned.NotBefore, pinned.NotAfter
	for _, certificate := range certificates {
		if len(certificate.Certificate) == 0 {
			return nil, fmt.Errorf("observer client certificate is missing")
		}
		leaf, err := x509.ParseCertificate(certificate.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("observer client certificate is invalid")
		}
		if leaf.NotBefore.After(validFrom) {
			validFrom = leaf.NotBefore
		}
		if leaf.NotAfter.Before(validUntil) {
			validUntil = leaf.NotAfter
		}
	}
	if now := time.Now(); now.Before(validFrom) || !now.Before(validUntil) {
		return nil, fmt.Errorf("observer certificate is outside its validity period")
	}
	transport := &http.Transport{
		Proxy:       nil,
		DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: certificates,
			// Incus uses a self-signed leaf. Exact DER identity plus validity
			// replaces CA/hostname verification; no mismatch fallback exists.
			InsecureSkipVerify: true,
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, pinned.Raw) {
					return fmt.Errorf("observer server certificate pin mismatch")
				}
				now := time.Now()
				if now.Before(pinned.NotBefore) || !now.Before(pinned.NotAfter) {
					return fmt.Errorf("observer server certificate is outside its validity period")
				}
				return nil
			},
		},
		TLSHandshakeTimeout:    3 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
		MaxIdleConns:           2,
		MaxIdleConnsPerHost:    2,
		MaxConnsPerHost:        2,
		IdleConnTimeout:        30 * time.Second,
		DisableCompression:     true,
	}
	return &pinnedGETClient{origin: "https://" + u.Host, validFrom: validFrom, validUntil: validUntil, http: &http.Client{
		Transport:     transport,
		Timeout:       8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func singleCertificate(body []byte) (*x509.Certificate, error) {
	if len(body) == 0 || len(body) > 64<<10 {
		return nil, fmt.Errorf("invalid certificate size")
	}
	trimmed := bytes.TrimSpace(body)
	block, rest := pem.Decode(trimmed)
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN CERTIFICATE-----")) || block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("expected one PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func (c *pinnedGETClient) get(ctx context.Context, path string, limit int64) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("observer request requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.http == nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "#\\\r\n") || limit <= 0 || limit > 4<<20 {
		return nil, fmt.Errorf("invalid internal observer request")
	}
	if c.check != nil {
		if err := c.check(ctx); err != nil {
			return nil, err
		}
	}
	// Check on every request, including a reused TLS connection.
	if now := time.Now(); now.Before(c.validFrom) || !now.Before(c.validUntil) {
		return nil, fmt.Errorf("observer certificate is outside its validity period")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot construct observer GET")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache, no-store")
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Do not wrap net/http errors: they carry endpoint/path information.
		return nil, fmt.Errorf("observer HTTPS GET failed")
	}
	defer response.Body.Close()
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || mediaErr != nil || mediaType != "application/json" || response.Header.Get("Content-Encoding") != "" || response.ContentLength > limit {
		return nil, fmt.Errorf("observer GET did not return a bounded JSON success response")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || int64(len(body)) > limit || !utf8.Valid(body) {
		return nil, fmt.Errorf("observer response is incomplete, oversized or invalid UTF-8")
	}
	if now := time.Now(); now.Before(c.validFrom) || !now.Before(c.validUntil) {
		return nil, fmt.Errorf("observer certificate expired during the response")
	}
	if c.check != nil {
		if err := c.check(ctx); err != nil {
			return nil, err
		}
	}
	return body, nil
}

// API schemas contain additional fields and nulls. Accept those, but reject
// duplicate members and excessive depth before decoding the selected fields.
// Neither parsing errors nor response bodies are returned to callers.
func decodeObservedJSON(body []byte, out any) error {
	typ := reflect.TypeOf(out)
	if !utf8.Valid(body) || typ == nil || typ.Kind() != reflect.Pointer || reflect.ValueOf(out).IsNil() {
		return fmt.Errorf("invalid observer JSON destination or encoding")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := observedJSONValue(d, typ.Elem(), 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("observer JSON has trailing content")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("observer JSON does not match the selected API fields")
	}
	return nil
}

func observedJSONValue(d *json.Decoder, typ reflect.Type, depth int) error {
	if depth > 32 {
		return fmt.Errorf("observer JSON exceeds the nesting limit")
	}
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	t, err := d.Token()
	if err != nil {
		return fmt.Errorf("invalid observer JSON")
	}
	switch t {
	case json.Delim('{'):
		// Incus/Traefik API records are extensible, so unknown fields remain
		// accepted. A spelling that aliases a selected Go struct field does not:
		// encoding/json would otherwise overwrite its exact-key value. Maps
		// retain case-sensitive application names and configuration keys.
		var fields map[string]reflect.Type
		if typ != nil && typ.Kind() == reflect.Struct {
			fields = make(map[string]reflect.Type)
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if !field.IsExported() {
					continue
				}
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = field.Name
				}
				fields[name] = field.Type
			}
		}
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return fmt.Errorf("observer JSON contains invalid or duplicate members")
			}
			seen[name] = true
			var child reflect.Type
			if fields != nil {
				child = fields[name]
				if child == nil {
					for exact := range fields {
						if strings.EqualFold(exact, name) {
							return fmt.Errorf("observer JSON contains a noncanonical field alias")
						}
					}
				}
			} else if typ != nil && typ.Kind() == reflect.Map {
				child = typ.Elem()
			}
			if err := observedJSONValue(d, child, depth+1); err != nil {
				return err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid observer JSON object")
		}
	case json.Delim('['):
		var element reflect.Type
		if typ != nil && (typ.Kind() == reflect.Array || typ.Kind() == reflect.Slice) {
			element = typ.Elem()
		}
		for d.More() {
			if err := observedJSONValue(d, element, depth+1); err != nil {
				return err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid observer JSON array")
		}
	case json.Delim('}'), json.Delim(']'):
		return fmt.Errorf("invalid observer JSON delimiter")
	}
	return nil
}
