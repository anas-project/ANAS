package main

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

type recordedCommand struct {
	Args  []string
	Stdin string
}

type fakeCommandExecutor struct {
	Responses [][]byte
	Commands  []recordedCommand
}

func (f *fakeCommandExecutor) Run(_ context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	input := ""
	if stdin != nil {
		body, _ := io.ReadAll(stdin)
		input = string(body)
	}
	f.Commands = append(f.Commands, recordedCommand{Args: append([]string{}, args...), Stdin: input})
	if len(f.Responses) == 0 {
		return nil, nil
	}
	response := f.Responses[0]
	f.Responses = f.Responses[1:]
	return response, nil
}

func TestIncusProviderVerifiesRestrictedProjectAndQuotas(t *testing.T) {
	run := &fakeCommandExecutor{Responses: [][]byte{[]byte(`{"config":{"restricted":"true","limits.instances":"8","limits.cpu":"16","limits.memory":"32GiB","limits.disk":"200GiB"}}`)}}
	p := &IncusProvider{run: run}
	if err := p.verifyRestrictedProject(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"project", "show", "anas-actions:anas-forgejo-runners", "--format=json"}
	if !reflect.DeepEqual(run.Commands[0].Args, want) {
		t.Fatalf("project inspection args = %#v, want %#v", run.Commands[0].Args, want)
	}

	for _, body := range []string{
		`{"config":{"restricted":"false","limits.instances":"8","limits.cpu":"16","limits.memory":"32GiB","limits.disk":"200GiB"}}`,
		`{"config":{"restricted":"true","limits.instances":"8","limits.cpu":"16","limits.memory":"32GiB"}}`,
	} {
		p.run = &fakeCommandExecutor{Responses: [][]byte{[]byte(body)}}
		if err := p.verifyRestrictedProject(context.Background()); err == nil {
			t.Fatalf("accepted unsafe project policy %s", body)
		}
	}
}

func TestIncusProviderVerifiesIsolatedRunnerProfile(t *testing.T) {
	safe := `{"config":{"user.anas.egress":"restricted"},"devices":{"root":{"type":"disk","path":"/","pool":"runners"},"eth0":{"type":"nic","network":"anas-forgejo-runners"}}}`
	run := &fakeCommandExecutor{Responses: [][]byte{[]byte(safe)}}
	p := &IncusProvider{cfg: IncusConfig{Profile: "anas-forgejo-runner"}, run: run}
	if err := p.verifyRunnerProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{
		`{"config":{"user.anas.egress":"open"},"devices":{"eth0":{"type":"nic","network":"anas-forgejo-runners"}}}`,
		`{"config":{"user.anas.egress":"restricted","cloud-init.user-data":"secret"},"devices":{"eth0":{"type":"nic","network":"anas-forgejo-runners"}}}`,
		`{"config":{"user.anas.egress":"restricted"},"devices":{"host":{"type":"disk","source":"/","path":"/host"},"eth0":{"type":"nic","network":"anas-forgejo-runners"}}}`,
		`{"config":{"user.anas.egress":"restricted"},"devices":{"eth0":{"type":"nic","parent":"eth0","nictype":"physical"}}}`,
	} {
		p.run = &fakeCommandExecutor{Responses: [][]byte{[]byte(unsafe)}}
		if err := p.verifyRunnerProfile(context.Background()); err == nil {
			t.Fatalf("accepted unsafe Runner profile %s", unsafe)
		}
	}
}

