package incusprovision

import (
	"regexp"
	"strings"
	"testing"
)

// Shared by offline guard tests and the Linux-only destructive native fixture.
// Matching only QEMU is insufficient: production machines can also be VMs.
func nativeHostIdentityAllowed(uid int, expected, actual, vendor string) bool {
	return uid == 0 && regexp.MustCompile(`^anas-incus-host-[a-z0-9]{6}$`).MatchString(expected) &&
		strings.TrimSpace(actual) == expected && strings.TrimSpace(vendor) == "QEMU"
}

func TestNativeHostProvisionGuardRequiresTheExactDisposableVM(t *testing.T) {
	if !nativeHostIdentityAllowed(0, "anas-incus-host-abc123", "anas-incus-host-abc123\n", "QEMU\n") {
		t.Fatal("valid explicit native VM identity was rejected")
	}
	for _, value := range []struct {
		uid                      int
		expected, actual, vendor string
	}{
		{1000, "anas-incus-host-abc123", "anas-incus-host-abc123", "QEMU"},
		{0, "anas-incus-host-abc123", "production-vm", "QEMU"},
		{0, "anas-incus-host-abc123", "anas-incus-host-def456", "QEMU"},
		{0, "production-vm", "production-vm", "QEMU"},
		{0, "anas-incus-host-abc123", "anas-incus-host-abc123", "Dell"},
		{0, "anas-incus-build-abc123", "anas-incus-build-abc123", "QEMU"},
		{0, "anas-incus-host-abc123\n", "anas-incus-host-abc123", "QEMU"},
		{0, "anas-incus-host-abc123;exec", "anas-incus-host-abc123;exec", "QEMU"},
		{0, "", "", "QEMU"},
	} {
		if nativeHostIdentityAllowed(value.uid, value.expected, value.actual, value.vendor) {
			t.Fatalf("unsafe native host identity was accepted: %#v", value)
		}
	}
}
