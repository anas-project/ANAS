package incusprovision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"github.com/anas-project/ANAS/internal/computeclient"
)

// The original host lock remains held. This session additionally holds the
// registered workspace lock, so Core cannot restart consumers during removal.
// Closing a session never clears a receipt or reinstates a revoked credential.
type forwardingRetirementSession struct {
	stamp string
	check func(context.Context) error
	close func() error
}

func forwardingRetirementReady(r ForwardingPermissionRecord) bool {
	return r.Status == "disabled" && r.Kernel.Validate() == nil && r.Kernel.PendingStep == "" &&
		r.Kernel.NewConnectionsClosed && r.Kernel.ConnectionsRevoked &&
		(r.Kernel.Phase == "closed" || r.Kernel.Phase == "released")
}

// No prefix filtering: stopped, unmanaged and differently named instances
// still prevent retirement. Certificate and operation inventories are complete
// bounded API responses; a missing or null response never means empty.
func observeForwardingRetirement(ctx context.Context, c *incusUnixClient, grant ForwardingLeaseGrant, manager string) (string, error) {
	if ctx == nil || ctx.Err() != nil || c == nil || grant.Validate() != nil || !digestPattern.MatchString(manager) ||
		manager == grant.Lease.CredentialFingerprint || grant.Network.BridgeName != computeclient.NetworkName(grant.Lease.Project) {
		return "", ErrBlocked
	}
	read := func(path string, out any, keys ...string) error {
		var body json.RawMessage
		if err := c.do(ctx, http.MethodGet, path, nil, &body); err != nil {
			return ErrBlocked
		}
		if len(keys) != 0 {
			return decodeNFTObject(body, out, keys...)
		}
		return json.Unmarshal(body, out)
	}
	var project struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config"`
	}
	if read("/1.0/projects/"+url.PathEscape(grant.Lease.Project), &project, "name", "config", "description", "used_by") != nil ||
		project.Name != grant.Lease.Project || project.Config["user.anas.consumer"] != grant.Lease.Consumer ||
		project.Config["user.anas.sandbox"] != grant.Lease.Project || project.Config["restricted"] != "true" ||
		project.Config["features.networks"] != "false" || project.Config["restricted.networks.access"] != grant.Network.BridgeName {
		return "", ErrBlocked
	}
	var certificates []json.RawMessage
	if read("/1.0/certificates?recursion=1", &certificates) != nil || certificates == nil || len(certificates) > 1024 {
		return "", ErrBlocked
	}
	seen, managerPresent := map[string]bool{}, false
	identities := []string{}
	for _, body := range certificates {
		var cert struct {
			Fingerprint string   `json:"fingerprint"`
			Type        string   `json:"type"`
			Restricted  *bool    `json:"restricted"`
			Projects    []string `json:"projects"`
		}
		if decodeNFTObject(body, &cert, "fingerprint", "type", "restricted", "projects", "name", "certificate", "description") != nil ||
			!digestPattern.MatchString(cert.Fingerprint) || cert.Type != "client" || cert.Restricted == nil ||
			seen[cert.Fingerprint] || cert.Fingerprint == grant.Lease.CredentialFingerprint {
			return "", ErrBlocked
		}
		seen[cert.Fingerprint] = true
		if cert.Fingerprint == manager {
			if *cert.Restricted || len(cert.Projects) != 0 {
				return "", ErrBlocked
			}
			managerPresent = true
		} else if !*cert.Restricted || cert.Projects == nil || slices.Contains(cert.Projects, grant.Lease.Project) {
			// A replacement restricted credential or another global identity
			// could recreate the lease while its deny baseline is removed.
			return "", ErrBlocked
		}
		identities = append(identities, stableDigest(cert))
	}
	if !managerPresent {
		return "", ErrBlocked
	}
	slices.Sort(identities)
	var instances []json.RawMessage
	query := "?project=" + url.QueryEscape(grant.Lease.Project)
	if read("/1.0/instances"+query+"&recursion=1", &instances) != nil || instances == nil || len(instances) != 0 {
		return "", ErrBlocked
	}
	var operations map[string][]json.RawMessage
	if read("/1.0/operations"+query+"&recursion=1", &operations) != nil || operations == nil || len(operations) > 8 {
		return "", ErrBlocked
	}
	for status, entries := range operations {
		if !slices.Contains([]string{"pending", "running", "success", "failure", "cancelling", "cancelled"}, status) || entries == nil || len(entries) != 0 {
			return "", ErrBlocked
		}
	}
	var network struct {
		Name    string            `json:"name"`
		Type    string            `json:"type"`
		Managed *bool             `json:"managed"`
		Status  string            `json:"status"`
		Config  map[string]string `json:"config"`
	}
	if read("/1.0/networks/"+url.PathEscape(grant.Network.BridgeName)+"?project=default", &network,
		"name", "type", "managed", "status", "config", "description", "used_by", "locations", "project") != nil ||
		network.Name != grant.Network.BridgeName || network.Type != "bridge" || network.Managed == nil || !*network.Managed || network.Status != "Created" ||
		network.Config["user.anas.consumer"] != grant.Lease.Consumer || network.Config["user.anas.sandbox"] != grant.Lease.Project ||
		network.Config["bridge.external_interfaces"] != "" || network.Config["ipv4.address"] != grant.Network.BridgeCIDR {
		return "", ErrBlocked
	}
	return stableDigest([]any{project, identities, instances, operations, network}), ctx.Err()
}