func TestIncusProviderCreateHasFixedVMAndNoArbitraryDevices(t *testing.T) {
	run := &fakeCommandExecutor{}
	p := &IncusProvider{cfg: IncusConfig{Profile: "anas-forgejo-runner"}, run: run}
	spec := InstanceSpec{
		ID: "anas-fj-0123456789abcdef0123", Image: strings.Repeat("a", 64), WorkloadID: "handle-1",
		CPU: 2, MemoryMiB: 4096, DiskGiB: 20,
	}
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(run.Commands[0].Args, " ")
	for _, want := range []string{
		"init anas-actions:" + spec.Image, "anas-actions:" + spec.ID, "--vm",
		"--profile=anas-forgejo-runner", "--config=limits.cpu=2", "--config=limits.memory=4096MiB",
		"--config=security.secureboot=true", "--config=user.anas.managed=true", "--device=root,size=20GiB",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("create args %q do not contain %q", joined, want)
		}
	}
	for _, forbidden := range []string{"host", "unix-char", "proxy", "source=/", "raw.qemu"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("create args contain forbidden value %q: %s", forbidden, joined)
		}
	}
}

func TestIncusProviderStartWaitsForFixedGuestEntrypoint(t *testing.T) {
	run := &fakeCommandExecutor{}
	p := &IncusProvider{run: run}
	if err := p.Start(context.Background(), "anas-fj-0123456789abcdef0123"); err != nil {
		t.Fatal(err)
	}
	if len(run.Commands) != 2 {
		t.Fatalf("start commands = %#v", run.Commands)
	}
	want := []string{"exec", "anas-actions:anas-fj-0123456789abcdef0123", "--", "/usr/bin/test", "-x", "/usr/local/libexec/anas-forgejo-runner-start"}
	if !reflect.DeepEqual(run.Commands[1].Args, want) {
		t.Fatalf("guest readiness command = %#v, want %#v", run.Commands[1].Args, want)
	}
}

func TestIncusProviderDeleteIsIdempotent(t *testing.T) {
	run := &fakeCommandExecutor{Responses: [][]byte{[]byte(`[]`)}}
	p := &IncusProvider{run: run}
	if err := p.Delete(context.Background(), "anas-fj-0123456789abcdef0123"); err != nil {
		t.Fatal(err)
	}
	if len(run.Commands) != 1 || run.Commands[0].Args[0] != "list" {
		t.Fatalf("missing VM delete commands = %#v", run.Commands)
	}
}

func TestIncusExecCarriesRunnerTokenOnlyOnStdin(t *testing.T) {
	run := &fakeCommandExecutor{}
	p := &IncusProvider{run: run}
	secret := "0123456789abcdef0123456789abcdef01234567"
	command := []string{
		"/usr/local/libexec/anas-forgejo-runner-start", "--url", "https://git.example.test/",
		"--uuid", "runner-uuid", "--handle", "job-handle", "--label", "docker:docker://node:24",
	}
	if err := p.ExecStdin(context.Background(), "anas-fj-0123456789abcdef0123", command, bytes.NewBufferString(secret)); err != nil {
		t.Fatal(err)
	}
	if run.Commands[0].Stdin != secret {
		t.Fatal("runner token was not passed intact through stdin")
	}
	if strings.Contains(strings.Join(run.Commands[0].Args, " "), secret) {
		t.Fatal("runner token leaked into Incus argv")
	}
	if err := p.ExecStdin(context.Background(), "anas-fj-0123456789abcdef0123", []string{"sh", "-c", "id"}, bytes.NewBufferString(secret)); err == nil {
		t.Fatal("provider accepted an arbitrary guest command")
	}
}

func TestIncusListManagedFiltersForeignInstances(t *testing.T) {
	body := `[
 {"name":"anas-fj-0123456789abcdef0123","status":"Running","config":{"user.anas.managed":"true"}},
 {"name":"foreign","status":"Running","config":{"user.anas.managed":"true"}},
 {"name":"anas-fj-abcdef0123456789abcd","status":"Stopped","config":{}}
]`
	run := &fakeCommandExecutor{Responses: [][]byte{[]byte(body)}}
	p := &IncusProvider{run: run}
	instances, err := p.ListManaged(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ID != "anas-fj-0123456789abcdef0123" {
		t.Fatalf("managed instances = %#v", instances)
	}
}
