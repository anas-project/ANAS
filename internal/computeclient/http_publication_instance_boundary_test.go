package computeclient

import (
	"context"
	"errors"
	"testing"
)

func TestHTTPPublicationDoesNotTrustUnmanagedOrFuzzyInstance(t *testing.T) {
	for _, name := range []string{"unmanaged", "different-filter-result"} {
		t.Run(name, func(t *testing.T) {
			publisher, run, writer := httpTestPublisher(t, "random")
			instance := httpTestInstance("anas-fj-job1", "job:1", "Running")
			if name == "unmanaged" {
				instance["config"].(map[string]string)["user.anas.managed"] = "false"
			} else {
				instance["name"] = "anas-fj-job10"
			}
			httpSetInstances(t, run, instance)
			if _, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{}); !errors.Is(err, ErrHTTPPublicationInstance) {
				t.Fatalf("unconfirmed managed instance was accepted: %v", err)
			}
			if len(writer.submitted) != 0 {
				t.Fatal("unconfirmed identity reached the writer")
			}
		})
	}
}

func TestHTTPPublicationValidNamedLabelRemainsInProjectedNamespace(t *testing.T) {
	publisher, _, writer := httpTestPublisher(t, "named")
	publication, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{Label: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if publication.RequestedURL() != "https://ci-api.example.test" || len(writer.submitted) != 1 || writer.submitted[0].Label != "api" {
		t.Fatal("named request escaped or changed its projected namespace")
	}
}

func TestHTTPPublicationRejectsZeroValueWithoutAccessingFilesystem(t *testing.T) {
	var nilPublisher *HTTPPublisher
	var zeroPublisher HTTPPublisher
	var nilClient *Client
	ctx := context.Background()
	for _, publisher := range []*HTTPPublisher{nilPublisher, &zeroPublisher} {
		if _, err := publisher.PublishPort(ctx, "anas-fj-job1", 7000, PublishOptions{}); !errors.Is(err, ErrHTTPPublicationUnavailable) {
			t.Fatalf("uninitialized publisher error = %v", err)
		}
		if err := publisher.UnpublishPort(ctx, nil); !errors.Is(err, ErrHTTPPublicationStale) {
			t.Fatalf("missing receipt error = %v", err)
		}
	}
	if _, err := nilClient.OpenHTTPPublisher(httpTestConfig("random")); !errors.Is(err, ErrHTTPPublicationUnavailable) {
		t.Fatalf("nil client opened a publisher: %v", err)
	}
	if err := nilPublisher.Close(); err != nil {
		t.Fatal(err)
	}
	var receipt *HTTPPublication
	if receipt.RequestedURL() != "" {
		t.Fatal("nil receipt predicted a URL")
	}
}
