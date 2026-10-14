package computeingress

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// routeShape is what Traefik acts on in a one-route file provider document;
// router and service names are left out.
type routeShape struct {
	Rule        string
	EntryPoints []string
	Middlewares []string
	TLS         bool
	Servers     []string
}

func parseRouteShape(t *testing.T, body []byte) routeShape {
	t.Helper()
	var doc struct {
		HTTP struct {
			Routers map[string]struct {
				Rule        string    `yaml:"rule"`
				Service     string    `yaml:"service"`
				EntryPoints []string  `yaml:"entryPoints"`
				Middlewares []string  `yaml:"middlewares"`
				TLS         *struct{} `yaml:"tls"`
			} `yaml:"routers"`
			Services map[string]struct {
				LoadBalancer struct {
					Servers []struct {
						URL string `yaml:"url"`
					} `yaml:"servers"`
				} `yaml:"loadBalancer"`
			} `yaml:"services"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil || len(doc.HTTP.Routers) != 1 || len(doc.HTTP.Services) != 1 {
		t.Fatalf("not a one-route document (%v):\n%s", err, body)
	}
	var shape routeShape
	for _, router := range doc.HTTP.Routers {
		service, ok := doc.HTTP.Services[router.Service]
		if !ok {
			t.Fatalf("router names a missing service:\n%s", body)
		}
		shape = routeShape{Rule: router.Rule, EntryPoints: router.EntryPoints, Middlewares: router.Middlewares, TLS: router.TLS != nil}
		for _, server := range service.LoadBalancer.Servers {
			shape.Servers = append(shape.Servers, server.URL)
		}
	}
	return shape
}

// INCUS-R-054: a compute HTTP route is a Traefik Module declared route
// (ANAS_TRAEFIK_ROUTE__*) in a file the mediator renders itself. Rendering the
// same route through the Traefik entrypoint keeps the two shapes from drifting;
// a change to either must be made in both.
func TestRenderRouteMatchesTraefikDeclaredRoute(t *testing.T) {
	entrypoint, err := filepath.Abs("../../modules/traefik/traefik/anas-entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := policyFixture()
	host, err := base.HostForLabel("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	for _, middleware := range []string{"", "gateway-forward-auth@file"} {
		a := base.Clone()
		env := []string{"PATH=" + os.Getenv("PATH"), "ANAS_CONFIG_DIR=" + t.TempDir(), "ANAS_TRAEFIK_BINARY=/usr/bin/true",
			"LEGO_CERT_NAME=example.test.crt", "LEGO_KEY_NAME=example.test.key",
			"ANAS_TRAEFIK_ROUTE__COMPUTE__RULE=Host(`" + host + "`)",
			"ANAS_TRAEFIK_ROUTE__COMPUTE__URL=http://10.101.0.17:7000"}
		if middleware != "" {
			a.Policy.Auth = "forward_auth"
			a.ForwardAuth = &ForwardAuth{Provider: "gateway", Middleware: middleware}
			env = append(env, "ANAS_TRAEFIK_ROUTE__COMPUTE__MIDDLEWARES="+middleware)
		}
		ours, err := RenderRoute(a, host, "10.101.0.17", 7000)
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command("/bin/sh", entrypoint)
		command.Env = env
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("entrypoint: %v\n%s", err, output)
		}
		declared, err := os.ReadFile(filepath.Join(env[1][len("ANAS_CONFIG_DIR="):], "routes.yml"))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := parseRouteShape(t, ours), parseRouteShape(t, declared); !reflect.DeepEqual(got, want) {
			t.Fatalf("middleware %q: mediator route %+v, declared route %+v", middleware, got, want)
		}
	}
}
