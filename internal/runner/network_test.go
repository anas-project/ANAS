package runner

import (
	"reflect"
	"testing"
)

func TestNetworkScriptArgsOnHost(t *testing.T) {
	got, err := networkScriptArgs("", "/tmp/anas_service.sh", "del")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sh", "/tmp/anas_service.sh", "del"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("networkScriptArgs() = %#v, want %#v", got, want)
	}
}

func TestNetworkScriptArgsInNamespace(t *testing.T) {
	got, err := networkScriptArgs("/run/netns/anas-test", "/tmp/anas_service.sh")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nsenter", "--net=/run/netns/anas-test", "sh", "/tmp/anas_service.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("networkScriptArgs() = %#v, want %#v", got, want)
	}
}

func TestNetworkScriptArgsRejectsRelativeNamespace(t *testing.T) {
	if _, err := networkScriptArgs("run/netns/anas-test", "/tmp/anas_service.sh"); err == nil {
		t.Fatal("expected relative network namespace path to be rejected")
	}
}

func TestHostLANRequired(t *testing.T) {
	a := &app{
		order: []string{"core", "storage"},
		reg: map[string]Module{
			"core":    {Name: "core"},
			"storage": {Name: "storage", UseHostLAN: "required"},
		},
	}
	if !a.hostLANRequired() {
		t.Fatal("expected required host LAN to be detected")
	}
}
