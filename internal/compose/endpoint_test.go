package compose

import (
	"reflect"
	"strings"
	"testing"
)

// TEST_CASES: TEMP-T-003

func TestTemporaryMountEnvironmentDoesNotInheritOtherModules(t *testing.T) {
	c := CLI{endpoint: freezeEndpoint([]string{"HOME=/home/operator"})}
	actual := strings.Join(c.Environment([]string{"ANAS_TEMP_OTHER=/private/other", "ANAS_TEMP_DOCUMENTS=/stale"}, map[string]string{"ANAS_TEMP_DOCUMENTS": "/owned/documents"}), "\n")
	if strings.Contains(actual, "ANAS_TEMP_OTHER=") || strings.Contains(actual, "/stale") || !strings.Contains(actual, "ANAS_TEMP_DOCUMENTS=/owned/documents") {
		t.Fatalf("inherited another module's temporary mount: %s", actual)
	}
}

func TestEndpointSelectionIsFrozenAndRejectsDeploymentOverrides(t *testing.T) {
	for _, base := range [][]string{{"HOME=/home/operator"}, {"HOME=/home/operator", "DOCKER_HOST=ssh://host"}, {"DOCKER_CONTEXT=rootless", "DOCKER_CONFIG=/private/config"}} {
		c := CLI{endpoint: freezeEndpoint(base)}
		actual := c.Environment([]string{"DOCKER_HOST=tcp://wrong", "HOME=/wrong", "MODULE_KEY=old"}, map[string]string{"DOCKER_HOST": "tcp://attacker", "DOCKER_CONTEXT": "attacker", "DOCKER_CONFIG": "/attacker", "MODULE_KEY": "new"})
		expected := c.Environment(base, map[string]string{"MODULE_KEY": "new"})
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("changed endpoint: %v != %v", actual, expected)
		}
		if !strings.Contains(strings.Join(actual, "\n"), "MODULE_KEY=new") {
			t.Fatal("lost business value")
		}
	}
}
