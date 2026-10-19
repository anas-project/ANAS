// TEST_CASES: PORTAL-T-001
package browserlogout

import (
	"github.com/anas-project/ANAS/internal/appcatalog"
	"strings"
	"testing"
)

func native(module, origin string) appcatalog.Application {
	return appcatalog.Application{Metadata: appcatalog.Metadata{Name: module, URL: origin, Hidden: true}, Enabled: true, Access: appcatalog.Access{Mode: "iam", Client: module}, Source: appcatalog.Source{Kind: "module", Module: module}}
}
func TestFixedProtocolAddressesExcludeBusinessMetadata(t *testing.T) {
	app := native("immich", "https://photos.example.test:443/private?secret=1")
	external := native("nextcloud", "https://external.example.test")
	external.Source.Kind = "external"
	disabled := native("nextcloud", "https://disabled.example.test")
	disabled.Enabled = false
	c := appcatalog.Catalog{Applications: []appcatalog.Application{app, app, external, disabled, native("unsupported", "https://unknown.example.test")}}
	targets, err := Targets(c, "https://portal.example.test")
	if err != nil || len(targets) != 1 || targets[0].Origin != "https://photos.example.test" {
		t.Fatal(targets, err)
	}
	rule := Rule(targets, "https://portal.example.test")
	if !strings.Contains(rule, "Host(`photos.example.test`)") || !strings.Contains(rule, Path) || strings.Contains(rule, "private") {
		t.Fatal(rule)
	}
}
func TestNativeTargetsRequireSameSiteHTTPSAndPort(t *testing.T) {
	for _, origin := range []string{"http://photos.example.test", "https://photos.other.test", "https://photos.example.test:9000", "https://evil@photos.example.test", "https://portal.example.test"} {
		if _, err := Targets(appcatalog.Catalog{Applications: []appcatalog.Application{native("immich", origin)}}, "https://portal.example.test"); err == nil {
			t.Fatal("accepted", origin)
		}
	}
}
