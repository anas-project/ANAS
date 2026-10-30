package main

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/modules/mail/cloudflare/installer"
	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

// vpsInstallDirectories creates both journals only for the first publish. A
// missing child after that is unknown and cannot generate replacement secrets.
func vpsInstallDirectories(path string, first bool) (string, string, error) {
	dir, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", "", installer.ErrJournal
	}
	defer unix.Close(dir)
	scan, err := unix.Openat(dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", "", installer.ErrUnknown
	}
	f := os.NewFile(uintptr(scan), "installation operation")
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil || len(names) > 1 || len(names) == 1 && names[0] != "vps-ssh" {
		return "", "", installer.ErrConflict
	}
	fresh := len(names) == 0
	if fresh {
		if !first {
			return "", "", installer.ErrUnknown
		}
		if unix.Mkdirat(dir, "vps-ssh", 0700) != nil || unix.Fsync(dir) != nil {
			return "", "", installer.ErrUnknown
		}
	}
	root, err := unix.Openat(dir, "vps-ssh", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", "", installer.ErrJournal
	}
	defer unix.Close(root)
	if !privateMailDir(root) {
		return "", "", installer.ErrConflict
	}
	if fresh {
		for _, name := range []string{"provision", "relay"} {
			if unix.Mkdirat(root, name, 0700) != nil {
				return "", "", installer.ErrUnknown
			}
		}
		if unix.Fsync(root) != nil {
			return "", "", installer.ErrUnknown
		}
	}
	for _, name := range []string{"provision", "relay"} {
		child, e := unix.Openat(root, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			return "", "", installer.ErrUnknown
		}
		ok := privateMailDir(child)
		unix.Close(child)
		if !ok {
			return "", "", installer.ErrConflict
		}
	}
	scan, err = unix.Openat(root, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", "", installer.ErrUnknown
	}
	f = os.NewFile(uintptr(scan), "VPS installation operation")
	names, err = f.Readdirnames(-1)
	f.Close()
	if err != nil || len(names) != 2 {
		return "", "", installer.ErrUnknown
	}
	for _, name := range names {
		if name != "provision" && name != "relay" {
			return "", "", installer.ErrConflict
		}
	}
	base := filepath.Join(path, "vps-ssh")
	return filepath.Join(base, "provision"), filepath.Join(base, "relay"), nil
}
func privateMailDir(fd int) bool {
	var st unix.Stat_t
	return unix.Fstat(fd, &st) == nil && st.Uid == uint32(os.Geteuid()) && st.Mode&0077 == 0 && st.Mode&unix.S_IFMT == unix.S_IFDIR
}

func handleMailVPSInstall(ctx context.Context, call httpapi.ServiceMailInstallCall, dir string, observer mailInstallAudit, service application.ConfigService, validator string, domains []string, current []ingress.RuntimeSource) (httpapi.ServiceMailInstallResult, error) {
	input := call.Request
	origin, err := url.Parse(input.SourceOrigin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.ForceQuery {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
	}
	port := origin.Port()
	if port == "" {
		port = "443"
	}
	template := cloudrelay.RuntimeConfig{Schema: "anas.mail-vps-runtime/v1", Directory: "/var/lib/anas-mail-relay/data/source", Domains: append([]string(nil), input.Domains...), CapacityBytes: input.CapacityBytes, DeliveredDays: input.DeliveredDays, Listen: net.JoinHostPort("0.0.0.0", port), CertificateHost: origin.Hostname(), TLSCertificate: input.TLSCertificate, TLSPrivateKey: input.TLSPrivateKey, NotificationOrigin: input.NotificationOrigin, SMTPListen: "0.0.0.0:25", SMTPHostname: input.SMTPHostname, ForwardDomain: input.ForwardDomain}
	request := installer.RelayProvisionInput{OperationID: input.OperationID, Origin: input.SourceOrigin, AllowedDomains: domains, IPv4Only: input.IPv4Only, Template: template, SSH: installer.RelaySSHConfig{Host: input.SSHHost, Port: input.SSHPort, User: input.SSHUser, HostKey: []byte(input.SSHHostKey), PrivateKey: []byte(input.SSHPrivateKey)}}
	if input.ForwardDomain != "" {
		request.CloudflareForward = &ingress.CloudflareForwardSpec{AccountID: input.AccountID, ZoneID: input.ZoneID, Domain: input.ForwardDomain}
		request.Routing, request.Destination = input.RoutingToken, input.DestinationToken
	}
	defer clear(request.SSH.PrivateKey)
	provisionPath, relayPath, err := vpsInstallDirectories(dir, input.Action == "publish")
	if err != nil {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
	}
	if input.Action != "publish" {
		for _, p := range []string{filepath.Join(provisionPath, "provision.lock"), filepath.Join(relayPath, "relay.lock")} {
			if _, e := os.Lstat(p); e != nil {
				return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
			}
		}
	}
	provision, err := installer.OpenRelayProvisionJournal(provisionPath)
	if err != nil {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
	}
	defer provision.Close()
	relay, err := installer.OpenRelayJournal(relayPath)
	if err != nil {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
	}
	defer relay.Close()
	if observer.AuthorizeRelayProvision(ctx) != nil {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
	}
	prepared, err := provision.Prepare(request)
	if err != nil {
		return httpapi.ServiceMailInstallResult{}, mailVPSInstallError(err)
	}
	var source ingress.SourceSpec
	switch input.Action {
	case "publish", "activate":
		plan := installer.RelayInstallRequest{Runtime: prepared.Source, Existing: current, DrainIDs: input.DrainIDs, TotalCapacity: input.TotalCapacityBytes, OperationID: input.OperationID}
		if err = relay.BindRemotePlan(ctx, observer, plan, current); err != nil {
			return httpapi.ServiceMailInstallResult{}, mailVPSInstallError(err)
		}
		prepared, _, err = provision.Remote(ctx, request, input.Action, observer)
		source = prepared.SourceSpec()
	case "prepare":
		source, err = provision.HandoffPrepare(ctx, request, current, input.DrainIDs, input.TotalCapacityBytes, observer, relay, observer)
	case "commit":
		source, err = provision.HandoffCommit(ctx, request, current, input.DrainIDs, input.TotalCapacityBytes, observer, relay, mailInstallConfigService{ConfigService: service, validator: validator}, observer, observer)
	default:
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
	}
	if err != nil || call.Reauthorize(ctx) != nil {
		if err != nil {
			return httpapi.ServiceMailInstallResult{}, mailVPSInstallError(err)
		}
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
	}
	state := map[string]string{"publish": "published", "activate": "activated", "prepare": "prepared", "commit": "configured"}[input.Action]
	return httpapi.ServiceMailInstallResult{State: state, SourceID: source.ID}, nil
}
func mailVPSInstallError(err error) error {
	if errors.Is(err, installer.ErrConflict) || errors.Is(err, installer.ErrJournal) {
		return httpapi.ErrMailInstallConflict
	}
	return httpapi.ErrMailInstallUnknown
}
