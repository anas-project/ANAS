package incusprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/computeimage"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/deployment"
)

const (
	ImagePruneSchema          = "anas.incus-image-prune/v1"
	ServiceConfigPath         = "/etc/anas/anasd.yml"
	defaultIncusUnixSocket    = "/var/lib/incus/unix.socket"
	imagePruneMaxWorkspaces   = 1024
	imagePruneMaxProjects     = 2048
	imagePruneMaxImages       = 4096
	imagePruneMaxDeployments  = 4096
	imagePruneMaxFingerprints = 8192
)

type ImagePruneRequest struct {
	Schema      string `json:"schema"`
	WorkspaceID string `json:"workspace_id"`
}

type ImagePruneBinding struct {
	Schema        string             `json:"schema"`
	PlanDigest    string             `json:"plan_digest"`
	WorkspaceID   string             `json:"workspace_id"`
	Delete        []ImagePruneTarget `json:"delete"`
	StateDigest   string             `json:"state_digest"`
	SummaryDigest string             `json:"summary_digest"`
}

type ImagePruneTarget struct {
	Project     string `json:"project"`
	Fingerprint string `json:"fingerprint"`
}

type ImagePrunePlanResult struct {
	Schema      string              `json:"schema"`
	WorkspaceID string              `json:"workspace_id"`
	Host        ImagePruneHost      `json:"host"`
	Summary     ImagePruneSummary   `json:"summary"`
	Retained    []ImagePruneRetain  `json:"retained"`
	Delete      []ImagePruneTarget  `json:"delete"`
	Inventory   ImagePruneInventory `json:"inventory"`
	StateDigest string              `json:"state_digest"`
	Digest      string              `json:"digest"`
	Blockers    []string            `json:"blockers,omitempty"`
}

type ImagePruneApplyResult struct {
	Schema      string                    `json:"schema"`
	WorkspaceID string                    `json:"workspace_id"`
	PlanDigest  string                    `json:"plan_digest"`
	Deleted     []ImagePruneDeleteReceipt `json:"deleted"`
	Unchanged   []ImagePruneTarget        `json:"unchanged,omitempty"`
	Partial     bool                      `json:"partial"`
}

type ImagePruneDeleteReceipt struct {
	Project     string `json:"project"`
	Fingerprint string `json:"fingerprint"`
	Verified    bool   `json:"verified"`
}

type ImagePruneHost struct {
	Socket          string `json:"socket"`
	WorkspaceCount  int    `json:"workspace_count"`
	TargetWorkspace string `json:"target_workspace"`
	ManagedDaemon   string `json:"managed_daemon"`
}

type ImagePruneSummary struct {
	CurrentDeployments  []string `json:"current_deployments"`
	PreviousDeployments []string `json:"previous_deployments"`
	ManagedProjects     []string `json:"managed_projects"`
	Retained            int      `json:"retained"`
	Delete              int      `json:"delete"`
	Unknown             int      `json:"unknown"`
}

type ImagePruneRetain struct {
	Project     string   `json:"project,omitempty"`
	Fingerprint string   `json:"fingerprint"`
	Reasons     []string `json:"reasons"`
}

type ImagePruneInventory struct {
	ImagesSeen     int `json:"images_seen"`
	InstancesSeen  int `json:"instances_seen"`
	ManagedHistory int `json:"managed_history"`
}

type ImagePruneBackend struct {
	socket     string
	client     *incusUnixClient
	store      stateStore
	openView   func(context.Context, consoleconfig.Config, ConnectionBundle, string) (*imagePruneView, error)
	verifyHost func(context.Context, State) error
}

func NewImagePruneBackend() *ImagePruneBackend {
	return &ImagePruneBackend{socket: defaultIncusUnixSocket, client: &incusUnixClient{socket: defaultIncusUnixSocket}, store: newFileStateStore(), openView: openInstalledImagePruneView, verifyHost: verifyImagePruneHost}
}

var loadImagePruneServiceConfig = func() (consoleconfig.Config, error) {
	return consoleconfig.LoadService(ServiceConfigPath)
}

