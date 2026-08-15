package modulestore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"github.com/anas-project/ANAS/internal/modulepackage"
	"github.com/anas-project/ANAS/internal/modulesource"
)

const (
	CatalogAPIVersion = "anas.module-catalog/v1"

	moduleArtifactType  = "application/vnd.anas.module.v1"
	moduleLayerType     = "application/vnd.anas.module.bundle.v1.tar+gzip"
	catalogArtifactType = "application/vnd.anas.module.catalog.v1"
	catalogLayerType    = "application/vnd.anas.module.catalog.v1+json"
	ociManifestType     = "application/vnd.oci.image.manifest.v1+json"

	maxManifestBytes = 4 << 20
	maxCatalogBytes  = 16 << 20
	maxBundleBytes   = 2 << 30
	maxBundleFiles   = 100000
)

var (
	moduleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	repositoryPattern = regexp.MustCompile(`^anas-module-[a-z0-9-]+$`)
	releasePattern    = regexp.MustCompile(`^(.+)-r([1-9][0-9]*)$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Catalog struct {
	APIVersion   string          `json:"api_version"`
	SourceCommit string          `json:"source_commit"`
	Modules      []CatalogModule `json:"modules"`
}

type CatalogModule struct {
	Module         string   `json:"module"`
	Repository     string   `json:"repository"`
	Platforms      []string `json:"platforms"`
	SharedContexts []string `json:"shared_contexts,omitempty"`
	Release        string   `json:"release"`
}

type CatalogResult struct {
	Catalog   Catalog `json:"catalog"`
	Reference string  `json:"reference"`
	OCIDigest string  `json:"oci_digest"`
}

type Version struct {
	Release  string `json:"release"`
	Version  string `json:"version"`
	Revision int    `json:"revision"`
	Current  bool   `json:"current"`
	parsed   *semver.Version
}

type Installation struct {
	Name               string                        `json:"name"`
	Release            string                        `json:"release"`
	Repository         string                        `json:"repository"`
	Reference          string                        `json:"reference"`
	ImmutableReference string                        `json:"immutable_reference"`
	OCIDigest          string                        `json:"oci_digest"`
	LayerDigest        string                        `json:"layer_digest"`
	ContentDigest      string                        `json:"content_digest"`
	BlobPath           string                        `json:"blob_path"`
	Path               string                        `json:"path"`
	Metadata           modulepackage.PackageMetadata `json:"-"`
}

type View struct {
	APIVersion    string                  `json:"api_version"`
	Digest        string                  `json:"digest"`
	ModuleRoot    string                  `json:"module_root"`
	Installations map[string]Installation `json:"installations"`
}

type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

type reference struct {
	Registry   string
	Repository string
	Reference  string
}

func (r reference) String() string {
	value := "oci://" + r.Registry + "/" + r.Repository
	if r.Reference == "" {
		return value
	}
	if strings.HasPrefix(r.Reference, "sha256:") {
		return value + "@" + r.Reference
	}
	return value + ":" + r.Reference
}

type CredentialFunc func(registry string) (username, password string, ok bool)

type Client struct {
	HTTPClient  *http.Client
	PlainHTTP   bool
	Credentials CredentialFunc
	UserAgent   string

	mu     sync.Mutex
	tokens map[string]string
}

func NewClient() *Client {
	return &Client{
		HTTPClient:  &http.Client{Timeout: 2 * time.Minute},
		Credentials: dockerCredential,
		UserAgent:   "anas-module-client/1",
		tokens:      map[string]string{},
	}
}

type Store struct {
	CacheRoot string
	Client    *Client
}

func New(cacheRoot string) (*Store, error) {
	if strings.TrimSpace(cacheRoot) == "" {
		var err error
		cacheRoot, err = DefaultCacheRoot()
		if err != nil {
			return nil, err
		}
	}
	root, err := filepath.Abs(cacheRoot)
	if err != nil {
		return nil, err
	}
	return &Store{CacheRoot: root, Client: NewClient()}, nil
}

func DefaultCacheRoot() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("ANAS_MODULE_CACHE")); configured != "" {
		return filepath.Abs(configured)
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "anas", "modules"), nil
}

func parseReference(raw string, requireReference bool) (reference, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "oci://") {
		return reference{}, fmt.Errorf("OCI reference %q must start with oci://", raw)
	}
	value := strings.TrimPrefix(raw, "oci://")
	slash := strings.IndexByte(value, '/')
	if slash <= 0 || slash == len(value)-1 {
		return reference{}, fmt.Errorf("invalid OCI reference %q", raw)
	}
	r := reference{Registry: value[:slash]}
	remainder := value[slash+1:]
	if at := strings.LastIndexByte(remainder, '@'); at >= 0 {
		r.Repository, r.Reference = remainder[:at], remainder[at+1:]
	} else if colon := strings.LastIndexByte(remainder, ':'); colon > strings.LastIndexByte(remainder, '/') {
		r.Repository, r.Reference = remainder[:colon], remainder[colon+1:]
	} else {
		r.Repository = remainder
	}
	if r.Repository == "" || strings.Contains(r.Repository, "..") || strings.HasPrefix(r.Repository, "/") {
		return reference{}, fmt.Errorf("invalid OCI repository in %q", raw)
	}
	if requireReference && r.Reference == "" {
		return reference{}, fmt.Errorf("OCI reference %q has no tag or digest", raw)
	}
	return r, nil
}

func (c *Client) client() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Client) scheme() string {
	if c != nil && c.PlainHTTP {
		return "http"
	}
	return "https"
}

func (c *Client) get(ctx context.Context, ref reference, endpoint, accept string) ([]byte, http.Header, error) {
	target := c.scheme() + "://" + ref.Registry + "/v2/" + ref.Repository + endpoint
	return c.getURL(ctx, ref, target, accept)
}

func (c *Client) getURL(ctx context.Context, ref reference, target, accept string) ([]byte, http.Header, error) {
	request := func(token string, basic bool) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if c != nil && c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else if basic && c != nil && c.Credentials != nil {
			if username, password, ok := c.Credentials(ref.Registry); ok {
				req.SetBasicAuth(username, password)
			}
		}
		return c.client().Do(req)
	}

	cacheKey := ref.Registry + "/" + ref.Repository
	token := c.cachedToken(cacheKey)
	resp, err := request(token, false)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		_ = resp.Body.Close()
		scheme, params := parseChallenge(challenge)
		switch strings.ToLower(scheme) {
		case "bearer":
			token, err = c.fetchToken(ctx, ref, params)
			if err != nil {
				return nil, nil, err
			}
			c.storeToken(cacheKey, token)
			resp, err = request(token, false)
		case "basic":
			resp, err = request("", true)
		default:
			return nil, nil, fmt.Errorf("registry %s returned unsupported authentication challenge %q", ref.Registry, challenge)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	defer resp.Body.Close()
	limit := int64(maxBundleBytes + 1)
	if strings.Contains(target, "/manifests/") || strings.Contains(target, "/tags/list") {
		limit = maxManifestBytes + 1
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if len(message) > 512 {
			message = message[:512]
		}
		return nil, resp.Header.Clone(), fmt.Errorf("registry GET %s: HTTP %d: %s", target, resp.StatusCode, message)
	}
	if int64(len(body)) >= limit {
		return nil, resp.Header.Clone(), fmt.Errorf("registry response from %s exceeds size limit", target)
	}
	return body, resp.Header.Clone(), nil
}

func (c *Client) cachedToken(key string) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens[key]
}

func (c *Client) storeToken(key, token string) {
	if c == nil || token == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokens == nil {
		c.tokens = map[string]string{}
	}
	c.tokens[key] = token
}

func parseChallenge(raw string) (string, map[string]string) {
	raw = strings.TrimSpace(raw)
	space := strings.IndexByte(raw, ' ')
	if space < 0 {
		return raw, map[string]string{}
	}
	params := map[string]string{}
	for _, match := range regexp.MustCompile(`([A-Za-z][A-Za-z0-9_-]*)="([^"]*)"`).FindAllStringSubmatch(raw[space+1:], -1) {
		params[strings.ToLower(match[1])] = match[2]
	}
	return raw[:space], params
}

