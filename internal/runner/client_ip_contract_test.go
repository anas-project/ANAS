package runner

import (
	"os"
	"strings"
	"testing"
)

func TestApplicationClientIPAdaptersRemainConfigured(t *testing.T) {
	cases := []struct {
		path      string
		required  []string
		forbidden []string
	}{
		{
			path:     "../../modules/authentik/authentik/server-entrypoint.sh",
			required: []string{"AUTHENTIK_LISTEN__TRUSTED_PROXY_CIDRS", "${traefik_ip}/32"},
		},
		{
			path:     "../../modules/lam/lam/config.sh",
			required: []string{"RemoteIPHeader X-Forwarded-For", "RemoteIPInternalProxy $TRAEFIK_HOSTNAME"},
		},
		{
			path:     "../../modules/llng/llng/anas-entrypoint.sh",
			required: []string{"real_ip_header X-Forwarded-For", "real_ip_recursive on", "set_real_ip_from ${TRAEFIK_HOSTNAME}"},
		},
		{
			path:     "../../modules/meshcentral/meshcentral/configure.js",
			required: []string{"settings.tlsOffload", `required("TRAEFIK_IP")`},
		},
		{
			path:     "../../modules/netbird/management/root/root/management.json.envsubst",
			required: []string{"\"TrustedHTTPProxies\": $NETBIRD_TRUSTED_HTTP_PROXIES"},
		},
		{
			path:     "../../modules/nextcloud/nextcloud/root/usr/local/bin/task.sh",
			required: []string{"config:system:set trusted_proxies", "config:system:set forwarded_for_headers", "HTTP_X_FORWARDED_FOR"},
		},
		{
			path:      "../../modules/oauth2_proxy/docker-compose.yml",
			required:  []string{"--reverse-proxy=true", "anas.client-ip.mode=application"},
			forbidden: []string{"--trusted-proxy-ip=10.0.0.0/8", "--trusted-proxy-ip=172.16.0.0/12", "--trusted-proxy-ip=192.168.0.0/16"},
		},
	}

	for _, tc := range cases {
		body, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		text := string(body)
		for _, required := range tc.required {
			if !strings.Contains(text, required) {
				t.Errorf("%s is missing client-IP adapter fragment %q", tc.path, required)
			}
		}
		for _, forbidden := range tc.forbidden {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains broad trust fragment %q", tc.path, forbidden)
			}
		}
	}
}
