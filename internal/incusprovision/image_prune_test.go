package incusprovision

import (
	"slices"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/deployment"
)

func TestImagePrunePlanRetainsCurrentPreviousInstancesAndUnknown(t *testing.T) {
	fp := func(ch string) string { return strings.Repeat(ch, 64) }
	state := deployment.ActiveState{APIVersion: deployment.StateAPIVersion, ActiveDeployment: "dep-current", PreviousDeployments: []string{"dep-prev"}}
	history := []imagePruneHistory{
		{Managed: true, Project: "anas-a", Fingerprint: fp("a"), Deployment: "dep-current", Current: true},
		{Managed: true, Project: "anas-a", Fingerprint: fp("b"), Deployment: "dep-prev", Previous: true},
		{Managed: true, Project: "anas-a", Fingerprint: fp("c"), Deployment: "dep-old"},
		{Managed: true, Project: "anas-b", Fingerprint: fp("d"), Deployment: "dep-old"},
	}
	images := []imagePruneImage{
		{Project: "anas-a", Fingerprint: fp("a")},
		{Project: "anas-a", Fingerprint: fp("b")},
		{Project: "anas-a", Fingerprint: fp("c")},
		{Project: "anas-a", Fingerprint: fp("e")},
		{Project: "anas-b", Fingerprint: fp("d")},
	}
	instances := map[ImagePruneTarget]bool{{Project: "anas-b", Fingerprint: fp("d")}: true}
	plan, err := buildImagePrunePlan("main", "/var/lib/incus/unix.socket", 1, state, []string{fp("a")}, []string{fp("b")}, history, images, instances, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 1 || plan.Delete[0] != (ImagePruneTarget{Project: "anas-a", Fingerprint: fp("c")}) {
		t.Fatalf("delete = %#v", plan.Delete)
	}
	reasons := map[string][]string{}
	for _, item := range plan.Retained {
		reasons[item.Project+"/"+item.Fingerprint] = item.Reasons
	}
	if !slices.Contains(reasons["anas-a/"+fp("a")], "current_deployment") {
		t.Fatalf("current image was not retained: %#v", reasons)
	}
	if !slices.Contains(reasons["anas-a/"+fp("b")], "previous_deployment") {
		t.Fatalf("previous image was not retained: %#v", reasons)
	}
	if !slices.Contains(reasons["anas-b/"+fp("d")], "instance_base_image") {
		t.Fatalf("instance image was not retained: %#v", reasons)
	}
	if !slices.Contains(reasons["anas-a/"+fp("e")], "unknown_external") {
		t.Fatalf("unknown image was not retained: %#v", reasons)
	}
	if plan.Digest == "" || plan.StateDigest == "" || plan.Digest == plan.StateDigest {
		t.Fatalf("digests not populated distinctly: %#v", plan)
	}
}

func TestSamePruneTargetsIgnoresOrder(t *testing.T) {
	a := []ImagePruneTarget{{Project: "b", Fingerprint: strings.Repeat("b", 64)}, {Project: "a", Fingerprint: strings.Repeat("a", 64)}}
	b := []ImagePruneTarget{{Project: "a", Fingerprint: strings.Repeat("a", 64)}, {Project: "b", Fingerprint: strings.Repeat("b", 64)}}
	if !samePruneTargets(a, b) {
		t.Fatal("same target set did not match")
	}
	b[1].Fingerprint = strings.Repeat("c", 64)
	if samePruneTargets(a, b) {
		t.Fatal("different target set matched")
	}
}
