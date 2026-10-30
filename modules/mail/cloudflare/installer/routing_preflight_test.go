// TEST_CASES: MAILMOD-T-042
package installer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRoutingPreflightUsesSeparateScopedTokenAndNoWrites(t *testing.T) {
	zone, token := strings.Repeat("1", 32), strings.Repeat("R", 32)
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"accessible", 200, `{"success":true,"result":[]}`, nil},
		{"unauthorized", 401, `{"errors":[{"message":"private-provider-error"}]}`, ErrRoutingDenied},
		{"forbidden", 403, `{"errors":[{"message":"private-provider-error"}]}`, ErrRoutingDenied},
		{"rate-limited", 429, `{}`, ErrUnknown},
		{"redirect", 302, `{}`, ErrUnknown},
		{"bad-json", 200, `{`, ErrUnknown},
		{"null-results", 200, `{"success":true,"result":null}`, ErrUnknown},
		{"oversized", 200, strings.Repeat("a", 1<<20+1), ErrUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := cloudFixture(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.cloudflare.com" || r.URL.Path != "/client/v4/zones/"+zone+"/email/routing/rules" || r.URL.RawQuery != "per_page=5&page=1" || r.Header.Get("Authorization") != "Bearer "+token {
					t.Fatal("wrong preflight request")
				}
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})
			err := client.preflightRouting(context.Background(), zone, token, "")
			if calls != 1 || test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatal("wrong preflight result", err)
			}
			if err != nil && (strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "private-provider-error")) {
				t.Fatal("provider detail leaked")
			}
		})
	}
}

func TestRuntimePreflightDenialDoesNotCreateResources(t *testing.T) {
	j, cloud, req := handoffFixture(t)
	cloud.routingErr = ErrRoutingDenied
	if _, err := j.prepareRuntime(t.Context(), cloud, req); !errors.Is(err, ErrRoutingDenied) {
		t.Fatal("denial accepted", err)
	}
	if j.state.Stage != "prepared" || cloud.workerCreates != 0 || cloud.bucketCreates != 0 || cloud.creates != 0 || cloud.writes != 0 {
		t.Fatal("preflight denial created resources")
	}
	var info unix.Stat_t
	if err := unix.Fstatat(j.dirfd, "source-secrets.json", &info, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
		t.Fatal("preflight denial created source credentials", err)
	}
}

func TestRoutingPreflightDoesNotMaskAnUnknownWrite(t *testing.T) {
	j, cloud, req := handoffFixture(t)
	cloud.lost = true
	if _, err := j.prepareRuntime(t.Context(), cloud, req); !errors.Is(err, ErrUnknown) {
		t.Fatal("lost write was not unknown", err)
	}
	before := cloud.routingReads
	cloud.routingErr = ErrRoutingDenied
	if _, err := j.prepareRuntime(t.Context(), cloud, req); !errors.Is(err, ErrUnknown) || cloud.routingReads != before {
		t.Fatal("routing denial masked unknown write", err)
	}
}