func (c *Client) fetchToken(ctx context.Context, ref reference, params map[string]string) (string, error) {
	realm := params["realm"]
	if realm == "" {
		return "", errors.New("registry bearer challenge has no realm")
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" && !(c != nil && c.PlainHTTP && u.Scheme == "http") {
		return "", fmt.Errorf("refusing insecure registry token realm %q", realm)
	}
	query := u.Query()
	if service := params["service"]; service != "" {
		query.Set("service", service)
	}
	scope := params["scope"]
	if scope == "" {
		scope = "repository:" + ref.Repository + ":pull"
	}
	query.Set("scope", scope)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	if c != nil && c.Credentials != nil {
		if username, password, ok := c.Credentials(ref.Registry); ok {
			req.SetBasicAuth(username, password)
		}
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("registry token request: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var document struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return "", err
	}
	if document.Token == "" {
		document.Token = document.AccessToken
	}
	if document.Token == "" {
		return "", errors.New("registry token response contains no token")
	}
	return document.Token, nil
}

func dockerCredential(registry string) (string, string, bool) {
	configRoot := strings.TrimSpace(os.Getenv("DOCKER_CONFIG"))
	if configRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", false
		}
		configRoot = filepath.Join(home, ".docker")
	}
	body, err := os.ReadFile(filepath.Join(configRoot, "config.json"))
	if err != nil {
		return "", "", false
	}
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if json.Unmarshal(body, &cfg) != nil {
		return "", "", false
	}
	for _, key := range []string{registry, "https://" + registry, "http://" + registry} {
		entry, ok := cfg.Auths[key]
		if !ok || entry.Auth == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			continue
		}
		username, password, found := strings.Cut(string(decoded), ":")
		if found {
			return username, password, true
		}
	}
	return "", "", false
}

