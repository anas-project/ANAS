package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
)

// ErrDestinationPending means Cloudflare has not verified the exact delivery
// address. It does not grant permission to enable a public forwarding rule.
var ErrDestinationPending = errors.New("cloud forwarding destination awaits verification")

// CloudflareForwardRules handles only forwarding rules for one source and one
// account/zone. Callers must first persist operation intent and independently
// read back every mutation; no automatic adoption of existing destinations or
// rules is provided by this low-level client.
type CloudflareForwardRules struct {
	rules                             *CloudflareRules
	account, domain, destinationToken string
}

func NewCloudflareForwardRules(source *SourceClient, account, zone, domain, destinationToken, routingToken string) (*CloudflareForwardRules, error) {
	if source == nil || source.cfg.ControlToken == "" || !cloudflareID.MatchString(account) || !cloudflareID.MatchString(zone) || !cloudflareToken.MatchString(destinationToken) || !cloudflareToken.MatchString(routingToken) || destinationToken == routingToken || destinationToken == source.cfg.Token || destinationToken == source.cfg.ControlToken || routingToken == source.cfg.Token || routingToken == source.cfg.ControlToken {
		return nil, ErrConfiguration
	}
	canonical, err := directorymail.Domain(domain, "")
	if err != nil || canonical != domain || len("anas-"+strings.Repeat("0", 32)+"@"+domain) > 90 {
		return nil, ErrConfiguration
	}
	for _, d := range source.cfg.Domains {
		if domain == d {
			return nil, ErrConfiguration
		}
	}
	return &CloudflareForwardRules{rules: &CloudflareRules{source: source, zone: zone, token: routingToken}, account: account, domain: domain, destinationToken: destinationToken}, nil
}

// strictObject allows only the documented keys, without the duplicate or
// case-folded ambiguity accepted by encoding/json when unmarshalling structs.
func strictObject(raw []byte, allowed map[string]bool) (map[string]json.RawMessage, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, false
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		item, err := d.Token()
		key, ok := item.(string)
		if err != nil || !ok || !allowed[key] {
			return nil, false
		}
		if _, duplicate := out[key]; duplicate {
			return nil, false
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, false
		}
		out[key] = value
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return nil, false
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}

func (c *CloudflareForwardRules) target(route Route) (string, error) {
	if c == nil || c.rules == nil || !c.rules.source.routeAllowed(route) || len(route.Address) > 90 {
		return "", ErrConfiguration
	}
	return ForwardTarget(c.rules.source.cfg.ID, c.rules.source.cfg.Generation, route, c.domain)
}

// Cloudflare may return a status in addition to its verified timestamp. The
// field is optional in older responses; unknown or malformed states cannot
// grant a route or be treated as a safely understood create acknowledgement.
func destinationStatus(fields map[string]json.RawMessage) (string, bool) {
	raw, present := fields["status"]
	if !present {
		return "", true
	}
	var status string
	if json.Unmarshal(raw, &status) != nil || status != "verified" && status != "unverified" {
		return "", false
	}
	return status, true
}

