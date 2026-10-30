// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This opt-in check reads the actual account destination list only. It never
// creates, verifies, modifies or prints another account destination.
func TestCloudflareForwardAccountListLive(t *testing.T) {
	path := os.Getenv("MAIL_CLOUDFLARE_DESTINATION_READ_FILE")
	if path == "" {
		t.Skip("private Cloudflare destination credential required")
	}
	info, err := os.Lstat(path)
	if err != nil || !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		t.Fatal("private credential file required")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		t.Fatal("private credential directory required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private credential unavailable")
	}
	defer clear(raw)
	var input struct {
		Account string `json:"account"`
		Token   string `json:"token"`
	}
	if json.Unmarshal(raw, &input) != nil || !cloudflareID.MatchString(input.Account) || !cloudflareToken.MatchString(input.Token) {
		t.Fatal("private credential invalid")
	}
	source, _, route := routeFixture(t)
	client, err := NewCloudflareForwardRules(source, input.Account, strings.Repeat("2", 32), "in.example.test", input.Token, strings.Repeat("e", 40))
	input.Token = ""
	if err != nil {
		t.Fatal("forward client invalid")
	}
	if err = client.CheckDestinationAbsent(t.Context(), route); err != nil {
		t.Fatal("account destination list was not safely accepted", err)
	}
}
