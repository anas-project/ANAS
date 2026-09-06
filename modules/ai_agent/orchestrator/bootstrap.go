package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// tokenName is the prefix every managed token carries. Rotation looks for it to
// find the credentials this module owns, so a token a person minted by hand for
// the same account is left alone.
const tokenName = "anas-ai-agent"

// tokenNameFor appends the generation, because Forgejo requires an access token
// name to be unique per user: minting the replacement before revoking the
// predecessor -- which is the whole of the rotation guarantee -- means both
// exist for a moment, and reusing the name fails that moment with a 400.
func tokenNameFor(generation int) string {
	return tokenName + "-g" + strconv.Itoa(generation)
}

// ManagedToken reports whether a token belongs to this module.
func ManagedToken(name string) bool { return strings.HasPrefix(name, tokenName) }

// keyTitle plays the same role for SSH keys, and carries the generation for the
// same reason: the replacement and the predecessor coexist during a rotation.
const keyTitle = "anas-ai-agent"

func keyTitleFor(generation int) string {
	return keyTitle + "-g" + strconv.Itoa(generation)
}

// credentialMaxAge is how long an agent's token and SSH key may live before the
// next reconciliation replaces them. Rotation has to happen without a person
// (AGENT-R-007), so it is driven by age here rather than by an operator
// remembering; the interval is short enough that a leaked credential expires on
// its own and long enough that a Forgejo outage does not turn into a rotation
// storm.
const credentialMaxAge = 30 * 24 * time.Hour

// discussionScopes is what an agent holds outside a job: enough to read the
// repository and take part in the issue, and nothing that can change code.
// Write access to a repository is added only for the duration of an execution
// job (AGENT-R-006).
//
// The set is not free to grow. A token limited to repositories may carry only
// read:issue, write:issue, read:repository and write:repository -- 15.0.7
// refuses the combination with a 400 -- so adding a scope such as read:user
// would silently trade the repository restriction away for it.
var discussionScopes = []string{"read:repository", "read:issue", "write:issue"}

// repositoryScopedScopes is the whole set a repository-limited token may carry
// on the pinned Forgejo. It is asserted against, rather than assumed, so a
// later scope addition fails a test instead of a deployment.
var repositoryScopedScopes = []string{
	"read:issue", "write:issue", "read:repository", "write:repository",
}

// TokenScoping records whether this Forgejo can limit a token to specific
// repositories. It is discovered rather than assumed, because the answer
// decides the shape of the identity model and the fixed 15.0.7 baseline has
// not been probed yet (AGENT-R-006).
type TokenScoping string

const (
	// ScopingRepositories is the intended model: one account per agent, each
	// token limited to the enabled repositories.
	ScopingRepositories TokenScoping = "repositories"
	// ScopingPerRepoAccount is the documented degradation: Forgejo ignores the
	// repository restriction, so least privilege is recovered by giving each
	// agent a separate account per repository.
	ScopingPerRepoAccount TokenScoping = "per_repo_account"
)

// Bootstrapper creates and rotates every identity this module owns. It runs on
// a schedule rather than once at install, so an account deleted by hand, a
// token revoked upstream or a repository added to the allowlist all heal on the
// next pass without an operator (AGENT-R-005, AGENT-R-007).
type Bootstrapper struct {
	Config   Config
	Admin    ForgejoAdmin
	Store    Store
	Redactor *Redactor
	Scoping  TokenScoping
	Now      func() time.Time
	Log      func(string)
	// TokenSink receives each freshly minted token. In the deployment it hands
	// the value to the in-memory credential holder the outbox uses; nothing
	// writes it to disk or to the database.
	TokenSink func(identity AgentIdentity, token string)
	// MaxAge overrides how old a credential may get before Reconcile replaces
	// it. Zero means credentialMaxAge.
	MaxAge time.Duration
}

func (b *Bootstrapper) maxAge() time.Duration {
	if b.MaxAge > 0 {
		return b.MaxAge
	}
	return credentialMaxAge
}

func (b *Bootstrapper) now() time.Time {
	if b.Now != nil {
		return b.Now().UTC()
	}
	return time.Now().UTC()
}