// VerifyDestination independently reads an account-level destination by its
// recorded provider ID. A matching email is not sufficient without the
// provider's verified timestamp. It does not create destinations or open links.
func (c *CloudflareForwardRules) VerifyDestination(ctx context.Context, id string, route Route) error {
	target, err := c.target(route)
	if err != nil || !cloudflareID.MatchString(id) {
		return ErrConfiguration
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.cloudflare.com/client/v4/accounts/"+c.account+"/email/routing/addresses/"+id, nil)
	if err != nil {
		return ErrConfiguration
	}
	req.Header.Set("Authorization", "Bearer "+c.destinationToken)
	response, err := c.rules.source.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 401, 403:
		return ErrAuthentication
	case 404:
		return ErrGone
	case 429:
		return ErrRateLimited
	case 200:
	default:
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return ErrIdentity
	}
	envelope, ok := strictObject(raw, map[string]bool{"success": true, "result": true, "errors": true, "messages": true, "result_info": true})
	if !ok {
		return ErrIdentity
	}
	var success bool
	if json.Unmarshal(envelope["success"], &success) != nil || !success {
		return ErrIdentity
	}
	fields, ok := strictObject(envelope["result"], map[string]bool{"id": true, "tag": true, "email": true, "created": true, "modified": true, "verified": true, "status": true})
	if !ok {
		return ErrIdentity
	}
	status, validStatus := destinationStatus(fields)
	if !validStatus {
		return ErrIdentity
	}
	var gotID, tag, email string
	if json.Unmarshal(fields["id"], &gotID) != nil || gotID != id || json.Unmarshal(fields["email"], &email) != nil || email != target {
		return ErrRuleConflict
	}
	if rawTag, present := fields["tag"]; present && (json.Unmarshal(rawTag, &tag) != nil || tag != "" && tag != id) {
		return ErrRuleConflict
	}
	verified, present := fields["verified"]
	if !present {
		return ErrIdentity
	}
	if bytes.Equal(bytes.TrimSpace(verified), []byte("null")) {
		if status == "verified" {
			return ErrIdentity
		}
		return ErrDestinationPending
	}
	if status == "unverified" {
		return ErrIdentity
	}
	var stamp string
	if json.Unmarshal(verified, &stamp) != nil {
		return ErrIdentity
	}
	when, err := time.Parse(time.RFC3339, stamp)
	if err != nil || when.IsZero() || when.After(time.Now().Add(5*time.Minute)) {
		return ErrIdentity
	}
	return nil
}

// CheckDestinationAbsent reads the complete account list and rejects any
// matching target. It never returns foreign addresses or their provider IDs.
// Absence is an observation, not an atomic reservation against other actors.
func (c *CloudflareForwardRules) CheckDestinationAbsent(ctx context.Context, route Route) error {
	target, err := c.target(route)
	if err != nil {
		return err
	}
	seenID, seenEmail := map[string]bool{}, map[string]bool{}
	total, count := -1, 0
	for page := 1; page <= 100; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.cloudflare.com/client/v4/accounts/"+c.account+"/email/routing/addresses?per_page=50&page="+strconv.Itoa(page), nil)
		if err != nil {
			return ErrConfiguration
		}
		req.Header.Set("Authorization", "Bearer "+c.destinationToken)
		response, err := c.rules.source.http.Do(req)
		if err != nil {
			return ErrUnavailable
		}
		status := response.StatusCode
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		response.Body.Close()
		switch status {
		case 401, 403:
			return ErrAuthentication
		case 429:
			return ErrRateLimited
		}
		if status != http.StatusOK {
			return ErrUnavailable
		}
		envelope, ok := strictObject(raw, map[string]bool{"success": true, "result": true, "errors": true, "messages": true, "result_info": true})
		var success bool
		if readErr != nil || len(raw) > 2<<20 || !ok || json.Unmarshal(envelope["success"], &success) != nil || !success {
			return ErrIdentity
		}
		observedTotal, pages, pageCount, ok := forwardPage(envelope["result_info"], page, total)
		if !ok {
			return ErrIdentity
		}
		total = observedTotal
		var rows []json.RawMessage
		if json.Unmarshal(envelope["result"], &rows) != nil || rows == nil || len(rows) > 50 || len(rows) != pageCount {
			return ErrIdentity
		}
		for _, row := range rows {
			fields, ok := strictObject(row, map[string]bool{"id": true, "tag": true, "email": true, "created": true, "modified": true, "verified": true, "status": true})
			var id, email, tag string
			_, validStatus := destinationStatus(fields)
			if !ok || !validStatus || json.Unmarshal(fields["id"], &id) != nil || !cloudflareID.MatchString(id) || json.Unmarshal(fields["email"], &email) != nil || email == "" || len(email) > 90 || seenID[id] || seenEmail[strings.ToLower(email)] {
				return ErrIdentity
			}
			if v, present := fields["tag"]; present && (json.Unmarshal(v, &tag) != nil || tag != "" && tag != id) {
				return ErrIdentity
			}
			seenID[id], seenEmail[strings.ToLower(email)] = true, true
			count++
			if strings.EqualFold(email, target) {
				return ErrRuleConflict
			}
		}
		if page >= pages {
			if count != total {
				return ErrIdentity
			}
			return nil
		}
	}
	return ErrIdentity
}

