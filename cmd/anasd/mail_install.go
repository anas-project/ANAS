package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/modules/mail/cloudflare/installer"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

var mailInstallOperation = regexp.MustCompile(`^cfg-[a-f0-9]{32}$`)

// mailInstallationDirectory is rooted in host configuration, never an API
// path. Each level is private and owned by the daemon; no symlink is followed.
func mailInstallationDirectory(base, workspace, operation string) (string, error) {
	if !filepath.IsAbs(base) || !mailInstallOperation.MatchString(operation) || workspace == "" || filepath.Base(workspace) != workspace || workspace == "." || workspace == ".." {
		return "", installer.ErrJournal
	}
	parent, err := unix.Open(filepath.Dir(base), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", installer.ErrJournal
	}
	defer func() { _ = unix.Close(parent) }()
	var parentInfo unix.Stat_t
	if unix.Fstat(parent, &parentInfo) != nil || parentInfo.Uid != 0 && parentInfo.Uid != uint32(os.Geteuid()) || parentInfo.Mode&0022 != 0 && parentInfo.Mode&unix.S_ISVTX == 0 {
		return "", installer.ErrJournal
	}
	for _, name := range []string{filepath.Base(base), workspace, operation} {
		if err := unix.Mkdirat(parent, name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return "", installer.ErrJournal
		}
		child, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return "", installer.ErrJournal
		}
		var st unix.Stat_t
		if unix.Fstat(child, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR || unix.Fsync(parent) != nil {
			unix.Close(child)
			return "", installer.ErrJournal
		}
		unix.Close(parent)
		parent = child
	}
	return filepath.Join(base, workspace, operation), nil
}

// Separate operation journals share this per-workspace lock, so two distinct
// requests cannot both provision against the same stale source snapshot.
func lockMailInstallWorkspace(path string) (func(), error) {
	dir, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, installer.ErrJournal
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, ".install.lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, installer.ErrJournal
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		unix.Close(fd)
		return nil, installer.ErrJournal
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, installer.ErrBusy
	}
	return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }, nil
}

// A single operation ID cannot be reused for both Worker and VPS. Existing
// Worker operation directories remain in place; a VPS uses a private child.
func relayOperationDirectory(path string) (string, error) {
	dir, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", installer.ErrJournal
	}
	defer unix.Close(dir)
	scan, err := unix.Openat(dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", installer.ErrJournal
	}
	f := os.NewFile(uintptr(scan), "mail operation directory")
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil || len(names) > 1 || len(names) == 1 && names[0] != "relay" {
		return "", installer.ErrConflict
	}
	if len(names) == 0 {
		if err = unix.Mkdirat(dir, "relay", 0700); err != nil {
			return "", installer.ErrJournal
		}
	}
	if unix.Fsync(dir) != nil {
		return "", installer.ErrJournal
	}
	child, err := unix.Openat(dir, "relay", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", installer.ErrJournal
	}
	defer unix.Close(child)
	var st unix.Stat_t
	if unix.Fstat(child, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", installer.ErrJournal
	}
	return filepath.Join(path, "relay"), nil
}

