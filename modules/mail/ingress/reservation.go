package ingress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/anas-project/ANAS/modules/mail/store"
)

// Reservation excludes credentials and mutable domain/transport/retention settings.
// A different origin, provider or public routing target is a new
// source instance, rather than silently reusing the old instance's identity.
func (s SourceSpec) Reservation() store.IngressReservation {
	raw, _ := json.Marshal(struct {
		Provider, Origin  string
		Cloudflare        *CloudflareRuleSpec
		CloudflareForward *CloudflareForwardSpec `json:",omitempty"`
	}{s.Provider, s.Origin, s.Cloudflare, s.CloudflareForward})
	hash := sha256.Sum256(raw)
	return store.IngressReservation{SourceID: s.ID, Generation: s.Generation, IdentityFingerprint: hex.EncodeToString(hash[:]), CapacityBytes: s.CapacityBytes}
}
