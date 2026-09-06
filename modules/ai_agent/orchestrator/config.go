package main

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
)

// Repo is an `owner/name` pair. The orchestrator never accepts a repository as
// a free string: every entry is parsed once, at load, against the allowlist.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// ParseRepo accepts exactly `owner/name`. Anything else -- an empty half, a
// path traversal, a third segment, a control character -- is a load error, so
// no later code has to re-check it.
func ParseRepo(value string) (Repo, error) {
	owner, name, found := strings.Cut(strings.TrimSpace(value), "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repo{}, fmt.Errorf("repository %q is not owner/name", value)
	}
	for _, part := range []string{owner, name} {
		if part == "." || part == ".." || strings.ContainsAny(part, " \t\r\n") {
			return Repo{}, fmt.Errorf("repository %q is not a usable owner/name", value)
		}
	}
	return Repo{Owner: owner, Name: name}, nil
}

// Config is the whole runtime configuration, already validated. Every field is
// derived from an environment variable the module manifest declares; nothing
// here is read from a file inside a work instance or from an issue body.
type Config struct {
	Enabled  bool
	Listen   string
	Language string

	ForgejoURL       string
	AdminUsername    string
	AdminPassword    string
	WebhookSecret    string
	WebhookURL       string
	RepositoryAllow  []Repo
	Runtimes         *Registry
	WorkspaceScope   string
	EgressAllowlist  []string
	DailyBudgetUSD   int
	JobWallclock     time.Duration
	ReconcileEvery   time.Duration
	SessionRoot      string
	DatabaseURL      string
	DatabaseRedacted string

	// Lease is the compute sandbox this deployment may create work instances
	// in. LeaseError records why it is unusable, which is only tolerable while
	// the module is switched off.
	Lease      computeclient.Lease
	LeaseError error
}

// AllowsRepo reports whether events for a repository may be processed at all.
// A repository that is not on the list is not "unauthorized" -- it is not
// participating, and its deliveries are dropped before any handler sees them
// (AGENT-R-011, AGENT-R-030).
func (c Config) AllowsRepo(repo Repo) bool {
	for _, allowed := range c.RepositoryAllow {
		if allowed == repo {
			return true
		}
	}
	return false
}

// LoadConfig reads and validates the environment. It fails closed: when the
// module is enabled and any precondition is missing, it returns an error rather
// than starting a control plane that can accept a webhook but not act on it
// (AGENT-R-004).
func LoadConfig() (Config, error) {
	return loadConfig(os.Getenv)
}