func workerOperationAllowed(path string) bool {
	for _, name := range []string{"relay", "vps-ssh"} {
		if _, err := os.Lstat(filepath.Join(path, name)); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}

type mailInstallAudit struct {
	call   httpapi.ServiceMailInstallCall
	writer consoleaudit.Appender
}

func (a mailInstallAudit) write(ctx context.Context, action string, operation string) error {
	if a.writer == nil || a.call.Reauthorize == nil || a.call.Reauthorize(ctx) != nil {
		return audit.ErrUnavailable
	}
	_, err := a.writer.AppendContext(ctx, audit.Event{Type: action, Actor: "service:" + a.call.ClientID, WorkspaceID: a.call.WorkspaceID, Outcome: "authorized", Details: map[string]any{"actor_source": "service_declared", "declared_actor": a.call.Request.Actor, "operation_id": operation, "application": "mail"}})
	return err
}
func (a mailInstallAudit) BeforeMailProvision(ctx context.Context, intent installer.ProvisionIntent) error {
	return a.write(ctx, "service.mail.ingress.prepare", intent.OperationID)
}
func (a mailInstallAudit) BeforeMailCommit(ctx context.Context, intent installer.ProvisionIntent) error {
	return a.write(ctx, "service.mail.ingress.commit", intent.OperationID)
}
func (a mailInstallAudit) BeforeRelayPrepare(ctx context.Context, intent installer.RelayInstallIntent) error {
	return a.write(ctx, "service.mail.ingress.prepare", intent.OperationID)
}
func (a mailInstallAudit) BeforeRelayCommit(ctx context.Context, intent installer.RelayInstallIntent) error {
	return a.write(ctx, "service.mail.ingress.commit", intent.OperationID)
}
func (a mailInstallAudit) AuthorizeRelayProvision(ctx context.Context) error {
	if a.call.Reauthorize == nil {
		return audit.ErrUnavailable
	}
	return a.call.Reauthorize(ctx)
}
func (a mailInstallAudit) BeforeRelayRemote(ctx context.Context, intent installer.RelayProvisionIntent) error {
	if intent.Action != "publish" && intent.Action != "activate" && intent.Action != "inspect" {
		return audit.ErrUnavailable
	}
	return a.write(ctx, "service.mail.ingress.vps_"+intent.Action, intent.OperationID)
}
func (a mailInstallAudit) BeforeConfigCommit(ctx context.Context, intent application.ConfigCommitIntent) error {
	return a.write(ctx, "service.mail.ingress.config_commit", intent.OperationID)
}

// The policy comes from the managed workspace, not from service-supplied
// allowed_domains. Both reads must name the same configuration generation.
func mailInstallPolicy(snapshot application.ConfigSnapshot) ([]string, error) {
	if !snapshot.Managed || snapshot.Validator == "" {
		return nil, installer.ErrConflict
	}
	if env, ok := snapshot.Config["env"].(map[string]any); ok {
		if _, exists := env["SAMBA_DC_MAIL_MANAGEMENT_POLICY"]; exists {
			return nil, installer.ErrConflict
		}
	}
	modules, ok := snapshot.Config["modules"].(map[string]any)
	if !ok {
		return nil, installer.ErrConflict
	}
	samba, ok := modules["samba_dc"].(map[string]any)
	if !ok {
		return nil, installer.ErrConflict
	}
	values, ok := samba["config"].(map[string]any)
	if !ok {
		return nil, installer.ErrConflict
	}
	raw, ok := values["mail_management_policy"].(string)
	if !ok {
		return nil, installer.ErrConflict
	}
	policy, err := directorymail.ParsePolicyJSON(raw)
	if err != nil {
		return nil, installer.ErrConflict
	}
	return append([]string(nil), policy.Domains...), nil
}

type mailInstallConfigService struct {
	application.ConfigService
	validator string
}

func (s mailInstallConfigService) GetConfig(ctx context.Context) (application.ConfigSnapshot, error) {
	snapshot, err := s.ConfigService.GetConfig(ctx)
	if err != nil || snapshot.Validator != s.validator {
		return application.ConfigSnapshot{}, installer.ErrConflict
	}
	return snapshot, nil
}
func (s mailInstallConfigService) ValidateConfig(ctx context.Context, candidate application.ConfigCandidate) (application.ConfigValidationResult, error) {
	result, err := s.ConfigService.ValidateConfig(ctx, candidate)
	if err != nil || result.BaseValidator != s.validator {
		return application.ConfigValidationResult{}, installer.ErrConflict
	}
	return result, nil
}

func newMailInstallHandler(root string, writer consoleaudit.Appender) func(context.Context, httpapi.ServiceMailInstallCall) (httpapi.ServiceMailInstallResult, error) {
	return func(ctx context.Context, call httpapi.ServiceMailInstallCall) (httpapi.ServiceMailInstallResult, error) {
		if call.Reauthorize == nil || call.Reauthorize(ctx) != nil || !mailInstallOperation.MatchString(call.Request.OperationID) {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		}
		dir, err := mailInstallationDirectory(root, call.WorkspaceID, call.Request.OperationID)
		if err != nil {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		}
		unlock, err := lockMailInstallWorkspace(filepath.Dir(dir))
		if err != nil {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		}
		defer unlock()
		service := runner.NewWorkspaceConfigService(call.WorkspacePath)
		config, err := service.GetConfig(ctx)
		if err != nil {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		}
		domains, err := mailInstallPolicy(config)
		if err != nil || !reflect.DeepEqual(domains, call.Request.AllowedDomains) {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
		}
		observer := mailInstallAudit{call: call, writer: writer}
		if observer.write(ctx, "service.mail.ingress.read", call.Request.OperationID) != nil {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		}
		snapshot, err := runner.ReadMailIngressSnapshot(ctx, call.WorkspacePath)
		if err != nil || snapshot.Validator != config.Validator {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		}
		current, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, domains)
		if err != nil {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
		}
		if call.Request.Provider == "vps-ssh" {
			return handleMailVPSInstall(ctx, call, dir, observer, service, snapshot.Validator, domains, current)
		}
		if call.Request.Provider == "cloud-relay" {
			return handleMailRelayInstall(ctx, call, dir, observer, service, snapshot.Validator, domains, current)
		}
		if !workerOperationAllowed(dir) {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
		}
		spec := installer.Spec{AccountID: call.Request.AccountID, Worker: call.Request.Worker, Bucket: call.Request.Bucket}
		journal, err := installer.OpenJournal(filepath.Join(dir, "journal.jsonl"), spec)
		if err != nil {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		}
		defer journal.Close()
		input := call.Request
		request := installer.InstallRequest{Runtime: installer.RuntimeRequest{Domains: input.Domains, AllowedDomains: domains, CapacityBytes: input.CapacityBytes, DeliveredDays: input.DeliveredDays, NotificationOrigin: input.NotificationOrigin, ZoneID: input.ZoneID, RoutingToken: input.RoutingToken}, DrainIDs: input.DrainIDs, TotalCapacity: input.TotalCapacityBytes, OperationID: input.OperationID}
		request, err = journal.RequestWithCurrentSources(request, current)
		if err != nil {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
		}
		var source ingress.SourceSpec
		if input.Action == "prepare" {
			cloud, e := installer.NewCloudflareWithAuthorization(input.DeploymentToken, call.Reauthorize)
			if e != nil {
				return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
			}
			source, err = journal.PrepareInstallRuntime(ctx, cloud, observer, request)
		} else if input.Action == "commit" {
			source, err = journal.CommitPreparedRuntime(ctx, mailInstallConfigService{ConfigService: service, validator: snapshot.Validator}, observer, observer, request)
		} else {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
		}
		if errors.Is(err, installer.ErrConflict) {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
		}
		if err != nil {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		}
		state := "prepared"
		if input.Action == "commit" {
			state = "configured"
		}
		return httpapi.ServiceMailInstallResult{State: state, SourceID: source.ID}, nil
	}
}

