// TEST_CASES: MAIL-T-001
package directorymail

import (
	"strings"
	"testing"
)

func TestDefaultAddressAndIndependentDomain(t *testing.T) {
	for _, tc := range []struct{ configured, fallback, want string }{
		{"", "finance.hlong.wang", "admin@finance.hlong.wang"},
		{" HLong.Wang. ", "finance.hlong.wang", "admin@hlong.wang"},
	} {
		domain, err := Domain(tc.configured, tc.fallback)
		if err != nil {
			t.Fatal(err)
		}
		address, err := Address("Admin", domain)
		if err != nil || address != tc.want {
			t.Fatalf("address=%q err=%v", address, err)
		}
	}
}

func TestInvalidDefaultAddressIsRejectedWithoutGuessing(t *testing.T) {
	for _, username := range []string{"", "admin@example", "a..b", "a.", " a", "a b", "用户", "Ken", "İan", strings.Repeat("a", 65)} {
		if _, err := Address(username, "finance.hlong.wang"); err == nil {
			t.Fatal("invalid username accepted", username)
		}
	}
	for _, domain := range []string{"", "https://example.com", "example..com", "a@example.com", "192.0.2.1", "-example.com"} {
		if _, err := Address("admin", domain); err == nil {
			t.Fatal("invalid domain accepted", domain)
		}
	}
}