func (c *Client) manifest(ctx context.Context, raw string) (reference, Manifest, string, error) {
	ref, err := parseReference(raw, true)
	if err != nil {
		return reference{}, Manifest{}, "", err
	}
	body, headers, err := c.get(ctx, ref, "/manifests/"+url.PathEscape(ref.Reference), ociManifestType)
	if err != nil {
		return reference{}, Manifest{}, "", err
	}
	digest := sha256Digest(body)
	if remote := strings.TrimSpace(headers.Get("Docker-Content-Digest")); remote != "" && remote != digest {
		return reference{}, Manifest{}, "", fmt.Errorf("manifest digest mismatch: header=%s actual=%s", remote, digest)
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return reference{}, Manifest{}, "", err
	}
	if manifest.SchemaVersion != 2 || manifest.MediaType != ociManifestType {
		return reference{}, Manifest{}, "", fmt.Errorf("unsupported OCI manifest schema/media type")
	}
	return ref, manifest, digest, nil
}

func (c *Client) blob(ctx context.Context, ref reference, descriptor Descriptor, maxBytes int64) ([]byte, error) {
	if !digestPattern.MatchString(descriptor.Digest) || descriptor.Size < 0 || descriptor.Size > maxBytes {
		return nil, fmt.Errorf("invalid OCI layer descriptor")
	}
	body, _, err := c.get(ctx, ref, "/blobs/"+descriptor.Digest, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != descriptor.Size {
		return nil, fmt.Errorf("OCI blob size mismatch: descriptor=%d actual=%d", descriptor.Size, len(body))
	}
	if actual := sha256Digest(body); actual != descriptor.Digest {
		return nil, fmt.Errorf("OCI blob digest mismatch: descriptor=%s actual=%s", descriptor.Digest, actual)
	}
	return body, nil
}

func sha256Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (s *Store) FetchCatalog(ctx context.Context, profile modulesource.Profile) (CatalogResult, error) {
	client := s.Client
	if client == nil {
		client = NewClient()
	}
	refs := append([]string{profile.Catalog}, profile.CatalogMirrors...)
	var failures []string
	for _, raw := range refs {
		ref, manifest, digest, err := client.manifest(ctx, raw)
		if err != nil {
			failures = append(failures, raw+": "+err.Error())
			continue
		}
		if manifest.ArtifactType != catalogArtifactType || len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != catalogLayerType {
			failures = append(failures, raw+": unexpected Module catalog artifact type")
			continue
		}
		body, err := client.blob(ctx, ref, manifest.Layers[0], maxCatalogBytes)
		if err != nil {
			failures = append(failures, raw+": "+err.Error())
			continue
		}
		var catalog Catalog
		if err := json.Unmarshal(body, &catalog); err != nil {
			failures = append(failures, raw+": "+err.Error())
			continue
		}
		if err := validateCatalog(catalog); err != nil {
			failures = append(failures, raw+": "+err.Error())
			continue
		}
		return CatalogResult{Catalog: catalog, Reference: ref.String(), OCIDigest: digest}, nil
	}
	return CatalogResult{}, fmt.Errorf("all Module catalog sources failed: %s", strings.Join(failures, "; "))
}

func validateCatalog(catalog Catalog) error {
	if catalog.APIVersion != CatalogAPIVersion || len(catalog.Modules) == 0 {
		return fmt.Errorf("invalid Module catalog api_version or empty module list")
	}
	seen := map[string]bool{}
	for _, module := range catalog.Modules {
		if !moduleNamePattern.MatchString(module.Module) || !repositoryPattern.MatchString(module.Repository) || seen[module.Module] {
			return fmt.Errorf("invalid or duplicate Module catalog entry %q", module.Module)
		}
		seen[module.Module] = true
		if _, err := ParseRelease(module.Release); err != nil {
			return fmt.Errorf("module %s current release: %w", module.Module, err)
		}
		if len(module.Platforms) == 0 {
			return fmt.Errorf("module %s has no supported platforms", module.Module)
		}
	}
	return nil
}

func ParseRelease(raw string) (Version, error) {
	match := releasePattern.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return Version{}, fmt.Errorf("invalid Module release %q; expected <semver>-r<N>", raw)
	}
	parsed, err := semver.NewVersion(match[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid Module release %q: %w", raw, err)
	}
	revision, err := strconv.Atoi(match[2])
	if err != nil || revision < 1 {
		return Version{}, fmt.Errorf("invalid Module revision in %q", raw)
	}
	return Version{Release: raw, Version: match[1], Revision: revision, parsed: parsed}, nil
}

func catalogModule(catalog Catalog, name string) (CatalogModule, bool) {
	for _, module := range catalog.Modules {
		if module.Module == name {
			return module, true
		}
	}
	return CatalogModule{}, false
}

func repositoryReference(template string, module CatalogModule) (string, error) {
	ref, err := parseReference(template, false)
	if err != nil {
		return "", err
	}
	slash := strings.LastIndexByte(ref.Repository, '/')
	if slash < 0 {
		ref.Repository = module.Repository
	} else {
		ref.Repository = ref.Repository[:slash+1] + module.Repository
	}
	ref.Reference = ""
	return ref.String(), nil
}

func (s *Store) Versions(ctx context.Context, profile modulesource.Profile, name string) ([]Version, CatalogResult, error) {
	catalogResult, err := s.FetchCatalog(ctx, profile)
	if err != nil {
		return nil, CatalogResult{}, err
	}
	module, ok := catalogModule(catalogResult.Catalog, name)
	if !ok {
		return nil, catalogResult, fmt.Errorf("unknown Module %q", name)
	}
	templates := append([]string{profile.Repository}, profile.Mirrors...)
	var tags []string
	var failures []string
	for _, template := range templates {
		repository, refErr := repositoryReference(template, module)
		if refErr != nil {
			failures = append(failures, refErr.Error())
			continue
		}
		tags, refErr = s.listTags(ctx, repository)
		if refErr == nil {
			break
		}
		failures = append(failures, repository+": "+refErr.Error())
	}
	if tags == nil {
		return nil, catalogResult, fmt.Errorf("all Module repositories failed: %s", strings.Join(failures, "; "))
	}
	seen := map[string]bool{}
	versions := []Version{}
	for _, tag := range tags {
		version, parseErr := ParseRelease(tag)
		if parseErr != nil || seen[tag] {
			continue
		}
		seen[tag] = true
		version.Current = tag == module.Release
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		if cmp := versions[i].parsed.Compare(versions[j].parsed); cmp != 0 {
			return cmp > 0
		}
		return versions[i].Revision > versions[j].Revision
	})
	return versions, catalogResult, nil
}