func handleMailRelayInstall(ctx context.Context, call httpapi.ServiceMailInstallCall, dir string, observer mailInstallAudit, service application.ConfigService, validator string, domains []string, current []ingress.RuntimeSource) (httpapi.ServiceMailInstallResult, error) {
	input := call.Request
	relayDir, err := relayOperationDirectory(dir)
	if err != nil {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
	}
	// A premature commit must not create an empty journal that prevents a later prepare.
	if input.Action == "commit" {
		if _, err := os.Lstat(filepath.Join(relayDir, "relay.lock")); errors.Is(err, os.ErrNotExist) {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		} else if err != nil {
			return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
		}
	}
	journal, err := installer.OpenRelayJournal(relayDir)
	if err != nil {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
	}
	defer journal.Close()
	request := installer.RelayInstallRequest{Runtime: installer.RelayRequest{SourceID: input.SourceID, Generation: input.Generation, Origin: input.SourceOrigin, Domains: input.Domains, AllowedDomains: domains, CapacityBytes: input.CapacityBytes, DeliveredDays: input.DeliveredDays, NotificationOrigin: input.NotificationOrigin, Reader: input.ReaderToken, Control: input.ControlToken, Notification: input.NotificationToken, IPv4Only: input.IPv4Only}, DrainIDs: input.DrainIDs, TotalCapacity: input.TotalCapacityBytes, OperationID: input.OperationID}
	request, err = journal.RequestWithCurrentSources(request, current)
	if err != nil {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
	}
	var source ingress.SourceSpec
	if input.Action == "prepare" {
		source, err = journal.Prepare(ctx, observer, request)
	} else if input.Action == "commit" {
		source, err = journal.Commit(ctx, mailInstallConfigService{ConfigService: service, validator: validator}, observer, observer, request)
	} else {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
	}
	if errors.Is(err, installer.ErrConflict) {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallConflict
	}
	if err != nil || call.Reauthorize(ctx) != nil {
		return httpapi.ServiceMailInstallResult{}, httpapi.ErrMailInstallUnknown
	}
	state := "prepared"
	if input.Action == "commit" {
		state = "configured"
	}
	return httpapi.ServiceMailInstallResult{State: state, SourceID: source.ID}, nil
}
