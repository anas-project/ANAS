package computeingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
)

const MaxRequestBytes = 4096

// A request names the instance that serves it, that instance's lease address
// and the guest port. Its directory binds it to one lease; the frozen
// authorization of that lease supplies the domain, authentication, middleware
// and entrypoint. A file present is a publication; removing it revokes it.
//
// Label is the named-mode label the consumer chose, or the random-mode label
// the consumer derived from its naming key (32 lowercase hex characters); the
// mediator holds no key and only checks its shape.
type Request struct {
	Instance string `json:"instance"`
	Address  string `json:"address"`
	Port     uint16 `json:"port"`
	Label    string `json:"label,omitempty"`
}

func ParseRequest(body []byte) (Request, error) {
	var r Request
	if len(body) > MaxRequestBytes {
		return r, fmt.Errorf("HTTP publication request exceeds 4 KiB")
	}
	if err := decodeStrict(body, &r); err != nil {
		return r, err
	}
	return r, r.Validate()
}

func (r Request) Validate() error {
	if !instanceName.MatchString(r.Instance) || r.Port == 0 {
		return fmt.Errorf("invalid HTTP request instance or port")
	}
	addr, err := netip.ParseAddr(r.Address)
	if err != nil || !addr.Is4() || addr.String() != r.Address {
		return fmt.Errorf("HTTP request address must be the instance's IPv4 address")
	}
	if r.Label != "" && !dnsLabel.MatchString(r.Label) {
		return fmt.Errorf("invalid HTTP request label")
	}
	return nil
}

// encoding/json otherwise accepts duplicate keys, case aliases and null scalar
// values. Reject all of those before decoding a small, closed schema. Errors
// deliberately do not echo the consumer-controlled payload.
func decodeStrict(body []byte, out any) error {
	if len(body) > 65536 {
		return fmt.Errorf("HTTP declaration exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	if err := jsonValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON input")
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("HTTP input does not match its schema")
	}
	return nil
}

func jsonValue(d *json.Decoder, depth int) error {
	if depth > 8 {
		return fmt.Errorf("HTTP JSON input is too deeply nested")
	}
	t, err := d.Token()
	if err != nil || t == nil {
		return fmt.Errorf("invalid or null HTTP JSON value")
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			key, ok := t.(string)
			if err != nil || !ok || key == "" || seen[key] {
				return fmt.Errorf("invalid or duplicate HTTP JSON field")
			}
			for _, c := range key {
				if (c < 'a' || c > 'z') && c != '_' {
					return fmt.Errorf("HTTP JSON fields must use their exact lowercase spelling")
				}
			}
			seen[key] = true
			if err := jsonValue(d, depth+1); err != nil {
				return err
			}
		}
		if t, err := d.Token(); err != nil || t != json.Delim('}') {
			return fmt.Errorf("invalid HTTP JSON object")
		}
	case '[':
		for d.More() {
			if err := jsonValue(d, depth+1); err != nil {
				return err
			}
		}
		if t, err := d.Token(); err != nil || t != json.Delim(']') {
			return fmt.Errorf("invalid HTTP JSON array")
		}
	default:
		return fmt.Errorf("invalid HTTP JSON delimiter")
	}
	return nil
}
