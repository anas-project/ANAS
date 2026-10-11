package runner

import (
	"testing"

	"github.com/anas-project/ANAS/internal/computeimage"
)

// INCUS-R-165: persistent volumes are an optional, frozen declaration.
func TestValidateComputeSpecReadsOptionalVolumes(t *testing.T) {
	spec := validComputeSpec()
	spec["volumes"] = map[string]any{"max_count": 4, "total_gib": 40}
	quota, _, err := validateComputeSpec("ai_agent", "work", spec)
	if err != nil || quota.VolumeCount != 4 || quota.VolumeGiB != 40 {
		t.Fatalf("quota = %+v %v", quota, err)
	}
	for name, volumes := range map[string]any{
		"not an object":    "4",
		"missing count":    map[string]any{"total_gib": 40},
		"missing capacity": map[string]any{"max_count": 4},
		"zero count":       map[string]any{"max_count": 0, "total_gib": 40},
		"count too large":  map[string]any{"max_count": 65, "total_gib": 40},
		"capacity too big": map[string]any{"max_count": 1, "total_gib": 8193},
		"host path":        map[string]any{"max_count": 1, "total_gib": 1, "source": "/srv"},
		"pool override":    map[string]any{"max_count": 1, "total_gib": 1, "pool": "other"},
	} {
		spec := validComputeSpec()
		spec["volumes"] = volumes
		if _, _, err := validateComputeSpec("ai_agent", "work", spec); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestComputeVolumeQuotaReachesProviderAndConsumer(t *testing.T) {
	a := computeApp(t, map[string]string{"ai_agent": "anas-ai-agent", "forgejo": "anas-forgejo-runners"})
	a.reg["ai_agent"].Resources[0].Spec["volumes"] = map[string]any{"max_count": 4, "total_gib": 40}
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []string{"ai_agent", "forgejo"} {
		if err := a.publishModuleResources(consumer); err != nil {
			t.Fatal(err)
		}
	}
	agent, forgejo := computeResourcePrefix("ai_agent", "runners"), computeResourcePrefix("forgejo", "runners")
	if a.env[agent+"VOLUME_COUNT"] != "4" || a.env[agent+"VOLUME_GIB"] != "40" {
		t.Fatalf("consumer volume quota = %q/%q", a.env[agent+"VOLUME_COUNT"], a.env[agent+"VOLUME_GIB"])
	}
	if _, ok := a.env[forgejo+"VOLUME_COUNT"]; ok {
		t.Fatal("a lease without volumes must not publish a volume quota")
	}

	for consumer, want := range map[string][2]string{"ai_agent": {"4", "40"}, "forgejo": {"0", "0"}} {
		spec := a.reg[consumer].Resources[0].Spec
		refs, err := computeimage.Parse(spec["image_allowlist"])
		if err != nil {
			t.Fatal(err)
		}
		images, err := computeimage.Freeze(refs, computeimage.Target{Architecture: "amd64", Interface: "incus_vm"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		request := ResourceRequest{Consumer: consumer, ID: "runners", Interface: "incus_vm", Spec: spec, ComputeImages: images, Credential: a.secrets.values[resourceSecretKey(consumer, "runners", "compute")]}
		env := map[string]string{}
		if err := projectComputeProviderEnv(env, request); err != nil {
			t.Fatal(err)
		}
		if env["ANAS_RESOURCE_VOLUME_COUNT"] != want[0] || env["ANAS_RESOURCE_VOLUME_GIB"] != want[1] {
			t.Errorf("%s provider volume quota = %q/%q", consumer, env["ANAS_RESOURCE_VOLUME_COUNT"], env["ANAS_RESOURCE_VOLUME_GIB"])
		}
	}
}
