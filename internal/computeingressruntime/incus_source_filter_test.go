package computeingressruntime

import (
	"context"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingress"
)

func TestIncusReaderRejectsLiveSourceFilterOverrides(t *testing.T) {
	for _, key := range []string{"security.mac_filtering", "security.ipv4_filtering", "security.ipv6_filtering"} {
		for _, value := range []string{"", "false", "TRUE"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
				instance := f.values["/1.0/instances/anas-fj-job1?project=anas-runners"].(map[string]any)
				nic := instance["expanded_devices"].(map[string]map[string]string)["eth0"]
				for _, filter := range []string{"security.mac_filtering", "security.ipv4_filtering", "security.ipv6_filtering"} {
					nic[filter] = "true"
				}
				if value == "" {
					delete(nic, key)
				} else {
					nic[key] = value
				}
				facts, err := f.reader.ObserveHTTP(context.Background(), f.grant, f.request)
				if err == nil || facts != (computeingress.Facts{}) {
					t.Fatal("live NIC source-filter override accepted as a fenced identity")
				}
			})
		}
	}
}