func loadConfig(lookup func(string) string) (Config, error) {
	get := func(key string) string { return strings.TrimSpace(lookup(key)) }

	cfg := Config{
		Enabled:        get("AI_AGENT_ENABLED") == "true",
		Listen:         valueOr(get("AI_AGENT_LISTEN"), ":8130"),
		Language:       valueOr(get("AI_AGENT_LANGUAGE"), "en"),
		ForgejoURL:     strings.TrimRight(get("AI_AGENT_FORGEJO_URL"), "/"),
		AdminUsername:  get("AI_AGENT_FORGEJO_ADMIN_USERNAME"),
		AdminPassword:  lookup("AI_AGENT_FORGEJO_ADMIN_PASSWORD"),
		WebhookSecret:  lookup("AI_AGENT_WEBHOOK_SECRET"),
		WebhookURL:     get("AI_AGENT_WEBHOOK_URL"),
		WorkspaceScope: valueOr(get("AI_AGENT_WORKSPACE_SCOPE"), "repo"),
		SessionRoot:    valueOr(get("AI_AGENT_SESSION_ROOT"), "/var/lib/anas-ai-agent/sessions"),
	}

	var err error
	if cfg.DailyBudgetUSD, err = wholeNumber(get("AI_AGENT_DAILY_BUDGET_USD"), 20); err != nil {
		return Config{}, fmt.Errorf("AI_AGENT_DAILY_BUDGET_USD: %w", err)
	}
	wallclock, err := wholeNumber(get("AI_AGENT_JOB_WALLCLOCK_MINUTES"), 60)
	if err != nil {
		return Config{}, fmt.Errorf("AI_AGENT_JOB_WALLCLOCK_MINUTES: %w", err)
	}
	cfg.JobWallclock = time.Duration(wallclock) * time.Minute
	reconcile, err := wholeNumber(get("AI_AGENT_RECONCILE_INTERVAL_SECONDS"), 300)
	if err != nil {
		return Config{}, fmt.Errorf("AI_AGENT_RECONCILE_INTERVAL_SECONDS: %w", err)
	}
	cfg.ReconcileEvery = time.Duration(reconcile) * time.Second

	switch cfg.WorkspaceScope {
	case "repo", "job":
	default:
		return Config{}, fmt.Errorf("AI_AGENT_WORKSPACE_SCOPE must be repo or job, got %q", cfg.WorkspaceScope)
	}

	for _, entry := range splitList(get("AI_AGENT_REPOSITORY_ALLOWLIST")) {
		repo, err := ParseRepo(entry)
		if err != nil {
			return Config{}, fmt.Errorf("AI_AGENT_REPOSITORY_ALLOWLIST: %w", err)
		}
		if cfg.AllowsRepo(repo) {
			return Config{}, fmt.Errorf("AI_AGENT_REPOSITORY_ALLOWLIST lists %s more than once", repo)
		}
		cfg.RepositoryAllow = append(cfg.RepositoryAllow, repo)
	}
	sort.Slice(cfg.RepositoryAllow, func(i, j int) bool {
		return cfg.RepositoryAllow[i].String() < cfg.RepositoryAllow[j].String()
	})
	cfg.EgressAllowlist = splitList(get("AI_AGENT_EGRESS_ALLOWLIST"))

	images, err := parseImagePins(get("AI_AGENT_RUNTIME_IMAGES"))
	if err != nil {
		return Config{}, fmt.Errorf("AI_AGENT_RUNTIME_IMAGES: %w", err)
	}
	if cfg.Runtimes, err = NewRegistry(splitList(get("AI_AGENT_RUNTIMES")), images); err != nil {
		return Config{}, fmt.Errorf("AI_AGENT_RUNTIMES: %w", err)
	}

	if cfg.DatabaseURL, cfg.DatabaseRedacted, err = databaseURL(get, lookup); err != nil {
		return Config{}, err
	}

	cfg.Lease, cfg.LeaseError = computeclient.LeaseFromLookup(lookup, "ai_agent", "work_instances")

	if !cfg.Enabled {
		return cfg, nil
	}
	if err := cfg.requireEnabledPreconditions(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// requireEnabledPreconditions is the whole of AGENT-R-004. `enabled` is the one
// switch, and it may only be on when every input the orchestrator needs to do
// its job is present. A half-configured deployment fails to start instead of
// accepting webhooks it cannot act on.
func (c Config) requireEnabledPreconditions() error {
	var missing []string
	if c.ForgejoURL == "" {
		missing = append(missing, "the Forgejo URL")
	} else if parsed, err := url.Parse(c.ForgejoURL); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("AI_AGENT_FORGEJO_URL must be an https URL, got %q", c.ForgejoURL)
	}
	if c.AdminUsername == "" || c.AdminPassword == "" {
		missing = append(missing, "the Forgejo administrator credential")
	}
	if c.WebhookSecret == "" {
		missing = append(missing, "the webhook secret")
	}
	if c.WebhookURL == "" {
		missing = append(missing, "the webhook URL")
	}
	if len(c.RepositoryAllow) == 0 {
		missing = append(missing, "a repository allowlist")
	}
	if c.Runtimes == nil || c.Runtimes.Len() == 0 {
		missing = append(missing, "at least one agent runtime with a pinned image")
	}
	if c.DatabaseURL == "" {
		missing = append(missing, "the orchestration database")
	}
	if c.LeaseError != nil {
		missing = append(missing, "a usable compute binding ("+c.LeaseError.Error()+")")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("ai_agent is enabled but is missing %s", strings.Join(missing, ", "))
}

// databaseURL takes both lookups on purpose. Host, name and user are trimmed,
// because surrounding whitespace there is always an accident. The password is
// read raw: a generated credential may legally start or end with whitespace,
// and trimming it would turn a correct password into an authentication failure
// nobody can explain from the error.
func databaseURL(get func(string) string, raw func(string) string) (string, string, error) {
	host, name := get("AI_AGENT_DB_HOST"), get("AI_AGENT_DB_NAME")
	user, password := get("AI_AGENT_DB_USERNAME"), raw("AI_AGENT_DB_PASSWORD")
	if host == "" && name == "" && user == "" {
		return "", "", nil
	}
	if host == "" || name == "" || user == "" || password == "" {
		return "", "", fmt.Errorf("the orchestration database binding is incomplete")
	}
	port := valueOr(get("AI_AGENT_DB_PORT"), "5432")
	endpoint := url.URL{
		Scheme: "postgres", Host: host + ":" + port, Path: "/" + name,
		User: url.UserPassword(user, password),
	}
	query := url.Values{"sslmode": {"disable"}}
	endpoint.RawQuery = query.Encode()
	redacted := endpoint
	redacted.User = url.User(user)
	return endpoint.String(), redacted.String(), nil
}

func parseImagePins(value string) (map[string]string, error) {
	pins := map[string]string{}
	for _, entry := range splitList(value) {
		id, fingerprint, found := strings.Cut(entry, "=")
		id, fingerprint = strings.TrimSpace(id), strings.TrimSpace(fingerprint)
		if !found || id == "" || fingerprint == "" {
			return nil, fmt.Errorf("%q is not runtime=fingerprint", entry)
		}
		if len(fingerprint) != 64 || strings.TrimLeft(fingerprint, "0123456789abcdef") != "" {
			return nil, fmt.Errorf("runtime %q is not pinned to a SHA-256 fingerprint", id)
		}
		if _, duplicate := pins[id]; duplicate {
			return nil, fmt.Errorf("runtime %q is pinned more than once", id)
		}
		pins[id] = fingerprint
	}
	return pins, nil
}

func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// wholeNumber parses a non-negative integer, falling back when unset. Zero is
// allowed: a daily budget of zero is a meaningful setting, not a mistake.
func wholeNumber(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", value)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%d is negative", parsed)
	}
	return parsed, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