func (s *Store) listTags(ctx context.Context, rawRepository string) ([]string, error) {
	ref, err := parseReference(rawRepository, false)
	if err != nil {
		return nil, err
	}
	client := s.Client
	if client == nil {
		client = NewClient()
	}
	target := client.scheme() + "://" + ref.Registry + "/v2/" + ref.Repository + "/tags/list?n=100"
	seenPages := map[string]bool{}
	seenTags := map[string]bool{}
	var tags []string
	for target != "" {
		if seenPages[target] {
			return nil, errors.New("registry tag pagination loop")
		}
		seenPages[target] = true
		body, headers, err := client.getURL(ctx, ref, target, "application/json")
		if err != nil {
			return nil, err
		}
		var page struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		for _, tag := range page.Tags {
			if !seenTags[tag] {
				seenTags[tag] = true
				tags = append(tags, tag)
			}
		}
		target, err = nextLink(target, headers.Get("Link"))
		if err != nil {
			return nil, err
		}
	}
	return tags, nil
}

func nextLink(current, header string) (string, error) {
	if strings.TrimSpace(header) == "" {
		return "", nil
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		left, right := strings.IndexByte(part, '<'), strings.IndexByte(part, '>')
		if left < 0 || right <= left+1 {
			return "", fmt.Errorf("invalid registry Link header %q", header)
		}
		base, err := url.Parse(current)
		if err != nil {
			return "", err
		}
		next, err := url.Parse(part[left+1 : right])
		if err != nil {
			return "", err
		}
		resolved := base.ResolveReference(next)
		if resolved.Host != base.Host || resolved.Scheme != base.Scheme {
			return "", errors.New("registry pagination attempted to change host")
		}
		return resolved.String(), nil
	}
	return "", nil
}

