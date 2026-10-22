// Package serviceauth authenticates registered services independently of console
// users. Host configuration owns grants; clients only receive opaque credentials.
package serviceauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnauthenticated = errors.New("service authentication required")
	ErrForbidden       = errors.New("service permission denied")
	ErrUnavailable     = errors.New("service authentication unavailable")
)

type Grant struct {
	Permission string   `json:"permission" yaml:"permission"`
	Workspace  string   `json:"workspace" yaml:"workspace"`
	Resources  []string `json:"resources" yaml:"resources"`
}

type Client struct {
	ID      string  `json:"id" yaml:"id"`
	Enabled bool    `json:"enabled" yaml:"enabled"`
	UID     int     `json:"uid" yaml:"uid"`
	GID     int     `json:"gid" yaml:"gid"`
	Grants  []Grant `json:"grants" yaml:"grants"`
}

type Config struct {
	Directory string   `json:"credentials_dir" yaml:"credentials_dir"`
	Clients   []Client `json:"clients" yaml:"clients"`
}

type Options struct {
	// LoadConfig must read host-controlled configuration, not workspace state.
	LoadConfig func() (Config, error)
	Lifetime   time.Duration
	Interval   time.Duration
	// OnError reports maintenance failures. Run keeps retrying after failures.
	OnError func(error)
}

type credential struct {
	digest  [sha256.Size]byte
	expires time.Time
	uid     int
	gid     int
}

type Manager struct {
	mu        sync.Mutex
	options   Options
	now       func() time.Time
	directory string
	tokens    map[string]credential
}

func New(options Options) (*Manager, error) {
	if options.LoadConfig == nil {
		return nil, errors.New("service authentication requires a configuration loader")
	}
	if options.Lifetime == 0 {
		options.Lifetime = 15 * time.Minute
	}
	if options.Interval == 0 {
		options.Interval = 30 * time.Second
	}
	if options.Lifetime <= 0 || options.Interval <= 0 || options.Interval >= options.Lifetime {
		return nil, errors.New("service maintenance interval must be positive and shorter than credential lifetime")
	}
	return &Manager{options: options, now: time.Now, tokens: make(map[string]credential)}, nil
}

// ValidateConfig rejects ambiguous grants and paths before any files are changed.
// Empty configuration disables the mechanism. Exact strings match all scopes;
// wildcard grants are deliberately unsupported.
func ValidateConfig(config Config) error {
	if config.Directory == "" && len(config.Clients) == 0 {
		return nil
	}
	if !filepath.IsAbs(config.Directory) || filepath.Clean(config.Directory) != config.Directory || filepath.Dir(config.Directory) == string(filepath.Separator) {
		return errors.New("service credentials directory must be a dedicated absolute path below a parent directory")
	}
	seen := make(map[string]bool)
	for _, client := range config.Clients {
		if !validID(client.ID) || seen[client.ID] {
			return errors.New("service clients require unique IDs containing only letters, digits, underscores and hyphens")
		}
		seen[client.ID] = true
		if client.UID < 0 || client.GID < 0 || uint64(client.UID) >= 1<<32-1 || uint64(client.GID) >= 1<<32-1 {
			return fmt.Errorf("service client %s requires valid Unix UID and GID", client.ID)
		}
		for _, grant := range client.Grants {
			if !validScope(grant.Permission) || !validScope(grant.Workspace) || len(grant.Resources) == 0 {
				return fmt.Errorf("service client %s requires explicit permission, workspace and resources", client.ID)
			}
			for _, resource := range grant.Resources {
				if !validScope(resource) {
					return fmt.Errorf("service client %s has an invalid resource scope", client.ID)
				}
			}
		}
	}
	return nil
}

func validID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validScope(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "*?\x00\r\n\t")
}

func (m *Manager) load() (Config, error) {
	config, err := m.options.LoadConfig()
	if err != nil {
		return Config{}, fmt.Errorf("%w: load host configuration: %w", ErrUnavailable, err)
	}
	if err := ValidateConfig(config); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return config, nil
}