func (b *ImagePruneBackend) Plan(ctx context.Context, request ImagePruneRequest) (ImagePrunePlanResult, error) {
	plan, _, err := b.runImagePruneSession(ctx, request, nil)
	return plan, err
}

func (b *ImagePruneBackend) Apply(ctx context.Context, request ImagePruneRequest, binding ImagePruneBinding) (ImagePruneApplyResult, error) {
	if b == nil || request.Schema != ImagePruneSchema || binding.Schema != ImagePruneSchema ||
		request.WorkspaceID == "" || request.WorkspaceID != binding.WorkspaceID || binding.PlanDigest == "" {
		return ImagePruneApplyResult{}, ErrInvalid
	}
	_, out, err := b.runImagePruneSession(ctx, request, &binding)
	return out, err
}

type imagePruneImage struct {
	Project     string
	Fingerprint string
}

type imagePruneHistory struct {
	Workspace   string
	Consumer    string
	Managed     bool
	Project     string
	Fingerprint string
	Deployment  string
	Current     bool
	Previous    bool
}

func buildImagePrunePlan(workspaceID, socket string, workspaceCount int, state deployment.ActiveState, current, previous []string, history []imagePruneHistory, images []imagePruneImage, instanceRefs map[ImagePruneTarget]bool, instancesSeen int) (ImagePrunePlanResult, error) {
	if len(history) > imagePruneMaxFingerprints || len(images) > imagePruneMaxImages {
		return ImagePrunePlanResult{}, ErrBlocked
	}
	imageSet := map[ImagePruneTarget]bool{}
	for _, img := range images {
		imageSet[ImagePruneTarget{Project: img.Project, Fingerprint: img.Fingerprint}] = true
	}
	managed := map[ImagePruneTarget]bool{}
	retainReasons := map[ImagePruneTarget]map[string]bool{}
	projects := map[string]bool{}
	for _, h := range history {
		target := ImagePruneTarget{Project: h.Project, Fingerprint: h.Fingerprint}
		if !h.Managed {
			addRetain(retainReasons, target, "unverified_ownership")
		} else if h.Workspace == "" || h.Workspace == workspaceID {
			managed[target] = true
		} else {
			addRetain(retainReasons, target, "other_workspace")
		}
		projects[h.Project] = true
		if h.Current {
			addRetain(retainReasons, target, "current_deployment")
		}
		if h.Previous {
			addRetain(retainReasons, target, "previous_deployment")
		}
	}
	for target := range instanceRefs {
		addRetain(retainReasons, target, "instance_base_image")
	}
	for _, img := range images {
		target := ImagePruneTarget{Project: img.Project, Fingerprint: img.Fingerprint}
		if !managed[target] {
			addRetain(retainReasons, target, "unknown_external")
		}
	}
	var imported, currentPins, previousPins, runningPins []string
	for target := range managed {
		if imageSet[target] {
			imported = append(imported, target.Fingerprint)
		}
	}
	currentPins = append(currentPins, current...)
	previousPins = append(previousPins, previous...)
	for target := range instanceRefs {
		runningPins = append(runningPins, target.Fingerprint)
	}
	if _, err := computeimage.PlanPrune(imported, currentPins, previousPins, runningPins); err != nil {
		return ImagePrunePlanResult{}, ErrInvalid
	}
	out := ImagePrunePlanResult{Schema: ImagePruneSchema, WorkspaceID: workspaceID, Host: ImagePruneHost{Socket: socket, WorkspaceCount: workspaceCount, TargetWorkspace: workspaceID, ManagedDaemon: "local-incus-unix-socket"}}
	out.Summary.CurrentDeployments = []string{state.ActiveDeployment}
	out.Summary.PreviousDeployments = append([]string{}, state.PreviousDeployments...)
	for project := range projects {
		out.Summary.ManagedProjects = append(out.Summary.ManagedProjects, project)
	}
	sort.Strings(out.Summary.ManagedProjects)
	for target := range managed {
		if !imageSet[target] {
			addRetain(retainReasons, target, "not_present")
			continue
		}
		if len(retainReasons[target]) == 0 {
			out.Delete = append(out.Delete, target)
		}
	}
	for target, reasons := range retainReasons {
		item := ImagePruneRetain{Project: target.Project, Fingerprint: target.Fingerprint}
		for reason := range reasons {
			item.Reasons = append(item.Reasons, reason)
		}
		sort.Strings(item.Reasons)
		out.Retained = append(out.Retained, item)
		if slices.Contains(item.Reasons, "unknown_external") || slices.Contains(item.Reasons, "not_present") {
			out.Summary.Unknown++
		}
	}
	sort.Slice(out.Delete, func(i, j int) bool {
		return out.Delete[i].Project+"/"+out.Delete[i].Fingerprint < out.Delete[j].Project+"/"+out.Delete[j].Fingerprint
	})
	sort.Slice(out.Retained, func(i, j int) bool {
		return out.Retained[i].Project+"/"+out.Retained[i].Fingerprint < out.Retained[j].Project+"/"+out.Retained[j].Fingerprint
	})
	out.Summary.Retained = len(out.Retained)
	out.Summary.Delete = len(out.Delete)
	out.Inventory = ImagePruneInventory{ImagesSeen: len(images), InstancesSeen: instancesSeen, ManagedHistory: len(history)}
	out.StateDigest = digestImagePruneState(state, history, images, instanceRefs)
	out.Digest = digestImagePruneSummary(out)
	return out, nil
}

