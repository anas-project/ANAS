package incusprovision

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/securefs"
)

type State struct {
	Schema         string                         `json:"schema"`
	ObserverScopes map[string]ObserverScopeRecord `json:"observer_scopes,omitempty"`
	UpdatedAt      time.Time                      `json:"updated_at"`
	Ownership      Ownership                      `json:"ownership"`
	Credential     *Credential                    `json:"credential,omitempty"`
	Bundle         *ConnectionBundle              `json:"bundle,omitempty"`
	Receipts       []Receipt                      `json:"receipts,omitempty"`
	Intents        []EffectIntent                 `json:"intents,omitempty"`
	Disabled       bool                           `json:"disabled,omitempty"`
}

type Ownership struct {
	ID                      string   `json:"id,omitempty"`
	PackagesInstalledByANAS bool     `json:"packages_installed_by_anas,omitempty"`
	ManagedPackages         []string `json:"managed_packages,omitempty"`
	IncusServiceByANAS      bool     `json:"incus_service_by_anas,omitempty"`
	ExternalDaemonPreserved bool     `json:"external_daemon_preserved,omitempty"`
	StoragePool             string   `json:"storage_pool,omitempty"`
	StoragePoolDriver       string   `json:"storage_pool_driver,omitempty"`
	DockerNetwork           string   `json:"docker_network,omitempty"`
	DockerNetworkID         string   `json:"docker_network_id,omitempty"`
	ControlSubnet           string   `json:"control_subnet,omitempty"`
	ControlGateway          string   `json:"control_gateway,omitempty"`
	ControlBridge           string   `json:"control_bridge,omitempty"`
	ControlInterfaceName    string   `json:"control_interface_name,omitempty"`
	ControlInterfaceIndex   int      `json:"control_interface_index,omitempty"`
	FirewallRules           bool     `json:"firewall_rules,omitempty"`
	RelayService            bool     `json:"relay_service,omitempty"`
	ManagementTrust         string   `json:"management_trust,omitempty"`
	ConnectionBundle        bool     `json:"connection_bundle,omitempty"`
}

type Credential struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Certificate string `json:"certificate_pem"`
	PrivateKey  string `json:"private_key_pem"`
}

