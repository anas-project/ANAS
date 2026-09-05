package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	sandboxPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	prefixPattern      = regexp.MustCompile(`^anas-[a-z0-9-]{1,50}$`)
	fingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	consumerPattern    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	storagePoolPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		// Errors reach the Runner's apply output. Nothing here interpolates a
		// certificate, a key or a consumer secret into an error string.
		fmt.Fprintln(os.Stderr, "anas-incus-provisioner: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	operation, isolation, err := parseArgs(args)
	if err != nil {
		return err
	}
	l, err := leaseFromEnv(isolation)
	if err != nil {
		return err
	}
	c, err := clientFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch operation {
	case "ensure":
		result, err := ensure(ctx, c, l)
		if err != nil {
			return err
		}
		return emit(result)
	case "inspect":
		result, err := inspect(ctx, c, l)
		if err != nil {
			return err
		}
		return emit(result)
	case "revoke":
		if err := revoke(ctx, c, l); err != nil {
			return err
		}
		return emit(map[string]bool{"revoked": true})
	default:
		return fmt.Errorf("unsupported compute operation %q", operation)
	}
}

func parseArgs(args []string) (operation, isolation string, err error) {
	if len(args) != 3 || args[1] != "--isolation" {
		return "", "", fmt.Errorf("usage: OPERATION --isolation vm|container")
	}
	operation, isolation = args[0], args[2]
	if isolation != "vm" && isolation != "container" {
		return "", "", fmt.Errorf("isolation tier must be vm or container")
	}
	return operation, isolation, nil
}

func clientFromEnv() (*client, error) {
	endpoint := strings.TrimSpace(os.Getenv("INCUS_ENDPOINT"))
	if !strings.HasPrefix(endpoint, "https://") {
		return nil, fmt.Errorf("INCUS_ENDPOINT must be an HTTPS URL")
	}
	serverCert, err := decodeBase64Env("INCUS_SERVER_CERT_B64")
	if err != nil {
		return nil, err
	}
	adminCert, err := decodeBase64Env("INCUS_ADMIN_CERT_B64")
	if err != nil {
		return nil, err
	}
	adminKey, err := decodeBase64Env("INCUS_ADMIN_KEY_B64")
	if err != nil {
		return nil, err
	}
	return newClient(endpoint, serverCert, adminCert, adminKey)
}

func leaseFromEnv(isolation string) (lease, error) {
	l := lease{
		Consumer: strings.TrimSpace(os.Getenv("ANAS_RESOURCE_CONSUMER")),
		Sandbox:  strings.TrimSpace(os.Getenv("ANAS_RESOURCE_SANDBOX")),
		// The pool is a property of the daemon this module points at, not of
		// any one lease, so it comes from the module's own configuration.
		StoragePool:    strings.TrimSpace(os.Getenv("INCUS_STORAGE_POOL")),
		NetworkIPv6:    strings.EqualFold(strings.TrimSpace(os.Getenv("INCUS_NETWORK_IPV6")), "true"),
		InstancePrefix: strings.TrimSpace(os.Getenv("ANAS_RESOURCE_INSTANCE_PREFIX")),
		Isolation:      isolation,
	}
	if !consumerPattern.MatchString(l.Consumer) {
		return lease{}, fmt.Errorf("ANAS_RESOURCE_CONSUMER is not a valid module name")
	}
	if !sandboxPattern.MatchString(l.Sandbox) {
		return lease{}, fmt.Errorf("ANAS_RESOURCE_SANDBOX is not a valid project name")
	}
	if !storagePoolPattern.MatchString(l.StoragePool) {
		return lease{}, fmt.Errorf("INCUS_STORAGE_POOL is not a valid storage pool name")
	}
	if !prefixPattern.MatchString(l.InstancePrefix) {
		return lease{}, fmt.Errorf("ANAS_RESOURCE_INSTANCE_PREFIX is not a valid instance prefix")
	}
	var err error
	if l.MaxInstances, err = intEnv("ANAS_RESOURCE_MAX_INSTANCES", 1, 256); err != nil {
		return lease{}, err
	}
	if l.CPU, err = intEnv("ANAS_RESOURCE_CPU", 1, 64); err != nil {
		return lease{}, err
	}
	if l.MemoryMiB, err = intEnv("ANAS_RESOURCE_MEMORY_MIB", 512, 262144); err != nil {
		return lease{}, err
	}
	if l.DiskGiB, err = intEnv("ANAS_RESOURCE_DISK_GIB", 4, 2048); err != nil {
		return lease{}, err
	}
	// A pinned fingerprint list is the whole point: a tag or an alias would let
	// whatever the remote publishes today become what this lease boots.
	for _, raw := range strings.Split(os.Getenv("ANAS_RESOURCE_IMAGE_ALLOWLIST"), ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if !fingerprintPattern.MatchString(value) {
			return lease{}, fmt.Errorf("image allowlist entries must be SHA-256 fingerprints")
		}
		l.ImageAllowlist = append(l.ImageAllowlist, value)
	}
	if len(l.ImageAllowlist) == 0 {
		return lease{}, fmt.Errorf("ANAS_RESOURCE_IMAGE_ALLOWLIST must list at least one image fingerprint")
	}
	if l.ClientCertPEM, err = decodeBase64Env("ANAS_RESOURCE_CLIENT_CERT"); err != nil {
		return lease{}, err
	}
	return l, nil
}

func intEnv(key string, min, max int) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if value < min || value > max {
		return 0, fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return value, nil
}

func decodeBase64Env(key string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil, fmt.Errorf("%s is required", key)
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// Deliberately does not echo the value.
		return nil, fmt.Errorf("%s is not valid base64", key)
	}
	return decoded, nil
}

func emit(result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}
