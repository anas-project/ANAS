package computeclient

import (
	"context"
	"testing"
)

// INCUS-R-146: stopping or deleting an instance withdraws its publications
// first; other instances keep theirs.
func TestStopAndDeleteWithdrawTheInstancesPublications(t *testing.T) {
	for _, action := range []string{"stop", "delete"} {
		t.Run(action, func(t *testing.T) {
			publisher, run, writer := httpTestPublisher(t, "named")
			publisher.client.publisher = publisher
			httpSetInstances(t, run, httpTestInstance("anas-fj-job1", "job:1", "Running"), httpTestInstance("anas-fj-job2", "job:2", "Running"))
			if _, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{Label: "web"}); err != nil {
				t.Fatal(err)
			}
			if _, err := publisher.PublishPort(context.Background(), "anas-fj-job2", 7000, PublishOptions{Label: "api"}); err != nil {
				t.Fatal(err)
			}
			var err error
			if action == "stop" {
				err = publisher.client.Stop(context.Background(), "anas-fj-job1")
			} else {
				err = publisher.client.Delete(context.Background(), "anas-fj-job1")
			}
			if err != nil && action == "stop" {
				t.Fatal(err)
			}
			if len(writer.records) != 1 {
				t.Fatalf("records after %s = %+v", action, writer.records)
			}
			for _, record := range writer.records {
				if record.request.Instance != "anas-fj-job2" {
					t.Fatalf("%s withdrew the wrong instance's publication", action)
				}
			}
		})
	}
}

// INCUS-R-146: the janitor removes requests whose instance is gone, stopped
// or moved to another address, including ones an earlier process wrote.
func TestJanitorPrunesOrphanedRequests(t *testing.T) {
	publisher, run, writer := httpTestPublisher(t, "named")
	httpSetInstances(t, run, httpTestInstance("anas-fj-job1", "job:1", "Running"), httpTestInstance("anas-fj-job2", "job:2", "Running"))
	if _, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{Label: "web"}); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.PublishPort(context.Background(), "anas-fj-job2", 7000, PublishOptions{Label: "api"}); err != nil {
		t.Fatal(err)
	}
	httpSetInstances(t, run, httpTestInstance("anas-fj-job2", "job:2", "Running"))
	removed, err := publisher.PruneOrphans(context.Background())
	if err != nil || removed != 1 || len(writer.records) != 1 {
		t.Fatalf("pruned %d, records %+v, %v", removed, writer.records, err)
	}
	if len(publisher.active) != 1 {
		t.Fatalf("the publisher still tracks a pruned publication: %d", len(publisher.active))
	}
}

func TestInstanceListCarriesTheLeaseAddress(t *testing.T) {
	client, run := testClient(t, testLease())
	httpSetInstances(t, run, httpTestInstance("anas-fj-job1", "job:1", "Running"))
	instance, err := client.Inspect(context.Background(), "anas-fj-job1")
	if err != nil || instance.IPv4 != "10.101.0.17" {
		t.Fatalf("instance = %+v, %v", instance, err)
	}
}
