package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type SourceSpec struct {
	ID                string                 `json:"id"`
	Generation        string                 `json:"generation"`
	Provider          string                 `json:"provider"`
	Mode              string                 `json:"mode"`
	Origin            string                 `json:"origin"`
	Domains           []string               `json:"domains"`
	CapacityBytes     int64                  `json:"capacity_bytes"`
	DeliveredDays     int                    `json:"delivered_days"`
	Notifications     bool                   `json:"notifications"`
	IPv4Only          bool                   `json:"ipv4_only"`
	Cloudflare        *CloudflareRuleSpec    `json:"cloudflare,omitempty"`
	CloudflareForward *CloudflareForwardSpec `json:"cloudflare_forward,omitempty"`
}
type CloudflareRuleSpec struct {
	ZoneID string `json:"zone_id"`
	Worker string `json:"worker"`
}
type CloudflareForwardSpec struct {
	AccountID string `json:"account_id"`
	ZoneID    string `json:"zone_id"`
	Domain    string `json:"domain"`
}
type SourceCredentials struct {
	Reader       string `json:"reader"`
	Control      string `json:"control,omitempty"`
	Notification string `json:"notification,omitempty"`
	Proxy        string `json:"proxy,omitempty"`
	Routing      string `json:"routing,omitempty"`
	Destination  string `json:"destination,omitempty"`
}
type RuntimeSource struct {
	SourceSpec
	Credentials SourceCredentials `json:"credentials"`
}

// ValidateCapacity accounts for active and draining sources together. The
// allocation is a reservation, not observed usage: an offline old source must
// not accidentally make room for another full-size source. This does not alter
// any provider limit or delete an undelivered cloud copy.
func ValidateCapacity(sources []RuntimeSource, total int64) error {
	if total < 0 || total > 9007199254740991 || len(sources) > 0 && total == 0 {
		return ErrConfiguration
	}
	remaining := total
	for _, source := range sources {
		if source.CapacityBytes < 1 || source.CapacityBytes > remaining {
			return ErrConfiguration
		}
		remaining -= source.CapacityBytes
	}
	return nil
}