func (s *Store) Install(ctx context.Context, profile modulesource.Profile, name, release, expectedOCIDigest string) (Installation, error) {
	if !moduleNamePattern.MatchString(name) {
		return Installation{}, fmt.Errorf("invalid Module name %q", name)
	}
	if _, err := ParseRelease(release); err != nil {
		return Installation{}, err
	}
	if expectedOCIDigest != "" && !digestPattern.MatchString(expectedOCIDigest) {
		return Installation{}, fmt.Errorf("invalid expected OCI digest %q", expectedOCIDigest)
	}
	catalogResult, err := s.FetchCatalog(ctx, profile)
	if err != nil {
		return Installation{}, err
	}
	module, ok := catalogModule(catalogResult.Catalog, name)
	if !ok {
		return Installation{}, fmt.Errorf("unknown Module %q", name)
	}
	templates := append([]string{profile.Repository}, profile.Mirrors...)
	var failures []string
	for _, template := range templates {
		repository, refErr := repositoryReference(template, module)
		if refErr != nil {
			failures = append(failures, refErr.Error())
			continue
		}
		result, refErr := s.installFrom(ctx, repository+":"+release, module, release, expectedOCIDigest)
		if refErr == nil {
			return result, nil
		}
		failures = append(failures, repository+": "+refErr.Error())
	}
	return Installation{}, fmt.Errorf("all Module artifact sources failed: %s", strings.Join(failures, "; "))
}

// InstallLocked restores an artifact from an immutable lock identity. It does
// not consult the moving discovery catalog or the release tag: an older locked
// Module must remain recoverable after it stops being the catalog's current
// release (or is removed from catalog discovery altogether). The configured
// profile contributes only equivalent Registry locations for the same digest.
func (s *Store) InstallLocked(ctx context.Context, profile modulesource.Profile, immutableReference, name, release, repository, expectedOCIDigest string) (Installation, error) {
	if !moduleNamePattern.MatchString(name) || !repositoryPattern.MatchString(repository) {
		return Installation{}, fmt.Errorf("invalid locked Module identity %q / %q", name, repository)
	}
	if _, err := ParseRelease(release); err != nil {
		return Installation{}, err
	}
	if !digestPattern.MatchString(expectedOCIDigest) {
		return Installation{}, fmt.Errorf("invalid expected OCI digest %q", expectedOCIDigest)
	}
	locked, err := parseReference(immutableReference, true)
	if err != nil {
		return Installation{}, err
	}
	if locked.Reference != expectedOCIDigest {
		return Installation{}, fmt.Errorf("locked OCI reference digest does not match oci_digest")
	}

	candidates := []string{locked.String()}
	module := CatalogModule{Module: name, Repository: repository}
	for _, template := range append([]string{profile.Repository}, profile.Mirrors...) {
		base, refErr := repositoryReference(template, module)
		if refErr != nil {
			continue
		}
		candidates = append(candidates, base+"@"+expectedOCIDigest)
	}
	seen := map[string]bool{}
	var failures []string
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		installation, installErr := s.installFrom(ctx, candidate, module, release, expectedOCIDigest)
		if installErr == nil {
			return installation, nil
		}
		failures = append(failures, candidate+": "+installErr.Error())
	}
	return Installation{}, fmt.Errorf("all locked Module artifact sources failed: %s", strings.Join(failures, "; "))
}

