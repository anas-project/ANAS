package main

import (
	"os"
	"strings"
	"testing"
)

func TestEntrypointBootstrapsBeforeStartingLDAPSynchronizer(t *testing.T) {
	b, err := os.ReadFile("../casdoor/entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	removeInit := "rm -f /tmp/init_data.json"
	renderInit := "/opt/anas/bin/casdoor-helper render-init"
	bootstrap := `/opt/anas/bin/casdoor-helper exec-as 1000 1000 "$@" &`
	longRunning := `exec /opt/anas/bin/casdoor-helper exec-as 1000 1000 "$@"`
	removeInitAt, renderInitAt := strings.Index(script, removeInit), strings.Index(script, renderInit)
	if removeInitAt < 0 || renderInitAt < 0 || removeInitAt >= renderInitAt {
		t.Fatal("entrypoint does not recreate the UID-1000-owned init data on process restart")
	}
	bootstrapAt, longRunningAt := strings.Index(script, bootstrap), strings.Index(script, longRunning)
	if bootstrapAt < 0 || longRunningAt < 0 || bootstrapAt >= longRunningAt {
		t.Fatalf("entrypoint must import init data in a bootstrap process before the long-running process:\n%s", script)
	}
	if !strings.Contains(script, "casdoor-helper service-healthcheck") || !strings.Contains(script, `kill -TERM "$bootstrap_pid"`) {
		t.Fatal("entrypoint does not verify and stop the bootstrap process")
	}
	setPassword := "casdoor-helper set-password built-in"
	readyMarker := "touch /tmp/anas-casdoor-ready"
	setPasswordAt, readyMarkerAt := strings.LastIndex(script, setPassword), strings.LastIndex(script, readyMarker)
	if setPasswordAt < 0 || readyMarkerAt < 0 || setPasswordAt >= readyMarkerAt {
		t.Fatal("entrypoint publishes readiness before reconciling the recovery administrator")
	}
	if !strings.Contains(script, "mkdir -p /data/anas-dirwatch") || !strings.Contains(script, "chown 1000:1000 /data/anas-dirwatch") {
		t.Fatal("entrypoint does not prepare the unprivileged directory watcher state")
	}
}

func TestHelperBuildUsesConfiguredGoProxy(t *testing.T) {
	dockerfile, err := os.ReadFile("../casdoor/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ARG GOPROXY_URL", `go env -w GOPROXY="$GOPROXY_URL"`, "go mod download"} {
		if !strings.Contains(string(dockerfile), required) {
			t.Fatalf("Casdoor helper Dockerfile does not contain %q", required)
		}
	}

	compose, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "GOPROXY_URL: ${GOPROXY_URL:-}") {
		t.Fatal("Casdoor Compose build does not forward GOPROXY_URL")
	}
}

func TestServerBuildPinsAndVerifiesPatchedUpstreamSource(t *testing.T) {
	dockerfile, err := os.ReadFile("../casdoor/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e.tar.gz",
		"365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460",
		"sha256sum -c -",
		"patches/0001-saml-directory-attributes.patch",
		"patches/0002-oidc-session-logout.patch",
		"patches/0003-oidc-logout-delivery-logging.patch",
		"patches/0004-postgres-token-user-filter.patch",
		"patch -p1",
		"COPY --chmod=0755 --from=casdoor-server /out/server /server",
	} {
		if !strings.Contains(string(dockerfile), required) {
			t.Fatalf("Casdoor server Dockerfile does not contain %q", required)
		}
	}

	patch, err := os.ReadFile("../casdoor/patches/0001-saml-directory-attributes.patch")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"$user.displayName", "$user.externalId"} {
		if !strings.Contains(string(patch), required) {
			t.Fatalf("Casdoor SAML patch does not contain %q", required)
		}
	}

	logoutPatch, err := os.ReadFile("../casdoor/patches/0002-oidc-session-logout.patch")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`Sid       string ` + "`json:\"sid,omitempty\"`",
		"generateJwtTokenWithSession",
		"SendBackchannelLogout(session.Owner, session.Name, sessionId",
		"ExpiresAt: jwt.NewNumericDate(nowTime.Add(2 * time.Minute))",
	} {
		if !strings.Contains(string(logoutPatch), required) {
			t.Fatalf("Casdoor OIDC logout patch does not contain %q", required)
		}
	}

	deliveryPatch, err := os.ReadFile("../casdoor/patches/0003-oidc-logout-delivery-logging.patch")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"active-token lookup failed",
		"token generation failed",
		"POST failed",
		"endpoint returned HTTP",
	} {
		if !strings.Contains(string(deliveryPatch), required) {
			t.Fatalf("Casdoor OIDC logout delivery patch does not contain %q", required)
		}
	}

	postgresPatch, err := os.ReadFile("../casdoor/patches/0004-postgres-token-user-filter.patch")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`Where(map[string]interface{}{"organization": organization, "user": username})`,
		`Where(map[string]interface{}{"organization": owner, "user": username})`,
	} {
		if !strings.Contains(string(postgresPatch), required) {
			t.Fatalf("Casdoor PostgreSQL token filter patch does not contain %q", required)
		}
	}
}

func TestImageDisablesCasdoorOldInstanceLookup(t *testing.T) {
	entrypoint, err := os.ReadFile("../casdoor/entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entrypoint), "rm -f /usr/bin/lsof") {
		t.Fatal("Casdoor image can kill its bootstrap child through the old-instance lookup")
	}
}

func TestImageCrossCompilesWithoutExecutingTargetArchitecture(t *testing.T) {
	dockerfile, err := os.ReadFile("../casdoor/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"FROM --platform=$BUILDPLATFORM",
		"ARG TARGETARCH",
		`GOOS="$TARGETOS" GOARCH="$TARGETARCH"`,
		"COPY --chmod=0755 --from=casdoor-server",
		"COPY --chmod=0755 --from=helper",
	} {
		if !strings.Contains(string(dockerfile), required) {
			t.Fatalf("Casdoor Dockerfile cannot cross-build without target emulation; missing %q", required)
		}
	}
	if strings.Contains(string(dockerfile), "ARG TARGETARCH=amd64") {
		t.Fatal("Casdoor Dockerfile overrides BuildKit's automatic target architecture")
	}
}
