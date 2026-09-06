package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testBootstrapper(t *testing.T, scoping TokenScoping) (*Bootstrapper, *fakeAdmin, *MemoryStore) {
	t.Helper()
	admin, store := newFakeAdmin(), NewMemoryStore()
	cfg := testConfig(t)
	cfg.WebhookURL = "https://agent.example:8443" + WebhookPath
	return &Bootstrapper{
		Config: cfg, Admin: admin, Store: store, Redactor: NewRedactor(testSecret),
		Scoping: scoping, Log: func(string) {},
	}, admin, store
}

// AGENT-R-005, AGENT-R-006: each agent gets its own account, a token limited to
// the enabled repositories, and its own SSH key -- with no operator step.
func TestBootstrapProvisionsAccountTokenAndKey(t *testing.T) {
	bootstrapper, admin, store := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	identities, err := store.Identities(ctx)
	if err != nil {
		t.Fatalf("Identities: %v", err)
	}
	if len(identities) != 1 || identities[0].Account != "agent-codex" {
		t.Fatalf("identities = %+v, want one account per runtime", identities)
	}
	if identities[0].TokenID == 0 || identities[0].SSHKeyID == 0 {
		t.Fatalf("identity = %+v, want a token and an SSH key", identities[0])
	}
	// The token value is never stored, only its fingerprint.
	if strings.Contains(identities[0].TokenFingerprint, "token-value") {
		t.Fatalf("the token value was stored: %q", identities[0].TokenFingerprint)
	}
	token := admin.tokens["agent-codex"][identities[0].TokenID]
	if len(token.Repositories) != 1 || token.Repositories[0] != (ForgejoTarget{Owner: "anas-project", Name: "ANAS"}) {
		t.Fatalf("token repositories = %v, want the enabled repository only", token.Repositories)
	}
	// The grant has to come first: Forgejo resolves the repository list as the
	// agent, so minting before the collaborator grant fails with a message that
	// says the repository does not exist.
	if err := admin.calledInOrder(
		"EnsureCollaborator:anas-project/ANAS:agent-codex:read", "CreateToken:agent-codex",
	); err != nil {
		t.Fatalf("provisioning order: %v", err)
	}
	for _, scope := range token.Scopes {
		if strings.HasPrefix(scope, "write:repository") {
			t.Fatalf("the discussion token holds %q; write access belongs to the execution phase only", scope)
		}
	}
}

