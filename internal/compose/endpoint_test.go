package compose

import (
	"reflect"
	"strings"
	"testing"
)

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
