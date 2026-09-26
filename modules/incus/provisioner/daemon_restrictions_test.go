package main

import (
	"context"
	"slices"
	"strings"
	"testing"
)

var (
	// Restriction extensions advertised by Incus 7.0.1 LTS and 7.5.1
	// (internal/version/api.go at those tags).
	incus70Extensions = []string{"projects", "projects_restricted_image_servers", "projects_restricted_storage_pool_access"}
	incus75Extensions = append(slices.Clone(incus70Extensions), "projects_restricted_virtual_machines_nesting")

	// Restriction keys accepted by Incus 7.5.1 (projectConfigKeys).
	incus75Restrictions = append(slices.Clone(incus60Restrictions),
		"restricted.images.servers", "restricted.storage-pools.access", "restricted.virtual-machines.nesting")
)

func TestReadDaemonRestrictionsFromAPIExtensions(t *testing.T) {
	for name, scenario := range map[string]struct {
		extensions []string
		want       daemonRestrictions
		fails      bool
	}{
		"incus 6.0 LTS":       {extensions: []string{"projects"}},
		"incus 7.0 LTS":       {extensions: incus70Extensions, want: daemonRestrictions{StoragePoolAccess: true}},
		"incus 7.5":           {extensions: incus75Extensions, want: daemonRestrictions{StoragePoolAccess: true, VMNesting: true}},
		"no extension report": {extensions: nil, fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			d.apiExtensions = scenario.extensions
			got, err := readDaemonRestrictions(context.Background(), d.clientFor(t))
			if scenario.fails {
				if err == nil {
					t.Fatal("a daemon that hides its extensions must not be treated as 6.0")
				}
				return
			}
			if err != nil || got != scenario.want {
				t.Fatalf("restrictions = %+v %v, want %+v", got, err, scenario.want)
			}
		})
	}
}

// INCUS-R-011 on Incus 7.x: the newer keys exist there, and two of them
// default to the permissive value.
func TestProjectConfigOwnsEveryIncus75Restriction(t *testing.T) {
	for _, isolation := range []string{"container", "vm"} {
		l := testLease(t, isolation)
		l.Daemon = daemonRestrictions{StoragePoolAccess: true, VMNesting: true}
		config := projectConfig(l)
		for _, key := range incus75Restrictions {
			_, written := config[key]
			if written == slices.Contains(clearedRestrictions, key) {
				t.Errorf("%s: %s must be either written or cleared, not both or neither", isolation, key)
			}
		}
		for key := range config {
			if strings.HasPrefix(key, "restricted.") && !slices.Contains(incus75Restrictions, key) {
				t.Errorf("%s: %s is not accepted by Incus 7.5", isolation, key)
			}
		}
		if config["restricted.virtual-machines.nesting"] != "block" || config["restricted.storage-pools.access"] != l.StoragePool {
			t.Errorf("%s: 7.x keys = %q / %q", isolation, config["restricted.virtual-machines.nesting"], config["restricted.storage-pools.access"])
		}
	}
}

func TestEnsureWritesNewerRestrictionsOnlyWhereTheDaemonAdvertisesThem(t *testing.T) {
	for name, scenario := range map[string]struct {
		extensions []string
		want       map[string]string
	}{
		"incus 6.0 LTS": {extensions: []string{"projects"}, want: map[string]string{}},
		"incus 7.0 LTS": {extensions: incus70Extensions, want: map[string]string{"restricted.storage-pools.access": "default"}},
		"incus 7.5":     {extensions: incus75Extensions, want: map[string]string{"restricted.storage-pools.access": "default", "restricted.virtual-machines.nesting": "block"}},
	} {
		for _, isolation := range []string{"container", "vm"} {
			t.Run(name+"/"+isolation, func(t *testing.T) {
				d := newFakeDaemon(t)
				d.apiExtensions = scenario.extensions
				l := testLease(t, isolation)
				result, err := ensure(context.Background(), d.clientFor(t), l)
				if err != nil || !result.Ready {
					t.Fatalf("ensure: %+v %v", result, err)
				}
				config := d.projects[l.Sandbox]
				for _, key := range []string{"restricted.storage-pools.access", "restricted.virtual-machines.nesting", "restricted.images.servers"} {
					if config[key] != scenario.want[key] {
						t.Errorf("%s = %q, want %q (a key the daemon does not know would be rejected)", key, config[key], scenario.want[key])
					}
				}
			})
		}
	}
}