// A second pass must not mint a second account or a second token.
func TestBootstrapIsIdempotent(t *testing.T) {
	bootstrapper, admin, store := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	for pass := 0; pass < 3; pass++ {
		if err := bootstrapper.Reconcile(ctx); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	identities, _ := store.Identities(ctx)
	if len(identities) != 1 {
		t.Fatalf("identities = %d, want 1 after three passes", len(identities))
	}
	if admin.liveTokens("agent-codex") != 1 {
		t.Fatalf("live tokens = %d, want 1", admin.liveTokens("agent-codex"))
	}
}

// AGENT-R-006: the documented degradation. When Forgejo cannot limit a token to
// repositories, least privilege comes from one account per agent per
// repository instead, and the account names stay stable across runs.
func TestPerRepositoryAccountDegradation(t *testing.T) {
	bootstrapper, admin, store := testBootstrapper(t, ScopingPerRepoAccount)
	second, err := ParseRepo("anas-project/anas-agent")
	if err != nil {
		t.Fatalf("ParseRepo: %v", err)
	}
	bootstrapper.Config.RepositoryAllow = append(bootstrapper.Config.RepositoryAllow, second)
	admin.scopeOff = true
	ctx := context.Background()
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	identities, _ := store.Identities(ctx)
	if len(identities) != 2 {
		t.Fatalf("identities = %d, want one per repository", len(identities))
	}
	seen := map[string]bool{}
	for _, identity := range identities {
		if identity.Scope == "" {
			t.Fatalf("identity %+v has no repository scope under the degradation", identity)
		}
		if seen[identity.Account] {
			t.Fatalf("account %q is reused across repositories, which defeats the degradation", identity.Account)
		}
		seen[identity.Account] = true
	}
	// Re-running must reach the same account names, not new ones.
	before := len(admin.users)
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(admin.users) != before {
		t.Fatalf("accounts grew from %d to %d on a second pass", before, len(admin.users))
	}
}

// AGENT-R-007: rotation issues the replacement, stores it, then revokes the
// predecessor -- and ends with exactly one live token and one live key.
func TestRotationReplacesCredentialsInOrder(t *testing.T) {
	bootstrapper, admin, store := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	identities, _ := store.Identities(ctx)
	original := identities[0]

	if err := bootstrapper.Rotate(ctx, original); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := admin.calledInOrder(
		"CreateToken:agent-codex", "AddKey:agent-codex", "CreateToken:agent-codex",
		"AddKey:agent-codex", "DeleteToken:agent-codex:"+strconv.FormatInt(original.TokenID, 10),
	); err != nil {
		t.Fatalf("rotation order: %v", err)
	}
	if admin.liveTokens("agent-codex") != 1 || admin.liveKeys("agent-codex") != 1 {
		t.Fatalf("after rotation: %d token(s), %d key(s); want exactly one of each",
			admin.liveTokens("agent-codex"), admin.liveKeys("agent-codex"))
	}
	rotated, _ := store.Identities(ctx)
	if rotated[0].Generation != original.Generation+1 || rotated[0].TokenID == original.TokenID {
		t.Fatalf("identity = %+v, want a new token at the next generation", rotated[0])
	}
}

// AGENT-R-007: a rotation that fails part-way must not leave two live
// credentials. The replacement is reclaimed before the failure is reported.
func TestFailedRotationLeavesNoDoubleLiveCredential(t *testing.T) {
	bootstrapper, admin, store := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	identities, _ := store.Identities(ctx)
	original := identities[0]

	admin.failOn["AddKey"] = errors.New("Forgejo rejected the key")
	if err := bootstrapper.Rotate(ctx, original); err == nil {
		t.Fatal("Rotate succeeded although the SSH key could not be added")
	}
	if admin.liveTokens("agent-codex") != 1 {
		t.Fatalf("live tokens = %d after a failed rotation, want 1", admin.liveTokens("agent-codex"))
	}
	if _, stillThere := admin.tokens["agent-codex"][original.TokenID]; !stillThere {
		t.Fatal("the failed rotation revoked the working token and left the account with none")
	}
	stored, _ := store.Identities(ctx)
	if stored[0].TokenID != original.TokenID {
		t.Fatalf("stored identity moved to %d although the rotation failed", stored[0].TokenID)
	}
}

// A failure where even the reclaim fails has to say so loudly: that is the one
// case where two credentials really are live and a person must intervene.
func TestUnreclaimableRotationSaysTwoCredentialsAreLive(t *testing.T) {
	bootstrapper, admin, store := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	identities, _ := store.Identities(ctx)

	admin.failOn["AddKey"] = errors.New("Forgejo rejected the key")
	admin.failOn["DeleteToken"] = errors.New("Forgejo refused the revocation")
	err := bootstrapper.Rotate(ctx, identities[0])
	if err == nil || !strings.Contains(err.Error(), "still live") {
		t.Fatalf("error = %v, want it to report that the new token is still live", err)
	}
}

// AGENT-R-009: the system webhook is registered once, and a changed signing key
// re-registers the same hook rather than leaving a second one behind.
func TestWebhookIsRegisteredOnceAndReRegisteredOnRotation(t *testing.T) {
	bootstrapper, admin, store := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("EnsureWebhook: %v", err)
	}
	if len(admin.hooks) != 1 {
		t.Fatalf("hooks = %d, want 1", len(admin.hooks))
	}
	registration, err := store.Webhook(ctx)
	if err != nil {
		t.Fatalf("Webhook: %v", err)
	}

	// Calling again with an unchanged configuration must not re-register.
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("second EnsureWebhook: %v", err)
	}
	if len(admin.hooks) != 1 {
		t.Fatalf("hooks = %d after an unchanged pass, want 1", len(admin.hooks))
	}

	rotated := "fedcba9876543210fedcba9876543210"
	bootstrapper.Config.WebhookSecret = rotated
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("EnsureWebhook after rotation: %v", err)
	}
	if len(admin.hooks) != 1 {
		t.Fatalf("hooks = %d after rotation, want the same hook updated in place", len(admin.hooks))
	}
	if admin.hookSecret(registration.HookID) != rotated {
		t.Fatal("Forgejo is still signing with the previous key; the ingress would reject every delivery")
	}
	updated, _ := store.Webhook(ctx)
	if updated.SecretFingerprint == registration.SecretFingerprint {
		t.Fatal("the recorded fingerprint did not follow the rotation")
	}
}

// A hook deleted in the Forgejo UI has to come back on the next pass; only
// Forgejo knows it is gone, so the recorded registration is not enough.
func TestWebhookIsRecreatedWhenDeletedUpstream(t *testing.T) {
	bootstrapper, admin, _ := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("EnsureWebhook: %v", err)
	}
	for id := range admin.hooks {
		delete(admin.hooks, id)
	}
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("EnsureWebhook after deletion: %v", err)
	}
	if len(admin.hooks) != 1 {
		t.Fatalf("hooks = %d, want the deleted hook recreated", len(admin.hooks))
	}
}

func TestGeneratedSSHKeyIsUsable(t *testing.T) {
	public, private, err := generateSSHKey()
	if err != nil {
		t.Fatalf("generateSSHKey: %v", err)
	}
	if !strings.HasPrefix(public, "ssh-ed25519 ") {
		t.Fatalf("public key = %q, want an ed25519 authorized-keys line", public)
	}
	if !strings.HasPrefix(private, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatalf("private key does not look like an OpenSSH key: %q", firstLine(private))
	}
	other, _, err := generateSSHKey()
	if err != nil {
		t.Fatalf("generateSSHKey: %v", err)
	}
	if other == public {
		t.Fatal("two generated keys are identical")
	}
}