func (s RuntimeSource) ClientConfig(ca []byte) SourceConfig {
	return SourceConfig{ID: s.ID, Generation: s.Generation, Origin: s.Origin, Domains: s.Domains, Token: s.Credentials.Reader, ControlToken: s.Credentials.Control, Proxy: s.Credentials.Proxy, IPv4Only: s.IPv4Only, AdditionalCA: ca}
}
func ValidateSources(sources []RuntimeSource, allowedDomains []string) error {
	if len(sources) > 8 {
		return ErrConfiguration
	}
	ids, origins, secrets, active := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	allowed := map[string]bool{}
	for _, domain := range allowedDomains {
		allowed[domain] = true
	}
	for _, s := range sources {
		if ids[s.ID] || origins[s.Origin] || s.Provider != "worker-r2" && s.Provider != "cloud-relay" || s.Mode != "active" && s.Mode != "draining" || s.CapacityBytes < 1 || s.CapacityBytes > 9007199254740991 || s.DeliveredDays < 1 || s.DeliveredDays > 3650 || s.Credentials.Control == "" || s.Notifications && !secretPattern.MatchString(s.Credentials.Notification) || !s.Notifications && s.Credentials.Notification != "" {
			return ErrConfiguration
		}
		if s.Cloudflare != nil && s.CloudflareForward != nil || (s.Cloudflare == nil && s.CloudflareForward == nil) != (s.Credentials.Routing == "") || (s.CloudflareForward == nil) != (s.Credentials.Destination == "") || s.Cloudflare != nil && (s.Provider != "worker-r2" || !cloudflareID.MatchString(s.Cloudflare.ZoneID) || !workerName.MatchString(s.Cloudflare.Worker)) || s.CloudflareForward != nil && (s.Provider != "cloud-relay" || !cloudflareID.MatchString(s.CloudflareForward.AccountID) || !cloudflareID.MatchString(s.CloudflareForward.ZoneID) || !cloudflareToken.MatchString(s.Credentials.Destination)) || s.Credentials.Routing != "" && !cloudflareToken.MatchString(s.Credentials.Routing) {
			return ErrConfiguration
		}
		ids[s.ID], origins[s.Origin] = true, true
		for _, domain := range s.Domains {
			if len(allowed) > 0 && !allowed[domain] || s.Mode == "active" && active[domain] {
				return ErrConfiguration
			}
			if s.Mode == "active" {
				active[domain] = true
			}
		}
		for _, secret := range []string{s.Credentials.Reader, s.Credentials.Control, s.Credentials.Notification} {
			if secret == "" {
				continue
			}
			if !secretPattern.MatchString(secret) || secrets[secret] {
				return ErrConfiguration
			}
			secrets[secret] = true
		}
		for _, secret := range []string{s.Credentials.Routing, s.Credentials.Destination} {
			if secret == "" {
				continue
			}
			if secrets[secret] {
				return ErrConfiguration
			}
			secrets[secret] = true
		}
		client, err := NewSourceClient(s.ClientConfig(nil))
		if err != nil {
			return ErrConfiguration
		}
		if s.CloudflareForward != nil {
			_, err = NewCloudflareForwardRules(client, s.CloudflareForward.AccountID, s.CloudflareForward.ZoneID, s.CloudflareForward.Domain, s.Credentials.Destination, s.Credentials.Routing)
			if err != nil {
				client.Close()
				return ErrConfiguration
			}
		}
		client.Close()
	}
	return nil
}
func decodeConfiguration(raw string, target any) error {
	if len(raw) > 65536 {
		return ErrConfiguration
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(target) != nil || d.Decode(&extra) != io.EOF {
		return ErrConfiguration
	}
	return nil
}

// sourceDescriptionFields and credentialFields describe the exact stored wire
// format. encoding/json otherwise accepts repeated keys and case-folded field
// names, allowing one persisted configuration to have ambiguous identities.
var sourceDescriptionFields = map[string]bool{
	"id": true, "generation": true, "provider": true, "mode": true, "origin": true,
	"domains": true, "capacity_bytes": true, "delivered_days": true,
	"notifications": true, "ipv4_only": true, "cloudflare": true, "cloudflare_forward": true,
}
var credentialFields = map[string]bool{"reader": true, "control": true, "notification": true, "proxy": true, "routing": true, "destination": true}

func strictSourceDescriptions(raw string) bool {
	if len(raw) > 65536 {
		return false
	}
	var items []json.RawMessage
	if json.Unmarshal([]byte(raw), &items) != nil || items == nil || len(items) > 8 {
		return false
	}
	for _, item := range items {
		fields, ok := strictObject(item, sourceDescriptionFields)
		if !ok {
			return false
		}
		for name, value := range fields {
			if name != "cloudflare" && name != "cloudflare_forward" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return false
			}
		}
		for name, allowed := range map[string]map[string]bool{
			"cloudflare":         {"zone_id": true, "worker": true},
			"cloudflare_forward": {"account_id": true, "zone_id": true, "domain": true},
		} {
			value, present := fields[name]
			if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				continue
			}
			if _, valid := strictObject(value, allowed); !valid {
				return false
			}
		}
	}
	return true
}

func strictSourceCredentials(raw string) bool {
	if len(raw) > 65536 {
		return false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		id, ok := key.(string)
		if err != nil || !ok || !idPattern.MatchString(id) || seen[id] || len(seen) >= 8 {
			return false
		}
		seen[id] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
		fields, ok := strictObject(value, credentialFields)
		if !ok {
			return false
		}
		for _, field := range fields {
			if bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
				return false
			}
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	return err == io.EOF
}

// Public descriptions and sensitive credentials use existing separate ANAS
// configuration/Secret inputs. Only RuntimeSource reaches the private Go file.
func ParseSources(descriptions, credentials string, allowedDomains []string) ([]RuntimeSource, error) {
	var specs []SourceSpec
	values := map[string]SourceCredentials{}
	if strings.TrimSpace(descriptions) != "" {
		if !strictSourceDescriptions(descriptions) || decodeConfiguration(descriptions, &specs) != nil || specs == nil {
			return nil, ErrConfiguration
		}
	}
	if strings.TrimSpace(credentials) != "" {
		if !strictSourceCredentials(credentials) || decodeConfiguration(credentials, &values) != nil || values == nil {
			return nil, ErrConfiguration
		}
	}
	if len(values) != len(specs) {
		return nil, ErrConfiguration
	}
	result := make([]RuntimeSource, 0, len(specs))
	for _, spec := range specs {
		value, ok := values[spec.ID]
		if !ok {
			return nil, ErrConfiguration
		}
		result = append(result, RuntimeSource{SourceSpec: spec, Credentials: value})
	}
	if err := ValidateSources(result, allowedDomains); err != nil {
		return nil, err
	}
	return result, nil
}