type ConnectionBundle struct {
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

func (c Credential) String() string {
	if c.Fingerprint == "" {
		return "incusprovision.Credential(<empty>)"
	}
	return "incusprovision.Credential(" + c.Fingerprint + ", redacted)"
}

func (c Credential) GoString() string { return c.String() }

func (b ConnectionBundle) String() string {
	if b.ControlNetwork == "" {
		return "incusprovision.ConnectionBundle(<empty>)"
	}
	return "incusprovision.ConnectionBundle(" + b.ControlNetwork + ", redacted)"
}

func (b ConnectionBundle) GoString() string { return b.String() }

type Receipt struct {
	Step      string    `json:"step"`
	Digest    string    `json:"digest"`
	IntentID  string    `json:"intent_id,omitempty"`
	Status    string    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type EffectIntent struct {
	ID          string    `json:"id"`
	OwnershipID string    `json:"ownership_id"`
	Step        string    `json:"step"`
	Phase       Phase     `json:"phase"`
	Digest      string    `json:"digest"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type PublicState struct {
	Schema                string          `json:"schema"`
	UpdatedAt             time.Time       `json:"updated_at,omitempty"`
	Ownership             PublicOwnership `json:"ownership"`
	CredentialFingerprint string          `json:"credential_fingerprint,omitempty"`
	BundlePersisted       bool            `json:"bundle_persisted,omitempty"`
	ReceiptCount          int             `json:"receipt_count"`
	PendingIntents        []EffectIntent  `json:"pending_intents,omitempty"`
	Disabled              bool            `json:"disabled,omitempty"`
}

type PublicOwnership struct {
	ID                      string   `json:"id,omitempty"`
	PackagesInstalledByANAS bool     `json:"packages_installed_by_anas,omitempty"`
	ManagedPackages         []string `json:"managed_packages,omitempty"`
	IncusServiceByANAS      bool     `json:"incus_service_by_anas,omitempty"`
	ExternalDaemonPreserved bool     `json:"external_daemon_preserved,omitempty"`
	StoragePool             string   `json:"storage_pool,omitempty"`
	StoragePoolDriver       string   `json:"storage_pool_driver,omitempty"`
	DockerNetwork           string   `json:"docker_network,omitempty"`
	DockerNetworkID         string   `json:"docker_network_id,omitempty"`
	ControlSubnet           string   `json:"control_subnet,omitempty"`
	ControlGateway          string   `json:"control_gateway,omitempty"`
	ControlBridge           string   `json:"control_bridge,omitempty"`
	ControlInterfaceName    string   `json:"control_interface_name,omitempty"`
	ControlInterfaceIndex   int      `json:"control_interface_index,omitempty"`
	FirewallRules           bool     `json:"firewall_rules,omitempty"`
	RelayService            bool     `json:"relay_service,omitempty"`
	ManagementTrust         string   `json:"management_trust,omitempty"`
	ConnectionBundle        bool     `json:"connection_bundle,omitempty"`
}

type stateStore interface {
	Lock(context.Context) (stateLock, error)
	Load(context.Context) (State, error)
	Save(context.Context, State) error
	ReadBundle(context.Context) (ConnectionBundle, error)
	WriteBundle(context.Context, ConnectionBundle) error
	RemoveBundle(context.Context) error
}

type stateLock interface {
	Unlock() error
}

type fileStateStore struct {
	statePath  string
	bundlePath string
}

func newFileStateStore() *fileStateStore {
	return &fileStateStore{statePath: DefaultStatePath, bundlePath: DefaultBundlePath}
}

func (s *fileStateStore) Load(ctx context.Context) (State, error) {
	if ctx == nil {
		return State{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	body, err := readRootOnlyFile(s.statePath, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return State{Schema: StateSchema}, nil
	}
	if err != nil {
		return State{}, err
	}
	defer clear(body)
	if err := validateNoDuplicateJSONFields(body); err != nil {
		return State{}, ErrUnsafeState
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(&struct{}{}) != io.EOF || state.Schema != StateSchema {
		return State{}, ErrUnsafeState
	}
	return state, nil
}

func (s *fileStateStore) Save(ctx context.Context, state State) error {
	if ctx == nil || state.Schema != StateSchema {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state.UpdatedAt = time.Now().UTC()
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return ErrUnsafeState
	}
	body = append(body, '\n')
	return writeRootOnlyFile(s.statePath, body, 0600)
}

func (s *fileStateStore) ReadBundle(ctx context.Context) (ConnectionBundle, error) {
	if ctx == nil {
		return ConnectionBundle{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return ConnectionBundle{}, err
	}
	body, err := readRootOnlyFile(s.bundlePath, 1<<20)
	if err != nil {
		return ConnectionBundle{}, err
	}
	defer clear(body)
	if err := validateNoDuplicateJSONFields(body); err != nil {
		return ConnectionBundle{}, ErrUnsafeState
	}
	var bundle ConnectionBundle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&bundle) != nil || decoder.Decode(&struct{}{}) != io.EOF || bundle.Schema != BundleSchema {
		return ConnectionBundle{}, ErrUnsafeState
	}
	return bundle, nil
}

func (s *fileStateStore) WriteBundle(ctx context.Context, bundle ConnectionBundle) error {
	if ctx == nil || bundle.Schema != BundleSchema {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return ErrUnsafeState
	}
	body = append(body, '\n')
	return writeRootOnlyFile(s.bundlePath, body, 0600)
}

func (s *fileStateStore) RemoveBundle(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(s.bundlePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrUnsafeState
	}
	if securefs.SyncParentDirectory(s.bundlePath) != nil {
		return ErrUnsafeState
	}
	return nil
}

func (s State) Public() PublicState {
	public := PublicState{
		Schema: StateSchema, UpdatedAt: s.UpdatedAt,
		Ownership: PublicOwnership{
			ID:                      s.Ownership.ID,
			PackagesInstalledByANAS: s.Ownership.PackagesInstalledByANAS,
			ManagedPackages:         slices.Clone(s.Ownership.ManagedPackages),
			IncusServiceByANAS:      s.Ownership.IncusServiceByANAS,
			ExternalDaemonPreserved: s.Ownership.ExternalDaemonPreserved,
			StoragePool:             s.Ownership.StoragePool,
			StoragePoolDriver:       s.Ownership.StoragePoolDriver,
			DockerNetwork:           s.Ownership.DockerNetwork,
			DockerNetworkID:         s.Ownership.DockerNetworkID,
			ControlSubnet:           s.Ownership.ControlSubnet,
			ControlGateway:          s.Ownership.ControlGateway,
			ControlBridge:           s.Ownership.ControlBridge,
			ControlInterfaceName:    s.Ownership.ControlInterfaceName,
			ControlInterfaceIndex:   s.Ownership.ControlInterfaceIndex,
			FirewallRules:           s.Ownership.FirewallRules,
			RelayService:            s.Ownership.RelayService,
			ManagementTrust:         s.Ownership.ManagementTrust,
			ConnectionBundle:        s.Ownership.ConnectionBundle,
		},
		BundlePersisted: s.Bundle != nil || s.Ownership.ConnectionBundle,
		ReceiptCount:    len(s.Receipts),
		Disabled:        s.Disabled,
	}
	if s.Credential != nil {
		public.CredentialFingerprint = s.Credential.Fingerprint
	}
	for _, intent := range s.Intents {
		if intent.Status == "pending" || intent.Status == "failed" {
			public.PendingIntents = append(public.PendingIntents, intent)
		}
	}
	return public
}

func readRootOnlyFile(path string, limit int64) (body []byte, result error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, ErrUnsafeState
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := trustedAncestors(path); err != nil {
		return nil, err
	}
	if !rootOwnedPrivateFile(info, 0600) && !rootOwnedPrivateFile(info, 0400) {
		return nil, ErrUnsafeState
	}
	file, err := openRootFileNoFollow(path)
	if err != nil {
		return nil, ErrUnsafeState
	}
	defer func() {
		if file.Close() != nil {
			clear(body)
			body = nil
			result = ErrUnsafeState
		}
	}()
	opened, err := file.Stat()
	if err != nil || !sameFileIdentity(info, opened) {
		return nil, ErrUnsafeState
	}
	body, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, ErrUnsafeState
	}
	after, err := file.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !sameFileIdentity(info, after) || !sameFileIdentity(after, current) || trustedAncestors(path) != nil {
		clear(body)
		return nil, ErrUnsafeState
	}
	return body, nil
}

func writeRootOnlyFile(path string, body []byte, mode os.FileMode) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || len(body) == 0 || len(body) > 1<<20 {
		return ErrUnsafeState
	}
	dir := filepath.Dir(path)
	if err := ensureTrustedRootDirectory(dir, 0700); err != nil {
		return ErrUnsafeState
	}
	parent, err := os.Lstat(dir)
	if err != nil || !rootOwnedPrivateDir(parent) {
		return ErrUnsafeState
	}
	tmp, err := os.CreateTemp(dir, ".anas-incus-provision-*")
	if err != nil {
		return ErrUnsafeState
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return ErrUnsafeState
	}
	writeErr := securefs.WriteAll(tmp, body)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return ErrUnsafeState
	}
	if err := os.Rename(tmpName, path); err != nil {
		return ErrUnsafeState
	}
	keep = true
	if securefs.SyncParentDirectory(path) != nil {
		return ErrUnsafeState
	}
	return nil
}

func (s State) digest() string {
	type publicState State
	copy := publicState(s)
	body, _ := json.Marshal(copy)
	return digestBytes(body)
}

func (s *State) hasReceipt(step, digest string) bool {
	if s == nil {
		return false
	}
	return slices.ContainsFunc(s.Receipts, func(r Receipt) bool {
		return r.Step == step && r.Digest == digest && r.Status == "ok"
	})
}

func (s *State) addReceipt(step, digest, status, detail string) {
	s.Receipts = append(s.Receipts, Receipt{
		Step: step, Digest: digest, Status: status, Detail: detail, CreatedAt: time.Now().UTC(),
	})
	if len(s.Receipts) > 256 {
		s.Receipts = append([]Receipt{}, s.Receipts[len(s.Receipts)-256:]...)
	}
}

func (s *State) addReceiptForIntent(intent EffectIntent, status, detail string) {
	now := time.Now().UTC()
	s.Receipts = append(s.Receipts, Receipt{
		Step: intent.Step, Digest: intent.Digest, IntentID: intent.ID, Status: status, Detail: detail, CreatedAt: now,
	})
	for i := len(s.Intents) - 1; i >= 0; i-- {
		if s.Intents[i].ID == intent.ID {
			s.Intents[i].Status = status
			s.Intents[i].UpdatedAt = now
			break
		}
	}
	if len(s.Receipts) > 256 {
		s.Receipts = append([]Receipt{}, s.Receipts[len(s.Receipts)-256:]...)
	}
	if len(s.Intents) > 256 {
		s.Intents = append([]EffectIntent{}, s.Intents[len(s.Intents)-256:]...)
	}
}

func sameConnectionBundle(a, b *ConnectionBundle) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(*a, *b)
}

func validateNoDuplicateJSONFields(body []byte) error {
	if !utf8.Valid(body) {
		return ErrUnsafeState
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := readJSONValueNoDuplicateKeys(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrUnsafeState
	}
	return nil
}

func readJSONValueNoDuplicateKeys(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrUnsafeState
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrUnsafeState
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				key, ok := keyToken.(string)
				if err != nil || !ok || seen[key] {
					return ErrUnsafeState
				}
				seen[key] = true
				if err := readJSONValueNoDuplicateKeys(decoder, depth+1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return ErrUnsafeState
			}
		case '[':
			for decoder.More() {
				if err := readJSONValueNoDuplicateKeys(decoder, depth+1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return ErrUnsafeState
			}
		default:
			return ErrUnsafeState
		}
	}
	return nil
}

func stableOwnershipID() (string, error) {
	var body [16]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", ErrExternalEffects
	}
	return fmt.Sprintf("anas-incus-%x", body[:]), nil
}
