package ingress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
)

var (
	ErrRuleConflict = errors.New("managed Cloudflare rule changed or conflicts")
	ErrRuleUnknown  = errors.New("Cloudflare rule mutation outcome unconfirmed")
	ErrRateLimited  = errors.New("Cloudflare API rate limited")
	ErrRuleRejected = errors.New("Cloudflare rule request rejected")
	cloudflareID    = regexp.MustCompile(`^[a-f0-9]{32}$`)
	cloudflareToken = regexp.MustCompile(`^[A-Za-z0-9_-]{20,256}$`)
	workerName      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

type CloudflareAction struct {
	Type  string   `json:"type"`
	Value []string `json:"value,omitempty"`
}
type CloudflareMatcher struct {
	Type  string `json:"type"`
	Field string `json:"field,omitempty"`
	Value string `json:"value,omitempty"`
}
type CloudflareRule struct {
	ID       string              `json:"id,omitempty"`
	Tag      string              `json:"tag,omitempty"`
	Name     string              `json:"name"`
	Enabled  bool                `json:"enabled"`
	Actions  []CloudflareAction  `json:"actions"`
	Matchers []CloudflareMatcher `json:"matchers"`
	Priority int                 `json:"priority"`
	Source   string              `json:"source"`
}
type ruleEnvelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Info    struct {
		Page       int  `json:"page"`
		Count      int  `json:"count"`
		PerPage    int  `json:"per_page"`
		TotalPages *int `json:"total_pages"`
		TotalCount *int `json:"total_count"`
	} `json:"result_info"`
}

// CloudflareRules exposes only zone-scoped Email Routing rules. It reuses the
// source's verified transport and explicit proxy, but a distinct API token.
// It cannot deploy Workers, subscribe to billing or alter DNS. The caller must
// persist mutation intent before invoking a write, then independently read back.
type CloudflareRules struct {
	source              *SourceClient
	zone, worker, token string
}

