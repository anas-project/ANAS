package computeingressruntime

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// ObservePinnedIncusVersion is for an explicit installation plan, never the
// periodic observer. It learns only a version from an ALREADY pinned mTLS
// identity. The plan must freeze it and recheck it before configuration writes.
// This is neither TOFU for certificates nor a compatibility/health assertion.
func ObservePinnedIncusVersion(ctx context.Context, config IncusObserverConfig) (string, error) {
	if ctx == nil || config.ServerVersion != "" {
		return "", fmt.Errorf("invalid Incus version observation")
	}
	config.ServerVersion = "installation-plan"
	r, err := NewIncusFactReader(config)
	if err != nil {
		return "", err
	}
	defer r.CloseIdleConnections()
	var selected string
	for range 2 {
		var server struct {
			Auth           string `json:"auth"`
			APIVersion     string `json:"api_version"`
			AuthUserName   string `json:"auth_user_name"`
			AuthUserMethod string `json:"auth_user_method"`
			Environment    struct {
				Server      string `json:"server"`
				Version     string `json:"server_version"`
				Clustered   *bool  `json:"server_clustered"`
				Fingerprint string `json:"certificate_fingerprint"`
			} `json:"environment"`
		}
		if err := r.get(ctx, "/1.0?project="+url.QueryEscape(config.Authorizations[0].Project), &server); err != nil {
			return "", err
		}
		e := server.Environment
		if server.Auth != "trusted" || server.APIVersion != "1.0" || server.AuthUserMethod != "tls" || server.AuthUserName != r.clientFingerprint ||
			e.Server != "incus" || e.Clustered == nil || *e.Clustered || e.Fingerprint != r.serverFingerprint || len(e.Version) == 0 || len(e.Version) > 64 ||
			strings.ContainsAny(e.Version, " \t\r\n\x00") || (selected != "" && selected != e.Version) {
			return "", fmt.Errorf("Incus version observation is inconsistent")
		}
		selected = e.Version
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return selected, nil
}
