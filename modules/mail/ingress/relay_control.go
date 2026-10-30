package ingress

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// RelayControlStatus is returned only to the source control credential. The
// notification proof binds a fresh challenge to the origin and source identity;
// it is not a notification receipt or evidence of public SMTP reachability.
type RelayControlStatus struct {
	Schema             string   `json:"schema"`
	SourceID           string   `json:"source_id"`
	Generation         string   `json:"generation"`
	Domains            []string `json:"domains"`
	NotificationOrigin string   `json:"notification_origin"`
	NotificationProof  string   `json:"notification_proof"`
}

func RelayNotificationProof(secret []byte, source, generation, challenge, origin string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("anas.mail-relay-control/v1\x00" + source + "\x00" + generation + "\x00" + challenge + "\x00" + origin))
	return hex.EncodeToString(mac.Sum(nil))
}

// CheckRelayControl verifies the restricted control token, configured domains
// and, when configured, possession of the expected notification secret. It is
// read-only and cannot prove that notifications actually reach the NAS.
func (c *SourceClient) CheckRelayControl(ctx context.Context, spec SourceSpec, notificationOrigin, notificationSecret string) error {
	if c == nil || spec.Provider != "cloud-relay" || spec.ID != c.cfg.ID || spec.Generation != c.cfg.Generation || spec.Origin != c.cfg.Origin || spec.Mode != "active" || c.cfg.ControlToken == "" || (notificationOrigin == "") != (notificationSecret == "") || spec.Notifications != (notificationSecret != "") {
		return ErrConfiguration
	}
	var secret []byte
	if notificationSecret != "" {
		if !secretPattern.MatchString(notificationSecret) {
			return ErrConfiguration
		}
		secret, _ = hex.DecodeString(notificationSecret)
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrUnavailable
	}
	challenge := hex.EncodeToString(random[:])
	response, err := c.authenticatedRequest(ctx, "GET", "/v1/control-status?challenge="+challenge, nil, c.cfg.ControlToken)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var status RelayControlStatus
	if err = sourceBoundedJSON(response.Body, &status, 16384); err != nil {
		return err
	}
	if status.Schema != "anas.mail-relay-control/v1" || status.SourceID != spec.ID || status.Generation != spec.Generation || status.NotificationOrigin != notificationOrigin || len(status.Domains) != len(spec.Domains) {
		return ErrIdentity
	}
	expected, actual := append([]string(nil), spec.Domains...), append([]string(nil), status.Domains...)
	sort.Strings(expected)
	sort.Strings(actual)
	for i := range expected {
		if expected[i] != actual[i] {
			return ErrIdentity
		}
	}
	proof := ""
	if secret != nil {
		proof = RelayNotificationProof(secret, spec.ID, spec.Generation, challenge, notificationOrigin)
	}
	if !hmac.Equal([]byte(status.NotificationProof), []byte(proof)) {
		return ErrIdentity
	}
	return nil
}