func NewCloudflareRules(source *SourceClient, zone, worker, token string) (*CloudflareRules, error) {
	if source == nil || !cloudflareID.MatchString(zone) || !workerName.MatchString(worker) || !cloudflareToken.MatchString(token) || token == source.cfg.Token || token == source.cfg.ControlToken {
		return nil, ErrConfiguration
	}
	return &CloudflareRules{source: source, zone: zone, worker: worker, token: token}, nil
}
func (c *CloudflareRules) name(address string) string {
	sum := sha256.Sum256([]byte(address))
	return "ANAS Mail " + c.source.cfg.ID + "/" + c.source.cfg.Generation + "/" + hex.EncodeToString(sum[:])
}
func (c *CloudflareRules) addressAllowed(address string) bool {
	local, domain, ok := strings.Cut(address, "@")
	canonical, e := directorymail.Address(local, domain)
	if !ok || e != nil || canonical != address || len(address) > 90 {
		return false
	}
	for _, allowed := range c.source.cfg.Domains {
		if domain == allowed {
			return true
		}
	}
	return false
}
func (c *CloudflareRules) owns(r CloudflareRule) bool {
	return cloudflareID.MatchString(r.ID) && (r.Tag == "" || r.Tag == r.ID) && r.Source == "api" && r.Priority == 0 && len(r.Actions) == 1 && r.Actions[0].Type == "worker" && len(r.Actions[0].Value) == 1 && r.Actions[0].Value[0] == c.worker && len(r.Matchers) == 1 && r.Matchers[0].Type == "literal" && r.Matchers[0].Field == "to" && c.addressAllowed(r.Matchers[0].Value) && r.Name == c.name(r.Matchers[0].Value)
}
func normalizeRule(r *CloudflareRule) bool {
	if r.ID == "" {
		r.ID = r.Tag
	}
	return cloudflareID.MatchString(r.ID) && (r.Tag == "" || r.Tag == r.ID)
}
func (c *CloudflareRules) request(ctx context.Context, method, path string, input any) (ruleEnvelope, error) {
	var raw []byte
	var e error
	if input != nil {
		raw, e = json.Marshal(input)
		if e != nil || len(raw) > 8192 {
			return ruleEnvelope{}, ErrConfiguration
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, "https://api.cloudflare.com/client/v4/zones/"+c.zone+"/email/routing/rules"+path, bytes.NewReader(raw))
	if e != nil {
		return ruleEnvelope{}, ErrConfiguration
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, e := c.source.http.Do(r)
	if e != nil {
		if method != "GET" {
			return ruleEnvelope{}, ErrRuleUnknown
		}
		return ruleEnvelope{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return ruleEnvelope{}, ErrAuthentication
	}
	if response.StatusCode == 429 {
		return ruleEnvelope{}, ErrRateLimited
	}
	if response.StatusCode == 404 {
		return ruleEnvelope{}, ErrGone
	}
	if response.StatusCode == 409 {
		return ruleEnvelope{}, ErrRuleConflict
	}
	if response.StatusCode >= 500 {
		if method != "GET" {
			return ruleEnvelope{}, ErrRuleUnknown
		}
		return ruleEnvelope{}, ErrUnavailable
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return ruleEnvelope{}, ErrIdentity
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ruleEnvelope{}, ErrRuleRejected
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	var out ruleEnvelope
	if e != nil || len(data) > 2<<20 || json.Unmarshal(data, &out) != nil || !out.Success || len(out.Result) == 0 || bytes.Equal(out.Result, []byte("null")) {
		if method != "GET" {
			return ruleEnvelope{}, ErrRuleUnknown
		}
		return ruleEnvelope{}, ErrIdentity
	}
	return out, nil
}
func (c *CloudflareRules) List(ctx context.Context) ([]CloudflareRule, error) {
	out := []CloudflareRule{}
	seen := map[string]bool{}
	total := -1
	for page := 1; page <= 100; page++ {
		v, e := c.request(ctx, "GET", "?per_page=50&page="+strconv.Itoa(page), nil)
		if e != nil {
			return nil, e
		}
		var rows []CloudflareRule
		if json.Unmarshal(v.Result, &rows) != nil || rows == nil || len(rows) > 50 || v.Info.Page != page || v.Info.Count != len(rows) || v.Info.PerPage != 50 || v.Info.TotalCount == nil || *v.Info.TotalCount < 0 || *v.Info.TotalCount > 5000 {
			return nil, ErrIdentity
		}
		if total < 0 {
			total = *v.Info.TotalCount
		} else if total != *v.Info.TotalCount {
			return nil, ErrIdentity
		}
		// The actual rules API reports total_count/per_page without
		// total_pages. Derive the bound from the required count, and still
		// reject an inconsistent optional page count if the provider adds it.
		pages := (total + 49) / 50
		if pages == 0 {
			pages = 1
		}
		if v.Info.TotalPages != nil && *v.Info.TotalPages != pages && !(total == 0 && *v.Info.TotalPages == 0) {
			return nil, ErrIdentity
		}
		for _, r := range rows {
			if !normalizeRule(&r) || seen[r.ID] {
				return nil, ErrIdentity
			}
			seen[r.ID] = true
			out = append(out, r)
		}
		if page >= pages {
			if len(out) != total {
				return nil, ErrIdentity
			}
			return out, nil
		}
	}
	return nil, ErrIdentity
}
func (c *CloudflareRules) Read(ctx context.Context, id string) (CloudflareRule, error) {
	if !cloudflareID.MatchString(id) {
		return CloudflareRule{}, ErrConfiguration
	}
	v, e := c.request(ctx, "GET", "/"+id, nil)
	if e != nil {
		return CloudflareRule{}, e
	}
	var out CloudflareRule
	if json.Unmarshal(v.Result, &out) != nil || !normalizeRule(&out) || out.ID != id {
		return CloudflareRule{}, ErrIdentity
	}
	return out, nil
}
func (c *CloudflareRules) Create(ctx context.Context, address string) (CloudflareRule, error) {
	if !c.addressAllowed(address) {
		return CloudflareRule{}, ErrConfiguration
	}
	in := CloudflareRule{Name: c.name(address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "worker", Value: []string{c.worker}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: address}}}
	v, e := c.request(ctx, "POST", "", in)
	if e != nil {
		return CloudflareRule{}, e
	}
	var out CloudflareRule
	if json.Unmarshal(v.Result, &out) != nil || !normalizeRule(&out) || !c.owns(out) || !out.Enabled || out.Name != in.Name || out.Matchers[0].Value != address {
		return CloudflareRule{}, ErrRuleUnknown
	}
	return out, nil
}
func (c *CloudflareRules) Delete(ctx context.Context, expected CloudflareRule) error {
	if !c.owns(expected) {
		return ErrRuleConflict
	}
	actual, e := c.Read(ctx, expected.ID)
	if e != nil {
		return e
	}
	actual.Tag, expected.Tag = "", ""
	if !reflect.DeepEqual(actual, expected) {
		return ErrRuleConflict
	}
	v, e := c.request(ctx, "DELETE", "/"+actual.ID, nil)
	if e != nil {
		return e
	}
	var out struct {
		ID  string `json:"id"`
		Tag string `json:"tag"`
	}
	if json.Unmarshal(v.Result, &out) != nil || out.ID != actual.ID && out.Tag != actual.ID || out.ID != "" && out.ID != actual.ID || out.Tag != "" && out.Tag != actual.ID {
		return ErrRuleUnknown
	}
	return nil
}