func TestEnsureConvergesRelaxedNewerRestrictionsOnAdoptedProject(t *testing.T) {
	d := newFakeDaemon(t)
	d.apiExtensions = incus75Extensions
	l := testLease(t, "vm")
	d.projects[l.Sandbox] = map[string]string{
		"features.networks":                   "false",
		"restricted":                          "true",
		"restricted.virtual-machines.nesting": "allow",
		"restricted.storage-pools.access":     "default, scratch",
		"restricted.images.servers":           "images.example",
	}
	result, err := ensure(context.Background(), d.clientFor(t), l)
	if err != nil || !result.Ready {
		t.Fatalf("ensure: %+v %v", result, err)
	}
	config := d.projects[l.Sandbox]
	if config["restricted.virtual-machines.nesting"] != "block" || config["restricted.storage-pools.access"] != "default" {
		t.Fatalf("relaxed 7.x restrictions survived: %q / %q", config["restricted.virtual-machines.nesting"], config["restricted.storage-pools.access"])
	}
	if _, present := config["restricted.images.servers"]; present {
		t.Fatal("an image server list would block creating instances from the lease's own images")
	}
}

func TestInspectRejectsRelaxedNewerRestrictionsWithoutRepair(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"vm nesting allowed":  func(config map[string]string) { config["restricted.virtual-machines.nesting"] = "allow" },
		"pool access removed": func(config map[string]string) { delete(config, "restricted.storage-pools.access") },
		"pool access widened": func(config map[string]string) { config["restricted.storage-pools.access"] = "default,scratch" },
		"image server list":   func(config map[string]string) { config["restricted.images.servers"] = "images.example" },
	} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			d.apiExtensions = incus75Extensions
			l := testLease(t, "vm")
			c := d.clientFor(t)
			if _, err := ensure(context.Background(), c, l); err != nil {
				t.Fatal(err)
			}
			mutate(d.projects[l.Sandbox])
			posts, puts := len(d.posted), len(d.puts)
			result, err := inspect(context.Background(), c, l)
			if err != nil || result.Ready || !result.Exists {
				t.Fatalf("relaxed 7.x fence reported ready: %+v %v", result, err)
			}
			if len(d.posted) != posts || len(d.puts) != puts {
				t.Fatal("inspect repaired a project instead of remaining read-only")
			}
		})
	}
}

// Incus 7.5 turns VM nesting on by default and, with the restriction blocked,
// rejects any VM that does not disable it. security.nesting is container-only
// on daemons without the extension, so the profile must not carry it there.
func TestVMTierProfileDisablesNestingOnlyWhereTheDaemonSupportsIt(t *testing.T) {
	for name, scenario := range map[string]struct {
		extensions []string
		want       string
	}{
		"incus 6.0 LTS": {extensions: []string{"projects"}},
		"incus 7.0 LTS": {extensions: incus70Extensions},
		"incus 7.5":     {extensions: incus75Extensions, want: "false"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			d.apiExtensions = scenario.extensions
			l := testLease(t, "vm")
			c := d.clientFor(t)
			if _, err := ensure(context.Background(), c, l); err != nil {
				t.Fatal(err)
			}
			key := l.Sandbox + "/anas-lease"
			if got := d.profiles[key].Config["security.nesting"]; got != scenario.want {
				t.Fatalf("VM profile security.nesting = %q, want %q", got, scenario.want)
			}
			d.profiles[key].Config["security.nesting"] = "true"
			if result, err := inspect(context.Background(), c, l); err != nil || result.Ready {
				t.Fatalf("drifted VM nesting reported ready: %+v %v", result, err)
			}
		})
	}
}