// CreateDestination only starts provider verification. The caller must record
// an issued intent and check the account for an existing address before this
// request. An unknown response is never permission to retry or adopt an
// existing account destination; administrative reconciliation is required.
// A returned ID does not prove verification or that public routing is ready.
func (c *CloudflareForwardRules) CreateDestination(ctx context.Context, route Route) (string, error) {
	if err := c.CheckDestinationAbsent(ctx, route); err != nil {
		return "", err
	}
	return c.createDestination(ctx, route)
}

// createDestination is only used after a successful account preflight. The
// durable coordinator issues its intent between the read and this mutation.
func (c *CloudflareForwardRules) createDestination(ctx context.Context, route Route) (string, error) {
	target, err := c.target(route)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(struct {
		Email string `json:"email"`
	}{Email: target})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cloudflare.com/client/v4/accounts/"+c.account+"/email/routing/addresses", bytes.NewReader(body))
	if err != nil {
		return "", ErrConfiguration
	}
	req.Header.Set("Authorization", "Bearer "+c.destinationToken)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.rules.source.http.Do(req)
	if err != nil {
		return "", ErrRuleUnknown
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 401, 403:
		return "", ErrAuthentication
	case 409:
		return "", ErrRuleConflict
	case 429:
		return "", ErrRateLimited
	}
	if response.StatusCode >= 500 {
		return "", ErrRuleUnknown
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return "", ErrRuleUnknown
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", ErrRuleRejected
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	envelope, ok := strictObject(raw, map[string]bool{"success": true, "result": true, "errors": true, "messages": true, "result_info": true})
	var success bool
	if err != nil || len(raw) > 64<<10 || !ok || json.Unmarshal(envelope["success"], &success) != nil || !success {
		return "", ErrRuleUnknown
	}
	fields, ok := strictObject(envelope["result"], map[string]bool{"id": true, "tag": true, "email": true, "created": true, "modified": true, "verified": true, "status": true})
	var id, tag, email string
	_, validStatus := destinationStatus(fields)
	if !ok || !validStatus || json.Unmarshal(fields["id"], &id) != nil || !cloudflareID.MatchString(id) || json.Unmarshal(fields["email"], &email) != nil || email != target {
		return "", ErrRuleUnknown
	}
	if value, present := fields["tag"]; present && (json.Unmarshal(value, &tag) != nil || tag != "" && tag != id) {
		return "", ErrRuleUnknown
	}
	return id, nil
}

func (c *CloudflareForwardRules) owns(r CloudflareRule, route Route) bool {
	target, err := c.target(route)
	return err == nil && cloudflareID.MatchString(r.ID) && (r.Tag == "" || r.Tag == r.ID) && r.Source == "api" && r.Priority == 0 && r.Name == c.rules.name(route.Address) && len(r.Actions) == 1 && r.Actions[0].Type == "forward" && len(r.Actions[0].Value) == 1 && r.Actions[0].Value[0] == target && len(r.Matchers) == 1 && r.Matchers[0].Type == "literal" && r.Matchers[0].Field == "to" && r.Matchers[0].Value == route.Address
}

func parseForwardRule(raw []byte) (CloudflareRule, bool) {
	var result CloudflareRule
	fields, ok := strictObject(raw, map[string]bool{"id": true, "tag": true, "name": true, "enabled": true, "actions": true, "matchers": true, "priority": true, "source": true, "zone": true})
	if !ok {
		return result, false
	}
	for _, name := range []string{"actions", "matchers"} {
		var items []json.RawMessage
		if json.Unmarshal(fields[name], &items) != nil || len(items) != 1 {
			return result, false
		}
		allowed := map[string]bool{"type": true, "value": true}
		if name == "matchers" {
			allowed["field"] = true
		}
		if _, valid := strictObject(items[0], allowed); !valid {
			return result, false
		}
	}
	if json.Unmarshal(raw, &result) != nil || !normalizeRule(&result) {
		return CloudflareRule{}, false
	}
	return result, true
}

// request rejects ambiguous provider envelopes before interpreting their
// results. An ambiguous write response cannot prove whether a mutation took
// effect, so the caller must retain its persisted intent for reconciliation.
type forwardEnvelope struct {
	Result, Info json.RawMessage
}

func forwardInt(raw json.RawMessage, out *int) bool {
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && json.Unmarshal(raw, out) == nil
}

func forwardPage(raw json.RawMessage, page, previous int) (total, pages, count int, ok bool) {
	info, valid := strictObject(raw, map[string]bool{"page": true, "per_page": true, "count": true, "total_count": true, "total_pages": true})
	var actual, perPage int
	if !valid || !forwardInt(info["page"], &actual) || !forwardInt(info["per_page"], &perPage) || !forwardInt(info["count"], &count) || !forwardInt(info["total_count"], &total) || actual != page || perPage != 50 || total < 0 || total > 5000 || previous >= 0 && previous != total {
		return 0, 0, 0, false
	}
	pages = (total + 49) / 50
	if pages == 0 {
		pages = 1
	}
	if value, present := info["total_pages"]; present {
		var n int
		if !forwardInt(value, &n) || n != pages && !(total == 0 && n == 0) {
			return 0, 0, 0, false
		}
	}
	return total, pages, count, true
}

func (c *CloudflareForwardRules) request(ctx context.Context, method, path string, input any) (forwardEnvelope, error) {
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil || len(body) > 8192 {
			return forwardEnvelope{}, ErrConfiguration
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.cloudflare.com/client/v4/zones/"+c.rules.zone+"/email/routing/rules"+path, bytes.NewReader(body))
	if err != nil {
		return forwardEnvelope{}, ErrConfiguration
	}
	req.Header.Set("Authorization", "Bearer "+c.rules.token)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.rules.source.http.Do(req)
	if err != nil {
		if method != http.MethodGet {
			return forwardEnvelope{}, ErrRuleUnknown
		}
		return forwardEnvelope{}, ErrUnavailable
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 401, 403:
		return forwardEnvelope{}, ErrAuthentication
	case 404:
		return forwardEnvelope{}, ErrGone
	case 409:
		return forwardEnvelope{}, ErrRuleConflict
	case 429:
		return forwardEnvelope{}, ErrRateLimited
	}
	if response.StatusCode >= 500 {
		if method != http.MethodGet {
			return forwardEnvelope{}, ErrRuleUnknown
		}
		return forwardEnvelope{}, ErrUnavailable
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return forwardEnvelope{}, ErrIdentity
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return forwardEnvelope{}, ErrRuleRejected
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	fields, ok := strictObject(raw, map[string]bool{"success": true, "result": true, "errors": true, "messages": true, "result_info": true})
	var success bool
	if err != nil || len(raw) > 2<<20 || !ok || json.Unmarshal(fields["success"], &success) != nil || !success || len(fields["result"]) == 0 || bytes.Equal(bytes.TrimSpace(fields["result"]), []byte("null")) {
		if method != http.MethodGet {
			return forwardEnvelope{}, ErrRuleUnknown
		}
		return forwardEnvelope{}, ErrIdentity
	}
	return forwardEnvelope{Result: fields["result"], Info: fields["result_info"]}, nil
}

// Inspect reads every page before any public write, rejecting competing rules
// and entries using a requested rule name that no longer match the claim.
// Returned matches are observations only: callers must prove ownership from
// their durable ledger before using or deleting any existing rule.
func (c *CloudflareForwardRules) Inspect(ctx context.Context, routes []Route) (map[string]CloudflareRule, error) {
	if c == nil || c.rules == nil || len(routes) > 5000 {
		return nil, ErrConfiguration
	}
	wanted := map[string]Route{}
	for _, route := range routes {
		if _, err := c.target(route); err != nil {
			return nil, err
		}
		if _, duplicate := wanted[route.Address]; duplicate {
			return nil, ErrConfiguration
		}
		wanted[route.Address] = route
	}
	found, seen := map[string]CloudflareRule{}, map[string]bool{}
	wantedNames := map[string]bool{}
	for address := range wanted {
		wantedNames[c.rules.name(address)] = true
	}
	total := -1
	count := 0
	for page := 1; page <= 100; page++ {
		response, err := c.request(ctx, http.MethodGet, "?per_page=50&page="+strconv.Itoa(page), nil)
		if err != nil {
			return nil, err
		}
		observedTotal, pages, pageCount, ok := forwardPage(response.Info, page, total)
		if !ok {
			return nil, ErrIdentity
		}
		total = observedTotal
		var rows []json.RawMessage
		if json.Unmarshal(response.Result, &rows) != nil || rows == nil || len(rows) > 50 || pageCount != len(rows) {
			return nil, ErrIdentity
		}
		for _, raw := range rows {
			rule, valid := parseForwardRule(raw)
			if !valid || seen[rule.ID] {
				return nil, ErrIdentity
			}
			seen[rule.ID] = true
			count++
			address := rule.Matchers[0].Value
			route, expected := wanted[strings.ToLower(address)]
			if wantedNames[rule.Name] || expected && rule.Matchers[0].Type == "literal" && rule.Matchers[0].Field == "to" {
				if !expected || !c.owns(rule, route) || found[route.Address].ID != "" {
					return nil, ErrRuleConflict
				}
				found[route.Address] = rule
			}
		}
		if page >= pages {
			if count != total {
				return nil, ErrIdentity
			}
			return found, nil
		}
	}
	return nil, ErrIdentity
}

// Create is intentionally not an orchestration or readiness API. The caller
// must record intent before this method, and re-read the returned rule after it.
func (c *CloudflareForwardRules) Create(ctx context.Context, route Route, destinationID string) (CloudflareRule, error) {
	if err := c.VerifyDestination(ctx, destinationID, route); err != nil {
		return CloudflareRule{}, err
	}
	return c.createRule(ctx, route)
}

// createRule is used only after destination verification and durable issue.
func (c *CloudflareForwardRules) createRule(ctx context.Context, route Route) (CloudflareRule, error) {
	target, err := c.target(route)
	if err != nil {
		return CloudflareRule{}, err
	}
	input := CloudflareRule{Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
	result, err := c.request(ctx, http.MethodPost, "", input)
	if err != nil {
		return CloudflareRule{}, err
	}
	out, valid := parseForwardRule(result.Result)
	if !valid || !c.owns(out, route) || !out.Enabled {
		return CloudflareRule{}, ErrRuleUnknown
	}
	return out, nil
}

// Read requires the complete expected binding, not merely the rule ID.
func (c *CloudflareForwardRules) Read(ctx context.Context, id string, route Route) (CloudflareRule, error) {
	if _, err := c.target(route); err != nil {
		return CloudflareRule{}, err
	}
	if !cloudflareID.MatchString(id) {
		return CloudflareRule{}, ErrConfiguration
	}
	result, err := c.request(ctx, http.MethodGet, "/"+id, nil)
	if err != nil {
		return CloudflareRule{}, err
	}
	actual, valid := parseForwardRule(result.Result)
	if !valid || actual.ID != id {
		return CloudflareRule{}, ErrIdentity
	}
	if !c.owns(actual, route) {
		return CloudflareRule{}, ErrRuleConflict
	}
	return actual, nil
}

// Delete refuses foreign or modified rules after a separate read. A provider
// deletion response can still be lost; callers must keep their issued intent.
func (c *CloudflareForwardRules) Delete(ctx context.Context, expected CloudflareRule, route Route) error {
	if c == nil || !c.owns(expected, route) {
		return ErrRuleConflict
	}
	actual, err := c.Read(ctx, expected.ID, route)
	if err != nil {
		return err
	}
	actual.Tag, expected.Tag = "", ""
	if !reflect.DeepEqual(actual, expected) {
		return ErrRuleConflict
	}
	result, err := c.request(ctx, http.MethodDelete, "/"+actual.ID, nil)
	if err != nil {
		return err
	}
	// Cloudflare documents a complete deleted rule in the response, while
	// some API versions return only its ID. Accept either strict shape, but
	// never treat a different full rule as proof of this deletion.
	fields, short := strictObject(result.Result, map[string]bool{"id": true, "tag": true})
	if short {
		var id, tag string
		if (len(fields["id"]) != 0 && json.Unmarshal(fields["id"], &id) != nil) || (len(fields["tag"]) != 0 && json.Unmarshal(fields["tag"], &tag) != nil) || id != actual.ID && !(id == "" && tag == actual.ID) || tag != "" && tag != actual.ID {
			return ErrRuleUnknown
		}
		return nil
	}
	deleted, valid := parseForwardRule(result.Result)
	deleted.Tag = ""
	if !valid || !reflect.DeepEqual(deleted, actual) {
		return ErrRuleUnknown
	}
	return nil
}
