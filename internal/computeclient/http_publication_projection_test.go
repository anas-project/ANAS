package computeclient

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeingress"
)

func TestHTTPConsumerProjectionDoesNotContainFrozenAuthority(t *testing.T) {
	config := httpTestConfig("random")
	typeOf := reflect.TypeOf(config)
	for _, name := range []string{"Authorization", "Deployment", "LeaseSecretRef", "ForwardAuth", "Middleware", "Entrypoint", "Store"} {
		if _, exists := typeOf.FieldByName(name); exists {
			t.Fatalf("consumer projection contains authoritative field %q", name)
		}
	}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), config.LeaseSecret) || strings.Contains(string(body), "LeaseSecret") {
		t.Fatal("serializing the projection leaked the naming key")
	}
}

func TestHTTPProjectedNamingMatchesValidatedAuthorityWithoutReplacingIt(t *testing.T) {
	for _, mode := range []string{"fixed", "named", "random"} {
		t.Run(mode, func(t *testing.T) {
			config := httpTestConfig(mode)
			label := ""
			if mode == "named" {
				label = "api"
			}
			authority := &computeingress.Authorization{
				Schema: computeingress.Schema, Deployment: "deployment-1", Consumer: "forgejo", Resource: "runners", Provider: "incus",
				Interface: config.Interface, Project: config.Project, InstancePrefix: config.InstancePrefix,
				LeaseSecretRef: EnvPrefix + "FORGEJO__RUNNERS__LEASE_SECRET", BaseDomain: config.BaseDomain, Policy: config.Policy,
			}
			expected, err := authority.Host("job:1", label, config.LeaseSecret)
			if err != nil {
				t.Fatal(err)
			}
			projected, err := config.Policy.Host(config.BaseDomain, "job:1", label, config.LeaseSecret)
			if err != nil || projected != expected {
				t.Fatalf("naming projection diverged from authoritative derivation: %v", err)
			}
			authority.Deployment = ""
			if _, err := authority.Host("job:1", label, config.LeaseSecret); err == nil {
				t.Fatal("extracting shared naming logic bypassed complete-grant validation")
			}
		})
	}
	config := httpTestConfig("named")
	config.Policy.Auth = "forward_auth"
	if _, err := config.Policy.Host(config.BaseDomain, "job:1", "api", ""); err != nil {
		t.Fatal("name prediction unexpectedly requires a consumer-visible middleware")
	}
	incomplete := &computeingress.Authorization{
		Schema: computeingress.Schema, Deployment: "deployment-1", Consumer: "forgejo", Resource: "runners", Provider: "incus",
		Interface: config.Interface, Project: config.Project, InstancePrefix: config.InstancePrefix,
		LeaseSecretRef: EnvPrefix + "FORGEJO__RUNNERS__LEASE_SECRET", BaseDomain: config.BaseDomain, Policy: config.Policy,
	}
	if _, err := incomplete.Host("job:1", "api", ""); err == nil {
		t.Fatal("authoritative derivation accepted forward_auth without its frozen binding")
	}
}