func (b *Bootstrapper) log(format string, args ...any) {
	if b.Log == nil {
		return
	}
	b.Log(b.Redactor.String(fmt.Sprintf(format, args...)))
}

// scopes returns the identity keys this deployment needs: one per runtime under
// the intended model, one per runtime and repository under the degradation.
func (b *Bootstrapper) scopes(runtime Runtime) []AgentIdentity {
	if b.Scoping == ScopingPerRepoAccount {
		identities := make([]AgentIdentity, 0, len(b.Config.RepositoryAllow))
		for _, repo := range b.Config.RepositoryAllow {
			identities = append(identities, AgentIdentity{
				Runtime: runtime.ID, Scope: repo.String(),
				Account: runtime.Account + "-" + accountSuffix(repo),
			})
		}
		return identities
	}
	return []AgentIdentity{{Runtime: runtime.ID, Account: runtime.Account}}
}

// accountSuffix turns a repository into an account-name fragment. Forgejo user
// names cannot contain a slash, and the suffix has to stay stable across runs,
// so it is a short digest rather than a sanitised name that could collide.
func accountSuffix(repo Repo) string {
	return fingerprint(repo.String())[:10]
}

// Reconcile brings every agent identity to the desired state and returns what
// it changed. It is safe to call repeatedly.
func (b *Bootstrapper) Reconcile(ctx context.Context) error {
	existing, err := b.Store.Identities(ctx)
	if err != nil {
		return err
	}
	known := map[string]AgentIdentity{}
	for _, identity := range existing {
		known[identity.Runtime+"|"+identity.Scope] = identity
	}
	repositories := b.repositoryList()
	for _, runtime := range b.Config.Runtimes.Entries() {
		for _, desired := range b.scopes(runtime) {
			current, seen := known[desired.Runtime+"|"+desired.Scope]
			if seen && current.TokenID != 0 {
				if b.now().Sub(current.RotatedAt) < b.maxAge() {
					continue
				}
				if err := b.Rotate(ctx, current); err != nil {
					return err
				}
				continue
			}
			scoped := repositories
			if desired.Scope != "" {
				scoped = []string{desired.Scope}
			}
			if err := b.provision(ctx, desired, scoped); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Bootstrapper) repositoryList() []string {
	repositories := make([]string, 0, len(b.Config.RepositoryAllow))
	for _, repo := range b.Config.RepositoryAllow {
		repositories = append(repositories, repo.String())
	}
	sort.Strings(repositories)
	return repositories
}

func (b *Bootstrapper) provision(ctx context.Context, desired AgentIdentity, repositories []string) error {
	user, err := b.Admin.EnsureUser(ctx, desired.Account, desired.Account+"@localhost.invalid")
	if err != nil {
		return err
	}
	if err := b.grantAccess(ctx, desired.Account, repositories); err != nil {
		return err
	}
	token, err := b.Admin.CreateToken(ctx, desired.Account, tokenNameFor(1), discussionScopes, repositories)
	if err != nil {
		return err
	}
	publicKey, privateKey, err := generateSSHKey()
	if err != nil {
		return err
	}
	b.Redactor.Add(privateKey)
	key, err := b.Admin.AddKey(ctx, desired.Account, keyTitleFor(1), publicKey)
	if err != nil {
		// The token was already minted. Leaving it behind would mean a
		// half-provisioned identity that no later pass recognises, so it is
		// revoked before the failure is reported.
		if revokeErr := b.Admin.DeleteToken(ctx, desired.Account, token.ID); revokeErr != nil {
			return fmt.Errorf("add SSH key for %s failed (%w) and revoking its token also failed: %s",
				desired.Account, err, b.Redactor.Error(revokeErr))
		}
		return err
	}
	identity := AgentIdentity{
		Runtime: desired.Runtime, Scope: desired.Scope, Account: desired.Account,
		ForgejoUserID: user.ID, TokenID: token.ID, TokenFingerprint: fingerprint(token.Token),
		SSHKeyID: key.ID, Generation: 1, RotatedAt: b.now(),
	}
	if err := b.Store.SaveIdentity(ctx, identity); err != nil {
		return err
	}
	if b.TokenSink != nil {
		b.TokenSink(identity, token.Token)
	}
	b.log("provisioned agent identity %s for runtime %s", desired.Account, desired.Runtime)
	return nil
}

// grantAccess makes the agent a collaborator on every repository its token will
// be limited to. Forgejo resolves the token's `repositories` in the agent's own
// context, so this has to happen first or the request comes back saying the
// repository does not exist. The permission is `read`: write access to a
// repository belongs to an execution job and is granted for its duration, not
// held standing (AGENT-R-006).
func (b *Bootstrapper) grantAccess(ctx context.Context, account string, repositories []string) error {
	for _, name := range repositories {
		repo, err := ParseRepo(name)
		if err != nil {
			return err
		}
		if err := b.Admin.EnsureCollaborator(ctx, repo, account, "read"); err != nil {
			return err
		}
	}
	return nil
}

// Rotate replaces one identity's token and SSH key. The order is deliberate:
// mint the replacement, prove it was stored, then revoke the predecessor. If
// any step fails the new credential is revoked, so a failed rotation leaves
// exactly one live credential -- never two (AGENT-R-007).
func (b *Bootstrapper) Rotate(ctx context.Context, identity AgentIdentity) error {
	repositories := b.repositoryList()
	if identity.Scope != "" {
		repositories = []string{identity.Scope}
	}
	if err := b.grantAccess(ctx, identity.Account, repositories); err != nil {
		return err
	}
	token, err := b.Admin.CreateToken(ctx, identity.Account, tokenNameFor(identity.Generation+1), discussionScopes, repositories)
	if err != nil {
		return err
	}
	publicKey, privateKey, err := generateSSHKey()
	if err != nil {
		return b.abandon(ctx, identity.Account, token.ID, 0, err)
	}
	b.Redactor.Add(privateKey)
	key, err := b.Admin.AddKey(ctx, identity.Account, keyTitleFor(identity.Generation+1), publicKey)
	if err != nil {
		return b.abandon(ctx, identity.Account, token.ID, 0, err)
	}
	rotated := identity
	rotated.TokenID, rotated.TokenFingerprint = token.ID, fingerprint(token.Token)
	rotated.SSHKeyID, rotated.Generation, rotated.RotatedAt = key.ID, identity.Generation+1, b.now()
	if err := b.Store.SaveIdentity(ctx, rotated); err != nil {
		return b.abandon(ctx, identity.Account, token.ID, key.ID, err)
	}
	// Only now is the predecessor removed. A failure here leaves the old
	// credential live, which is visible on the next pass and is far better than
	// an account whose only recorded credential no longer exists.
	if identity.TokenID != 0 {
		if err := b.Admin.DeleteToken(ctx, identity.Account, identity.TokenID); err != nil {
			return fmt.Errorf("revoke the previous token for %s: %w", identity.Account, err)
		}
	}
	if identity.SSHKeyID != 0 {
		if err := b.Admin.DeleteKey(ctx, identity.Account, identity.SSHKeyID); err != nil {
			return fmt.Errorf("revoke the previous SSH key for %s: %w", identity.Account, err)
		}
	}
	if b.TokenSink != nil {
		b.TokenSink(rotated, token.Token)
	}
	b.log("rotated agent identity %s to generation %d", rotated.Account, rotated.Generation)
	return nil
}

// abandon revokes a partially issued replacement so a failed rotation cannot
// leave two usable credentials behind.
func (b *Bootstrapper) abandon(ctx context.Context, account string, tokenID, keyID int64, cause error) error {
	if keyID != 0 {
		if err := b.Admin.DeleteKey(ctx, account, keyID); err != nil {
			return fmt.Errorf("rotation for %s failed (%w) and the new SSH key is still live: %s",
				account, cause, b.Redactor.Error(err))
		}
	}
	if err := b.Admin.DeleteToken(ctx, account, tokenID); err != nil {
		return fmt.Errorf("rotation for %s failed (%w) and the new token is still live: %s",
			account, cause, b.Redactor.Error(err))
	}
	return cause
}

// EnsureWebhook registers the system webhook, or re-registers it when the URL
// or the signing key has changed. Rotating the secret and re-registering the
// hook is one operation for a reason: doing them separately leaves a window in
// which Forgejo signs with a key the ingress has already stopped accepting
// (AGENT-R-009).
//
// Two facts about the pinned Forgejo shape this. GET /admin/hooks answers with
// an empty array even when hooks exist, so existence is checked by id and the
// listing is only a best-effort way to adopt a hook this deployment lost track
// of. And the server expands a requested event into a family -- asking for
// `issues` stores five entries -- so the recorded event list is what was asked
// for, and drift is judged on the URL and the key, which are the parts this
// module actually controls.
func (b *Bootstrapper) EnsureWebhook(ctx context.Context) error {
	desired := ForgejoHookSpec{
		URL: b.Config.WebhookURL, Secret: b.Config.WebhookSecret,
		Events: append([]string(nil), subscribedEvents...), Active: true,
	}
	wanted := fingerprint(b.Config.WebhookSecret)

	recorded, err := b.Store.Webhook(ctx)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil && recorded.HookID != 0 {
		if _, lookupErr := b.Admin.SystemHook(ctx, recorded.HookID); lookupErr == nil {
			if recorded.URL == desired.URL && recorded.SecretFingerprint == wanted {
				return nil
			}
			updated, updateErr := b.Admin.UpdateSystemHook(ctx, recorded.HookID, desired)
			if updateErr != nil {
				return updateErr
			}
			return b.recordWebhook(ctx, updated.ID, desired, wanted)
		} else if !isStatus(lookupErr, http.StatusNotFound) {
			return lookupErr
		}
		// The hook is gone upstream -- deleted in the UI, or the instance was
		// rebuilt. Fall through and register a new one.
	}

	// No usable record. Adopt a hook already pointing at this ingress if the
	// listing happens to report one, so a lost record does not leave two hooks
	// delivering to the same URL.
	hooks, err := b.Admin.ListSystemHooks(ctx)
	if err != nil {
		return err
	}
	for _, hook := range hooks {
		if hook.Config["url"] != desired.URL {
			continue
		}
		updated, err := b.Admin.UpdateSystemHook(ctx, hook.ID, desired)
		if err != nil {
			return err
		}
		return b.recordWebhook(ctx, updated.ID, desired, wanted)
	}
	created, err := b.Admin.CreateSystemHook(ctx, desired)
	if err != nil {
		return err
	}
	return b.recordWebhook(ctx, created.ID, desired, wanted)
}

func (b *Bootstrapper) recordWebhook(ctx context.Context, id int64, spec ForgejoHookSpec, secretFingerprint string) error {
	b.log("registered the Forgejo system webhook at %s", spec.URL)
	return b.Store.SaveWebhook(ctx, WebhookRegistration{
		HookID: id, URL: spec.URL, SecretFingerprint: secretFingerprint, Events: spec.Events,
	})
}

// generateSSHKey returns an OpenSSH authorized-keys line and the matching
// private key in OpenSSH format. Ed25519 rather than RSA: it is the shorter,
// faster key every supported Forgejo accepts, and there is no interoperability
// reason here to carry an RSA key's size.
func generateSSHKey() (string, string, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(private, keyTitle)
	if err != nil {
		return "", "", err
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))) + " " + keyTitle
	return authorized, string(encodePEM(block.Type, block.Bytes)), nil
}

func encodePEM(blockType string, der []byte) []byte {
	const width = 64
	var out strings.Builder
	out.WriteString("-----BEGIN " + blockType + "-----\n")
	encoded := base64.StdEncoding.EncodeToString(der)
	for len(encoded) > width {
		out.WriteString(encoded[:width] + "\n")
		encoded = encoded[width:]
	}
	if encoded != "" {
		out.WriteString(encoded + "\n")
	}
	out.WriteString("-----END " + blockType + "-----\n")
	return []byte(out.String())
}

// randomSecret produces a placeholder password for an account that must exist
// but must never be signed in to.
func randomSecret(byteCount int) (string, error) {
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