func addRetain(reasons map[ImagePruneTarget]map[string]bool, target ImagePruneTarget, reason string) {
	if reasons[target] == nil {
		reasons[target] = map[string]bool{}
	}
	reasons[target][reason] = true
}

func digestImagePruneState(state deployment.ActiveState, history []imagePruneHistory, images []imagePruneImage, refs map[ImagePruneTarget]bool) string {
	lines := []string{state.ActiveDeployment, strings.Join(state.PreviousDeployments, ",")}
	for _, h := range history {
		lines = append(lines, h.Project+"\x00"+h.Fingerprint+"\x00"+h.Deployment)
	}
	for _, img := range images {
		lines = append(lines, img.Project+"\x00"+img.Fingerprint)
	}
	for ref := range refs {
		lines = append(lines, ref.Project+"\x00"+ref.Fingerprint)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func digestImagePruneSummary(plan ImagePrunePlanResult) string {
	lines := []string{plan.WorkspaceID, plan.StateDigest}
	for _, target := range plan.Delete {
		lines = append(lines, "delete\x00"+target.Project+"\x00"+target.Fingerprint)
	}
	for _, item := range plan.Retained {
		lines = append(lines, "retain\x00"+item.Project+"\x00"+item.Fingerprint+"\x00"+strings.Join(item.Reasons, ","))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func samePruneTargets(a, b []ImagePruneTarget) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]ImagePruneTarget{}, a...)
	y := append([]ImagePruneTarget{}, b...)
	sort.Slice(x, func(i, j int) bool { return x[i].Project+"/"+x[i].Fingerprint < x[j].Project+"/"+x[j].Fingerprint })
	sort.Slice(y, func(i, j int) bool { return y[i].Project+"/"+y[i].Fingerprint < y[j].Project+"/"+y[j].Fingerprint })
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

type incusImage struct {
	Fingerprint string `json:"fingerprint"`
	Project     string `json:"project"`
}

type incusInstanceWithConfig struct {
	Project string            `json:"project"`
	Config  map[string]string `json:"config"`
}

func (c *incusUnixClient) getImage(ctx context.Context, project, fingerprint string) (incusImage, error) {
	var out incusImage
	err := c.do(ctx, http.MethodGet, "/1.0/images/"+url.PathEscape(fingerprint)+"?project="+url.QueryEscape(project), nil, &out)
	return out, err
}

func (c *incusUnixClient) deleteImage(ctx context.Context, project, fingerprint string) error {
	return c.do(ctx, http.MethodDelete, "/1.0/images/"+url.PathEscape(fingerprint)+"?project="+url.QueryEscape(project), nil, nil)
}
