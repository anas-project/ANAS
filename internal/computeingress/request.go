package computeingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const MaxRequestBytes = 4096

// A request never supplies lease identity, target address, host, authentication,
// middleware or entrypoint. Its directory binding supplies the authorization.
type Request struct {
	Action     string `json:"action"`
	InstanceID string `json:"instance_id"`
	WorkloadID string `json:"workload_id"`
	GuestPort  uint16 `json:"guest_port"`
	Label      string `json:"label,omitempty"`
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
	if r.Action != "publish" && r.Action != "revoke" {
		return fmt.Errorf("HTTP request action must be publish or revoke")
	}
	if !instanceName.MatchString(r.InstanceID) || !workloadName.MatchString(r.WorkloadID) || r.GuestPort == 0 {
		return fmt.Errorf("invalid HTTP request instance, workload or port")
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
