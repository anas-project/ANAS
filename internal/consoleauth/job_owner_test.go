package consoleauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJobOwnerLocalRecheckDoesNotRequireLiveBrowser(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	state := StateFull
	store := openTestStoreWithState(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, clock, func(context.Context) (ConsoleState, error) { return state, nil })
	if store.CheckJobOwner(ctx, "local-owner", "", "") == nil {
		t.Fatal("missing owner authorized")
	}
	if err := store.SetOwnerPassword(ctx, "owner-test-password-1234"); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeLocalSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckJobOwner(ctx, "local-owner", "", ""); err != nil {
		t.Fatal("browser logout cancelled persisted actor", err)
	}
	for _, actor := range []string{"bootstrap:test", "enrollment:test", "root", "", "local-owner-extra"} {
		if store.CheckJobOwner(ctx, actor, "", "") == nil {
			t.Fatal("invalid actor accepted")
		}
	}
	state = StateBootstrap
	if store.CheckJobOwner(ctx, "local-owner", "", "") == nil {
		t.Fatal("non-full state accepted")
	}
	state = StateFull
	c, cancel := context.WithCancel(ctx)
	cancel()
	if store.CheckJobOwner(c, "local-owner", "", "") == nil {
		t.Fatal("cancelled check accepted")
	}
}

func TestJobOwnerProxyIsLocallyBoundAndNeverRenews(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	store := openTestStoreWithState(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, clock, func(context.Context) (ConsoleState, error) { return StateFull, nil })
	i := ProxyIdentity{Issuer: "https://iam.example.test", Subject: "stable-person", SemanticRole: "platform_admin", DirectoryGroup: "owners", AuthenticatedAt: clock.Now().Add(-time.Minute), ExpiresAt: clock.Now().Add(time.Hour), AssertionDigest: strings.Repeat("a", 64)}
	if _, err := store.RefreshProxySession(ctx, ProxySessionRefreshRequest{Origin: "https://anas.example.test", Identity: i}); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(i.Issuer + "\x00" + i.Subject))
	actor := "oidc:" + hex.EncodeToString(digest[:])
	path := filepath.Join(store.Directory(), proxyFileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if err := store.CheckJobOwner(ctx, actor, i.Issuer, i.DirectoryGroup); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("job check refreshed a browser credential")
	}
	for _, pair := range [][2]string{{"", i.DirectoryGroup}, {i.Issuer, "different"}, {"https://other.example.test", i.DirectoryGroup}} {
		if store.CheckJobOwner(ctx, actor, pair[0], pair[1]) == nil {
			t.Fatal("identity policy override accepted")
		}
	}
	clock.Advance(time.Hour)
	if store.CheckJobOwner(ctx, actor, i.Issuer, i.DirectoryGroup) == nil {
		t.Fatal("expired authority accepted")
	}
}