func firstLine(value string) string {
	if index := strings.Index(value, "\n"); index >= 0 {
		return value[:index]
	}
	return value
}

// AGENT-R-007: rotation is unattended. Reconcile itself replaces a credential
// once it is older than the policy, so nothing depends on an operator
// remembering to run it.
func TestReconcileRotatesAgedCredentialsOnItsOwn(t *testing.T) {
	bootstrapper, admin, store := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	bootstrapper.Now = func() time.Time { return now }
	bootstrapper.MaxAge = 30 * 24 * time.Hour

	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	identities, _ := store.Identities(ctx)
	original := identities[0]

	// One day later nothing is due, so a frequent sweep must not churn
	// credentials.
	bootstrapper.Now = func() time.Time { return now.Add(24 * time.Hour) }
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	fresh, _ := store.Identities(ctx)
	if fresh[0].TokenID != original.TokenID {
		t.Fatal("a credential well inside its lifetime was rotated")
	}

	// Past the policy age it is replaced without anyone asking.
	bootstrapper.Now = func() time.Time { return now.Add(31 * 24 * time.Hour) }
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rotated, _ := store.Identities(ctx)
	if rotated[0].TokenID == original.TokenID {
		t.Fatal("an over-age credential was not rotated")
	}
	if admin.liveTokens("agent-codex") != 1 || admin.liveKeys("agent-codex") != 1 {
		t.Fatalf("after the automatic rotation: %d token(s), %d key(s); want one of each",
			admin.liveTokens("agent-codex"), admin.liveKeys("agent-codex"))
	}
}

// AGENT-R-006: a repository-limited token may only carry issue and repository
// scopes. Forgejo answers any other combination with a 400, so a scope added to
// the discussion set without noticing would silently cost the restriction.
func TestDiscussionScopesStayCompatibleWithRepositoryLimiting(t *testing.T) {
	allowed := map[string]bool{}
	for _, scope := range repositoryScopedScopes {
		allowed[scope] = true
	}
	for _, scope := range discussionScopes {
		if !allowed[scope] {
			t.Errorf("discussion scope %q cannot be combined with a repository restriction; "+
				"Forgejo allows only %v", scope, repositoryScopedScopes)
		}
	}
	for _, scope := range discussionScopes {
		if scope == "write:repository" {
			t.Error("the discussion token holds write:repository; that belongs to an execution job")
		}
	}
}

// The pinned Forgejo answers GET /admin/hooks with an empty array even when
// hooks exist, so a reconciliation that judged existence by listing would
// register a new hook on every sweep. Existence is an id lookup instead.
func TestWebhookSurvivesAnEmptyHookListing(t *testing.T) {
	bootstrapper, admin, _ := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("EnsureWebhook: %v", err)
	}
	for sweep := 0; sweep < 5; sweep++ {
		if err := bootstrapper.EnsureWebhook(ctx); err != nil {
			t.Fatalf("sweep %d: %v", sweep, err)
		}
	}
	if len(admin.hooks) != 1 {
		t.Fatalf("hooks = %d after five sweeps against an empty listing, want 1", len(admin.hooks))
	}
}

// The server expands a requested event into its family, so the stored list
// never equals the requested one. Judging drift on that would re-register
// forever; the URL and the signing key are what this module controls.
func TestExpandedEventListIsNotTreatedAsDrift(t *testing.T) {
	bootstrapper, admin, _ := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("EnsureWebhook: %v", err)
	}
	var id int64
	for hookID := range admin.hooks {
		id = hookID
	}
	if len(admin.hooks[id].Events) == len(subscribedEvents) {
		t.Fatal("the fake did not expand the event list, so this test proves nothing")
	}
	admin.calls = nil
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("second EnsureWebhook: %v", err)
	}
	for _, call := range admin.calls {
		if strings.HasPrefix(call, "CreateSystemHook") || strings.HasPrefix(call, "UpdateSystemHook") {
			t.Fatalf("an unchanged configuration triggered %s", call)
		}
	}
}

// A hook this deployment lost the record of, still pointing at the ingress, is
// adopted rather than duplicated -- when the listing reports it at all.
func TestWebhookIsAdoptedFromTheListingWhenRecordIsLost(t *testing.T) {
	bootstrapper, admin, store := testBootstrapper(t, ScopingRepositories)
	ctx := context.Background()
	admin.listHooks = true
	if _, err := admin.CreateSystemHook(ctx, ForgejoHookSpec{
		URL: bootstrapper.Config.WebhookURL, Secret: "an-older-secret", Events: subscribedEvents, Active: true,
	}); err != nil {
		t.Fatalf("seed hook: %v", err)
	}
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("EnsureWebhook: %v", err)
	}
	if len(admin.hooks) != 1 {
		t.Fatalf("hooks = %d, want the existing hook adopted rather than duplicated", len(admin.hooks))
	}
	recorded, err := store.Webhook(ctx)
	if err != nil {
		t.Fatalf("Webhook: %v", err)
	}
	if admin.hookSecret(recorded.HookID) != bootstrapper.Config.WebhookSecret {
		t.Fatal("the adopted hook kept the older signing key")
	}
}