func (s *Store) installFrom(ctx context.Context, raw string, catalog CatalogModule, expectedRelease, expectedOCIDigest string) (Installation, error) {
	client := s.Client
	if client == nil {
		client = NewClient()
	}
	ref, manifest, manifestDigest, err := client.manifest(ctx, raw)
	if err != nil {
		return Installation{}, err
	}
	if expectedOCIDigest != "" && manifestDigest != expectedOCIDigest {
		return Installation{}, fmt.Errorf("OCI manifest digest mismatch: lock=%s registry=%s", expectedOCIDigest, manifestDigest)
	}
	if manifest.ArtifactType != moduleArtifactType || len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != moduleLayerType {
		return Installation{}, fmt.Errorf("unexpected Module artifact or layer type")
	}
	bundle, err := client.blob(ctx, ref, manifest.Layers[0], maxBundleBytes)
	if err != nil {
		return Installation{}, err
	}
	if err := os.MkdirAll(filepath.Join(s.CacheRoot, "blobs", "sha256"), 0700); err != nil {
		return Installation{}, err
	}
	if err := os.MkdirAll(filepath.Join(s.CacheRoot, "unpacked", "sha256"), 0700); err != nil {
		return Installation{}, err
	}
	blobPath := filepath.Join(s.CacheRoot, "blobs", "sha256", strings.TrimPrefix(manifestDigest, "sha256:"))
	if err := atomicWriteFile(blobPath, bundle, 0600); err != nil {
		return Installation{}, err
	}
	temp, err := os.MkdirTemp(filepath.Join(s.CacheRoot, "unpacked", "sha256"), ".install-*")
	if err != nil {
		return Installation{}, err
	}
	defer os.RemoveAll(temp)
	if err := extractBundle(bundle, temp); err != nil {
		return Installation{}, err
	}
	metadata, err := modulepackage.VerifyUnpacked(temp)
	if err != nil {
		return Installation{}, err
	}
	if metadata.Name != catalog.Module || metadata.Repository != catalog.Repository || metadata.Release != expectedRelease {
		return Installation{}, fmt.Errorf("Module package identity does not match catalog/reference")
	}
	if metadata.Compatibility.ModuleAPI != "anas.module/v1" {
		return Installation{}, fmt.Errorf("unsupported Module API %q", metadata.Compatibility.ModuleAPI)
	}
	if info, err := os.Stat(filepath.Join(temp, "contracts")); err != nil || !info.IsDir() {
		return Installation{}, errors.New("Module package does not contain runtime contracts")
	}
	contentHex := strings.TrimPrefix(metadata.ContentDigest, "sha256:")
	if len(contentHex) != 64 {
		return Installation{}, fmt.Errorf("invalid content digest %q", metadata.ContentDigest)
	}
	installedPath := filepath.Join(s.CacheRoot, "unpacked", "sha256", contentHex)
	if info, statErr := os.Stat(installedPath); statErr == nil && info.IsDir() {
		existing, verifyErr := modulepackage.VerifyUnpacked(installedPath)
		if verifyErr != nil || existing.ContentDigest != metadata.ContentDigest {
			return Installation{}, fmt.Errorf("cached Module tree %s is corrupt", installedPath)
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return Installation{}, statErr
	} else if err := os.Rename(temp, installedPath); err != nil {
		if existing, verifyErr := modulepackage.VerifyUnpacked(installedPath); verifyErr != nil || existing.ContentDigest != metadata.ContentDigest {
			return Installation{}, err
		}
	}
	installation := Installation{
		Name: metadata.Name, Release: metadata.Release, Repository: metadata.Repository,
		Reference:          ref.String(),
		ImmutableReference: (reference{Registry: ref.Registry, Repository: ref.Repository, Reference: manifestDigest}).String(),
		OCIDigest:          manifestDigest, LayerDigest: manifest.Layers[0].Digest,
		ContentDigest: metadata.ContentDigest, BlobPath: blobPath, Path: installedPath, Metadata: metadata,
	}
	if err := s.saveInstallationRecord(installation); err != nil {
		return Installation{}, err
	}
	return installation, nil
}

func (s *Store) saveInstallationRecord(installation Installation) error {
	root := filepath.Join(s.CacheRoot, "records", "sha256")
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(installation, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return atomicWriteFile(filepath.Join(root, strings.TrimPrefix(installation.OCIDigest, "sha256:")+".json"), body, 0600)
}

func (s *Store) Cached(ociDigest string) (Installation, bool, error) {
	if !digestPattern.MatchString(ociDigest) {
		return Installation{}, false, fmt.Errorf("invalid OCI digest %q", ociDigest)
	}
	recordPath := filepath.Join(s.CacheRoot, "records", "sha256", strings.TrimPrefix(ociDigest, "sha256:")+".json")
	body, err := os.ReadFile(recordPath)
	if os.IsNotExist(err) {
		return Installation{}, false, nil
	}
	if err != nil {
		return Installation{}, false, err
	}
	var installation Installation
	if err := json.Unmarshal(body, &installation); err != nil {
		return Installation{}, false, fmt.Errorf("cached Module record %s: %w", recordPath, err)
	}
	if installation.OCIDigest != ociDigest || !digestPattern.MatchString(installation.LayerDigest) ||
		!digestPattern.MatchString(installation.ContentDigest) || !moduleNamePattern.MatchString(installation.Name) ||
		!repositoryPattern.MatchString(installation.Repository) {
		return Installation{}, false, fmt.Errorf("cached Module record %s is invalid", recordPath)
	}
	if _, err := ParseRelease(installation.Release); err != nil {
		return Installation{}, false, fmt.Errorf("cached Module record %s is invalid", recordPath)
	}
	immutable, err := parseReference(installation.ImmutableReference, true)
	if err != nil || immutable.Reference != ociDigest {
		return Installation{}, false, fmt.Errorf("cached Module record %s has an invalid immutable reference", recordPath)
	}
	expectedBlobPath := filepath.Join(s.CacheRoot, "blobs", "sha256", strings.TrimPrefix(ociDigest, "sha256:"))
	expectedTreePath := filepath.Join(s.CacheRoot, "unpacked", "sha256", strings.TrimPrefix(installation.ContentDigest, "sha256:"))
	if filepath.Clean(installation.BlobPath) != expectedBlobPath || filepath.Clean(installation.Path) != expectedTreePath {
		return Installation{}, false, fmt.Errorf("cached Module record %s points outside its content-addressed paths", recordPath)
	}
	bundle, err := os.ReadFile(installation.BlobPath)
	if err != nil || sha256Digest(bundle) != installation.LayerDigest {
		return Installation{}, false, fmt.Errorf("cached Module blob for %s is missing or corrupt", ociDigest)
	}
	metadata, err := modulepackage.VerifyUnpacked(installation.Path)
	if err != nil || metadata.ContentDigest != installation.ContentDigest || metadata.Name != installation.Name ||
		metadata.Release != installation.Release || metadata.Repository != installation.Repository ||
		metadata.Compatibility.ModuleAPI != "anas.module/v1" {
		return Installation{}, false, fmt.Errorf("cached Module tree for %s is missing or corrupt", ociDigest)
	}
	installation.Metadata = metadata
	return installation, true, nil
}

func (s *Store) BuildView(installations map[string]Installation) (View, error) {
	if len(installations) == 0 {
		return View{}, errors.New("cannot build an empty Module view")
	}
	names := make([]string, 0, len(installations))
	for name := range installations {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		installation := installations[name]
		if installation.Name != name || !digestPattern.MatchString(installation.OCIDigest) || !digestPattern.MatchString(installation.ContentDigest) {
			return View{}, fmt.Errorf("invalid installation record for Module %s", name)
		}
		metadata, err := modulepackage.VerifyUnpacked(installation.Path)
		if err != nil || metadata.Name != name || metadata.ContentDigest != installation.ContentDigest {
			return View{}, fmt.Errorf("cannot verify installed Module %s", name)
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", name, installation.OCIDigest, installation.ContentDigest)
	}
	viewDigest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	viewsRoot := filepath.Join(s.CacheRoot, "views", "sha256")
	if err := os.MkdirAll(viewsRoot, 0700); err != nil {
		return View{}, err
	}
	target := filepath.Join(viewsRoot, strings.TrimPrefix(viewDigest, "sha256:"))
	moduleRoot := filepath.Join(target, "modules")
	view := View{APIVersion: "anas.module-view/v1", Digest: viewDigest, ModuleRoot: moduleRoot, Installations: installations}
	contractSources := map[string]string{}
	contractDigests := map[string]string{}
	for _, name := range names {
		installation := installations[name]
		entries, err := os.ReadDir(filepath.Join(installation.Path, "contracts"))
		if err != nil {
			return View{}, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			source := filepath.Join(installation.Path, "contracts", entry.Name())
			digest, err := treeDigest(source)
			if err != nil {
				return View{}, err
			}
			if previous, ok := contractDigests[entry.Name()]; ok {
				if previous != digest {
					return View{}, fmt.Errorf("installed Modules carry conflicting %s contract definitions", entry.Name())
				}
				continue
			}
			contractDigests[entry.Name()] = digest
			contractSources[entry.Name()] = source
		}
	}
	if info, err := os.Stat(moduleRoot); err == nil && info.IsDir() {
		if err := verifyExistingView(target, view, names, contractSources); err != nil {
			return View{}, err
		}
		return view, nil
	}
	temp, err := os.MkdirTemp(viewsRoot, ".view-*")
	if err != nil {
		return View{}, err
	}
	defer os.RemoveAll(temp)
	if err := os.MkdirAll(filepath.Join(temp, "modules"), 0755); err != nil {
		return View{}, err
	}
	if err := os.MkdirAll(filepath.Join(temp, "contracts"), 0755); err != nil {
		return View{}, err
	}
	for _, name := range names {
		installation := installations[name]
		if err := os.Symlink(installation.Path, filepath.Join(temp, "modules", name)); err != nil {
			return View{}, err
		}
	}
	contractNames := make([]string, 0, len(contractSources))
	for name := range contractSources {
		contractNames = append(contractNames, name)
	}
	sort.Strings(contractNames)
	for _, name := range contractNames {
		if err := os.Symlink(contractSources[name], filepath.Join(temp, "contracts", name)); err != nil {
			return View{}, err
		}
	}
	body, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return View{}, err
	}
	if err := os.WriteFile(filepath.Join(temp, "view.json"), append(body, '\n'), 0600); err != nil {
		return View{}, err
	}
	if err := os.Rename(temp, target); err != nil {
		if verifyErr := verifyExistingView(target, view, names, contractSources); verifyErr != nil {
			return View{}, err
		}
	}
	return view, nil
}

func verifyExistingView(target string, expected View, moduleNames []string, contractSources map[string]string) error {
	verifyLinks := func(root string, names []string, sources map[string]string) error {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != len(names) {
			return errors.New("entry set does not match")
		}
		for index, name := range names {
			if entries[index].Name() != name || entries[index].Type()&os.ModeSymlink == 0 {
				return errors.New("entry set does not match")
			}
			linked, linkErr := filepath.EvalSymlinks(filepath.Join(root, name))
			source, sourceErr := filepath.EvalSymlinks(sources[name])
			if linkErr != nil || sourceErr != nil || linked != source {
				return errors.New("link target does not match")
			}
		}
		return nil
	}
	moduleSources := make(map[string]string, len(moduleNames))
	for _, name := range moduleNames {
		moduleSources[name] = expected.Installations[name].Path
	}
	contractNames := make([]string, 0, len(contractSources))
	for name := range contractSources {
		contractNames = append(contractNames, name)
	}
	sort.Strings(contractNames)
	if err := verifyLinks(filepath.Join(target, "modules"), moduleNames, moduleSources); err != nil {
		return fmt.Errorf("cached Module view %s is corrupt: modules %v", target, err)
	}
	if err := verifyLinks(filepath.Join(target, "contracts"), contractNames, contractSources); err != nil {
		return fmt.Errorf("cached Module view %s is corrupt: contracts %v", target, err)
	}
	want, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		return err
	}
	want = append(want, '\n')
	actual, err := os.ReadFile(filepath.Join(target, "view.json"))
	if err != nil || !bytes.Equal(actual, want) {
		return fmt.Errorf("cached Module view %s has invalid metadata", target)
	}
	return nil
}

func treeDigest(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			paths = append(paths, current)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, current := range paths {
		rel, _ := filepath.Rel(root, current)
		body, err := os.ReadFile(current)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%s\x00", filepath.ToSlash(rel))
		_, _ = hash.Write(body)
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func atomicWriteFile(target string, body []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, body) {
		return nil
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".blob-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, target)
}

func extractBundle(body []byte, root string) error {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	count := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if count > maxBundleFiles || header.Size < 0 || total+header.Size > maxBundleBytes {
			return errors.New("Module bundle exceeds extraction limits")
		}
		total += header.Size
		name := strings.TrimSuffix(header.Name, "/")
		clean := path.Clean(name)
		if name == "" || clean == "." || clean != name || path.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "\\") {
			return fmt.Errorf("unsafe Module archive path %q", header.Name)
		}
		if seen[clean] {
			return fmt.Errorf("duplicate Module archive path %q", clean)
		}
		seen[clean] = true
		target := filepath.Join(root, filepath.FromSlash(clean))
		switch header.Typeflag {
		case tar.TypeDir:
			mode := os.FileMode(header.Mode) & 0755
			if mode == 0 {
				mode = 0755
			}
			if err := os.MkdirAll(target, mode); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode) & 0755
			if mode == 0 {
				mode = 0644
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			written, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil || written != header.Size {
				if copyErr != nil {
					return copyErr
				}
				return io.ErrUnexpectedEOF
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported Module archive entry %q type %d", header.Name, header.Typeflag)
		}
	}
	return nil
}
