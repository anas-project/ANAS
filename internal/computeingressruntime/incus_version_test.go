package computeingressruntime

import (
	"context"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
)

func TestPinnedVersionPlanningRejectsChangedVersionAndIdentity(t *testing.T) {
	for _, scenario := range []string{"stable", "version", "identity", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
			// Reuse the real mTLS fixture credentials through the existing server
			// setup helper; do not reinterpret a response as a new certificate pin.
			config := f.config
			config.ServerVersion = ""
			ctx := context.Background()
			if scenario == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if scenario == "version" || scenario == "identity" {
				f.mutate = func(path string, n int, values map[string]any) {
					if n != 2 {
						return
					}
					server := values[path].(map[string]any)
					if scenario == "identity" {
						server["auth_user_name"] = "not-the-client"
					} else {
						server["environment"].(map[string]any)["server_version"] = "different"
					}
				}
			}
			version, err := ObservePinnedIncusVersion(ctx, config)
			if scenario == "stable" {
				if err != nil || version == "" {
					t.Fatal(version, err)
				}
			} else if err == nil || version != "" {
				t.Fatal("invalid version observation accepted")
			}
		})
	}
}
