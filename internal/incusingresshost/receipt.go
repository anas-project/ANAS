package incusingresshost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/securefs"
	"golang.org/x/sys/unix"
)

type receipt struct {
	Schema          string    `json:"schema"`
	Scope           string    `json:"scope"`
	Generation      uint64    `json:"generation"`
	Target          Target    `json:"target"`
	TargetDigest    string    `json:"target_digest"`
	ScopeDigest     string    `json:"scope_digest"`
	PermitSet       string    `json:"permit_set"`
	PermitComment   string    `json:"permit_comment"`
	RouteTable      uint32    `json:"route_table"`
	RouteProtocol   uint8     `json:"route_protocol"`
	AddressHeld     bool      `json:"address_held"`
	AddressIntent   bool      `json:"address_intent,omitempty"`
	RouteIntent     bool      `json:"route_intent,omitempty"`
	RouteReady      bool      `json:"route_ready"`
	PermitIntent    bool      `json:"permit_intent,omitempty"`
	PermitReady     bool      `json:"permit_ready"`
	PermitExpiresAt time.Time `json:"permit_expires_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	LastObservedAt  time.Time `json:"last_observed_at"`
}

var errReceiptMissing = errors.New("ingress host receipt missing")

func (b *Backend) loadReceipt(target Target) (receipt, error) {
	return b.loadReceiptPath(b.receiptPath(target), target)
}

func (b *Backend) loadReceiptPath(path string, expected Target) (receipt, error) {
	if filepath.Dir(path) != b.config.ReceiptDir {
		return receipt{}, fmt.Errorf("receipt is outside the installed directory")
	}
	body, err := b.readHostDocument(filepath.Base(path))
	if err != nil {
		return receipt{}, err
	}
	var r receipt
	if err := decodeObservedJSON(body, &r); err != nil {
		return receipt{}, fmt.Errorf("invalid ingress host receipt JSON")
	}
	if expected.Reservation != "" && r.Target != expected {
		return receipt{}, fmt.Errorf("ingress host receipt target mismatch")
	}
	if err := b.validateReceipt(r, r.Target); err != nil {
		return receipt{}, err
	}
	return r, nil
}

func (b *Backend) readHostDocument(name string) (body []byte, result error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return nil, fmt.Errorf("invalid host document name")
	}
	path := filepath.Join(b.config.ReceiptDir, name)
	directory, err := openReceiptDirectory(b.config.ReceiptDir)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, directory.Close()) }()
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, errReceiptMissing
	}
	if err != nil || securefs.ValidateFileInfo(info, "ingress receipt") != nil {
		return nil, fmt.Errorf("invalid ingress host receipt")
	}
	fd, err := unix.Openat(int(directory.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open ingress host receipt")
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { result = errors.Join(result, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || securefs.VerifyOpenNamedFile(file, path, "ingress receipt") != nil {
		return nil, fmt.Errorf("ingress host receipt changed while opening")
	}
	body, err = io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(body) > 65536 {
		return nil, fmt.Errorf("ingress host receipt is unreadable or oversized")
	}
	after, err := file.Stat()
	if err != nil || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) ||
		securefs.VerifyOpenNamedFile(file, path, "ingress receipt") != nil ||
		securefs.VerifyOpenDirectory(directory, b.config.ReceiptDir, "ingress receipt directory") != nil {
		return nil, fmt.Errorf("ingress receipt changed while reading")
	}
	return body, nil
}

func (b *Backend) saveReceipt(ctx context.Context, r receipt) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.LastObservedAt = time.Now().UTC()
	if err := b.validateReceipt(r, r.Target); err != nil {
		return err
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return b.writeHostDocument(ctx, receiptName(r.Target), body)
}

func (b *Backend) writeHostDocument(ctx context.Context, name string, body []byte) (result error) {
	if ctx == nil || name == "" || filepath.Base(name) != name || name == "." || name == ".." || len(body) == 0 || len(body) > 65536 {
		return fmt.Errorf("invalid host document")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := openReceiptDirectory(b.config.ReceiptDir)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, directory.Close()) }()
	tmpName := name + ".tmp"
	// Never truncate, adopt or delete a pre-existing temp artifact. An
	// interrupted write remains evidence requiring recovery, not permission.
	fd, err := unix.Openat(int(directory.Fd()), tmpName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("create exclusive ingress receipt intent")
	}
	file := os.NewFile(uintptr(fd), filepath.Join(b.config.ReceiptDir, tmpName))
	closed := false
	defer func() {
		if !closed {
			result = errors.Join(result, file.Close())
		}
	}()
	if err := securefs.WriteAll(file, body); err != nil {
		return fmt.Errorf("write ingress receipt intent: %w", err)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := securefs.VerifyOpenNamedFile(file, file.Name(), "ingress receipt intent"); err != nil {
		return err
	}
	if err := securefs.VerifyOpenDirectory(directory, b.config.ReceiptDir, "ingress receipt directory"); err != nil {
		return err
	}
	closed = true
	if err := file.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(int(directory.Fd()), tmpName, int(directory.Fd()), name); err != nil {
		return fmt.Errorf("publish ingress host receipt: %w", err)
	}
	return errors.Join(directory.Sync(), securefs.VerifyOpenDirectory(directory, b.config.ReceiptDir, "ingress receipt directory"))
}

func (b *Backend) removeReceipt(target Target) (result error) {
	directory, err := openReceiptDirectory(b.config.ReceiptDir)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, directory.Close()) }()
	if _, err := b.loadReceipt(target); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(directory.Fd()), receiptName(target), 0); err != nil {
		return fmt.Errorf("remove ingress host receipt: %w", err)
	}
	return errors.Join(directory.Sync(), securefs.VerifyOpenDirectory(directory, b.config.ReceiptDir, "ingress receipt directory"))
}

func (b *Backend) validateReceipt(r receipt, target Target) error {
	if r.Schema != receiptSchema || r.Scope != b.config.ScopeName || r.ScopeDigest != ingressScopeDigest(b.config) || r.Target != target || r.TargetDigest != targetDigest(target) || r.PermitSet != permitSetName(target) || r.PermitComment != permitComment(target) || r.RouteTable != b.config.RouteTable || r.RouteProtocol != b.config.RouteProtocol {
		return fmt.Errorf("ingress host receipt does not match the complete trusted target")
	}
	if err := b.validateTarget(target); err != nil {
		return err
	}
	if r.PermitReady && !r.RouteReady || r.RouteReady && !r.AddressHeld || r.PermitIntent && !r.RouteReady || r.RouteIntent && !r.AddressHeld {
		return fmt.Errorf("ingress host receipt has impossible step ordering")
	}
	if r.AddressIntent && (r.AddressHeld || r.RouteReady || r.PermitReady || r.RouteIntent || r.PermitIntent) {
		return fmt.Errorf("address acquisition intent cannot contain later publication steps")
	}
	return nil
}

// A receipt for an old container namespace must never authorize cleanup in a
// replacement namespace, even when its guest target and installed scope name
// are unchanged. Old evidence remains blocked rather than silently migrated.
func ingressScopeDigest(config backendConfig) string {
	projection := struct {
		Scope, PermitTable, OriginTable, GuestBridge, IngressBridge string
		TraefikVeth, TraefikInterface, Source, Gateway, GuestSubnet string
		Table                                                       uint32
		Protocol                                                    uint8
		Namespace                                                   RouteNamespacePin
	}{config.ScopeName, config.PermitTable, config.OriginTable, config.GuestBridge, config.IngressBridge,
		config.TraefikVeth, config.TraefikInterface, config.TraefikSourceIP, config.IngressGateway, config.GuestSubnet,
		config.RouteTable, config.RouteProtocol, config.Namespace}
	body, _ := json.Marshal(projection)
	// Policy upgrades cannot reinterpret prior receipts as permission for the
	// new bidirectional firewall. Existing evidence requires explicit recovery.
	body = append([]byte("bidirectional-origin-v1\x00"), body...)
	if config.AddressRouting != nil {
		address, _ := json.Marshal(config.AddressRouting)
		body = append(append(body, []byte("\x00device-address-routing/v1\x00")...), address...)
	}
	sum := sha256.Sum256(append([]byte("anas.incus-http-scope/v2\x00"), body...))
	return hex.EncodeToString(sum[:])
}

func (b *Backend) receiptPath(target Target) string {
	return filepath.Join(b.config.ReceiptDir, receiptName(target))
}

func receiptName(target Target) string {
	return strings.ReplaceAll(target.Reservation, ":", "-") + ".json"
}

func (b *Backend) listReceipts() ([]receipt, error) {
	entries, err := os.ReadDir(b.config.ReceiptDir)
	if err != nil {
		return nil, err
	}
	if len(entries) > 4096 {
		return nil, fmt.Errorf("ingress receipt inventory exceeds limit")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	result := make([]receipt, 0, len(names))
	allocations := map[string]Target{}
	for _, name := range names {
		if name == addressRoutingFile {
			guard, err := b.addressRouter()
			if err != nil {
				return nil, err
			}
			if _, err := guard.load(); err != nil {
				return nil, err
			}
			continue
		}
		if name == baselineReceiptName {
			if _, err := b.loadBaselineReceipt(); err != nil {
				return nil, err
			}
			continue
		}
		if name == ".lock" {
			continue
		}
		if !strings.HasSuffix(name, ".json") || strings.Contains(name, "/") || strings.HasSuffix(name, ".tmp.json") {
			return nil, fmt.Errorf("ingress receipt directory contains unknown artifact")
		}
		r, err := b.loadReceiptPath(filepath.Join(b.config.ReceiptDir, name), Target{})
		if err != nil {
			return nil, err
		}
		if receiptName(r.Target) != name {
			return nil, fmt.Errorf("ingress receipt filename does not match its complete target")
		}
		if previous, ok := allocations[r.Target.GuestIP]; ok && !sameAddressAllocation(previous, r.Target) {
			return nil, fmt.Errorf("ingress address is claimed by conflicting allocation identities")
		}
		allocations[r.Target.GuestIP] = r.Target
		result = append(result, r)
	}
	return result, nil
}

// Ports/reservations may share one running allocation, but a new process
// incarnation, lease, epoch, NIC or instance must wait for every old hold to be
// released. This journal barrier does not replace the required Incus allocator
// lifecycle contract, so production publication remains disabled.
func sameAddressAllocation(a, b Target) bool {
	a.Reservation, b.Reservation = "", ""
	a.GuestPort, b.GuestPort = 0, 0
	return a == b
}

func (b *Backend) checkAddressOwner(target Target) error {
	receipts, err := b.listReceipts()
	if err != nil {
		return err
	}
	for _, r := range receipts {
		if r.Target.GuestIP == target.GuestIP && !sameAddressAllocation(r.Target, target) {
			return fmt.Errorf("retain old ingress allocation before reusing its guest address")
		}
	}
	return nil
}

func (b *Backend) receiptedTargets() ([]Target, error) {
	receipts, err := b.listReceipts()
	if err != nil {
		return nil, err
	}
	targets := make([]Target, 0, len(receipts))
	for _, receipt := range receipts {
		targets = append(targets, receipt.Target)
	}
	return targets, nil
}

func (b *Backend) otherReadyRouteReceipt(target Target) (bool, error) {
	receipts, err := b.listReceipts()
	if err != nil {
		return false, err
	}
	for _, receipt := range receipts {
		if receipt.Target.Reservation != target.Reservation && receipt.Target.GuestIP == target.GuestIP && receipt.RouteReady {
			if !sameAddressAllocation(receipt.Target, target) {
				return false, fmt.Errorf("guest route belongs to a different address allocation")
			}
			return true, nil
		}
	}
	return false, nil
}