// Reconcile renews live credentials without changing their values. Restart,
// expiry, missing delivery files or changed ownership always issue a new value;
// no existing delivery file is sufficient to establish a credential's validity.
func (m *Manager) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := m.load()
	if err != nil {
		return err
	}
	if m.directory != "" && config.Directory != m.directory {
		clear(m.tokens)
		if err := removeCredentials(m.directory, nil); err != nil {
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		m.directory = ""
	}
	if config.Directory == "" {
		return nil
	}
	if err := ensureRoot(config.Directory); err != nil {
		clear(m.tokens)
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	m.directory = config.Directory
	enabled := make(map[string]bool)
	for _, client := range config.Clients {
		if client.Enabled {
			enabled[client.ID] = true
		}
	}
	for id := range m.tokens {
		if !enabled[id] {
			delete(m.tokens, id)
		}
	}
	if err := removeCredentials(config.Directory, enabled); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	var failures []error
	for _, client := range config.Clients {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if !client.Enabled {
			continue
		}
		if err := m.reconcileClient(client); err != nil {
			delete(m.tokens, client.ID)
			failures = append(failures, fmt.Errorf("%w: client %s: %w", ErrUnavailable, client.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) reconcileClient(client Client) error {
	dir := filepath.Join(m.directory, client.ID)
	if err := ensureClientDirectory(dir, client); err != nil {
		return err
	}
	current, exists := m.tokens[client.ID]
	now := m.now()
	if exists && now.Before(current.expires) && current.uid == client.UID && current.gid == client.GID {
		body, found, err := readToken(dir, client)
		if err != nil {
			return err
		}
		if found && sha256.Sum256([]byte(strings.TrimSuffix(string(body), "\n"))) == current.digest {
			current.expires = now.Add(m.options.Lifetime)
			m.tokens[client.ID] = current
			return nil
		}
	}
	// Invalidate before delivery: a failed replacement must never extend old state.
	delete(m.tokens, client.ID)
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("generate service credential")
	}
	token := client.ID + "." + base64.RawURLEncoding.EncodeToString(random[:])
	if err := writeToken(dir, client, []byte(token+"\n")); err != nil {
		return err
	}
	m.tokens[client.ID] = credential{digest: sha256.Sum256([]byte(token)), expires: now.Add(m.options.Lifetime), uid: client.UID, gid: client.GID}
	return nil
}

// Authenticate re-reads host grants on every request. It neither renews tokens
// nor accepts an expired token to obtain a replacement.
func (m *Manager) Authenticate(ctx context.Context, token, permission, workspace, resource string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	config, err := m.load()
	if err != nil {
		return "", err
	}
	if len(token) > 128 {
		return "", ErrUnauthenticated
	}
	id, secret, found := strings.Cut(token, ".")
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(secret)
	if !found || !validID(id) || decodeErr != nil || len(decoded) != 32 {
		return "", ErrUnauthenticated
	}
	var client *Client
	for i := range config.Clients {
		if config.Clients[i].ID == id {
			client = &config.Clients[i]
			break
		}
	}
	if client == nil || !client.Enabled || config.Directory != m.directory {
		delete(m.tokens, id)
		return "", ErrUnauthenticated
	}
	current, exists := m.tokens[id]
	digest := sha256.Sum256([]byte(token))
	if !exists || !m.now().Before(current.expires) || client.UID != current.uid || client.GID != current.gid {
		delete(m.tokens, id)
		return "", ErrUnauthenticated
	}
	if subtle.ConstantTimeCompare(digest[:], current.digest[:]) != 1 {
		return "", ErrUnauthenticated
	}
	if validScope(permission) && validScope(workspace) && validScope(resource) {
		for _, grant := range client.Grants {
			if grant.Permission == permission && grant.Workspace == workspace {
				for _, allowed := range grant.Resources {
					if allowed == resource {
						return id, nil
					}
				}
			}
		}
	}
	return "", ErrForbidden
}

// Run maintains delivery even while a client is stopped. Individual failures
// fail closed and are retried on the next interval; cancellation returns nil.
func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(m.options.Interval)
	defer ticker.Stop()
	for {
		if err := m.Reconcile(ctx); err != nil && ctx.Err() == nil && m.options.OnError != nil {
			m.options.OnError(err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
