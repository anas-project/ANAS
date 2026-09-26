package computeingressruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
)

func leaseReaderFixture(t *testing.T) (*incusReaderFixture, IncusLeaseObservationScope, *IncusFactReader) {
	t.Helper()
	f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
	s := IncusLeaseObservationScope{Deployment: f.grant.Deployment, Consumer: f.grant.Consumer,
		Resource: f.grant.Resource, Provider: f.grant.Provider, Interface: f.grant.Interface,
		Project: f.grant.Project, InstancePrefix: f.grant.InstancePrefix, MaxInstances: 8,
		CredentialFingerprint: strings.Repeat("a", 64)}
	f.values["/1.0/certificates/"+s.CredentialFingerprint] = map[string]any{
		"fingerprint": s.CredentialFingerprint, "type": "client", "restricted": true, "projects": []string{s.Project}}
	config := f.config
	config.Authorizations = nil
	config.LeaseScopes = []IncusLeaseObservationScope{s}
	r, err := NewIncusFactReader(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.CloseIdleConnections)
	return f, s, r
}

func TestLeaseReaderDoesNotInventAnHTTPGrant(t *testing.T) {
	f, scope, reader := leaseReaderFixture(t)
	q := IncusInstanceObservationRequest{InstanceID: f.request.InstanceID, WorkloadID: f.request.WorkloadID}
	if _, err := reader.ObserveHostInstance(context.Background(), scope, q); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ObserveHTTP(context.Background(), f.grant, f.request); err == nil {
		t.Fatal("lease identity scope conferred HTTP publication authority")
	}
	if _, err := f.reader.ObserveHostInstance(context.Background(), scope, q); err == nil {
		t.Fatal("HTTP authority was silently reused as general lease authority")
	}
}

func TestLeaseReaderRequiresTheExactLiveRestrictedCredential(t *testing.T) {
	for _, change := range []string{"revoked", "unrestricted", "cross-project", "additional-project", "fingerprint", "type"} {
		t.Run(change, func(t *testing.T) {
			f, scope, reader := leaseReaderFixture(t)
			path := "/1.0/certificates/" + scope.CredentialFingerprint
			value := f.values[path].(map[string]any)
			switch change {
			case "revoked":
				delete(f.values, path)
			case "unrestricted":
				value["restricted"] = false
			case "cross-project":
				value["projects"] = []string{"anas-other"}
			case "additional-project":
				value["projects"] = []string{scope.Project, "anas-other"}
			case "fingerprint":
				value["fingerprint"] = strings.Repeat("b", 64)
			case "type":
				value["type"] = "server"
			}
			q := IncusInstanceObservationRequest{InstanceID: f.request.InstanceID, WorkloadID: f.request.WorkloadID}
			got, err := reader.ObserveHostInstance(context.Background(), scope, q)
			if err == nil || got != (IncusHostObservation{}) {
				t.Fatal("revoked, substituted or widened lease certificate authorized an identity")
			}
		})
	}
}

func TestLeaseReaderBindsAllScopeFieldsBeforeAccess(t *testing.T) {
	for _, change := range []string{"deployment", "consumer", "resource", "provider", "project", "prefix", "credential", "quota"} {
		t.Run(change, func(t *testing.T) {
			f, scope, reader := leaseReaderFixture(t)
			switch change {
			case "deployment":
				scope.Deployment = "deployment-two"
			case "consumer":
				scope.Consumer = "agent"
			case "resource":
				scope.Resource = "other"
			case "provider":
				scope.Provider = "other"
			case "project":
				scope.Project = "anas-other"
			case "prefix":
				scope.InstancePrefix = "anas-other-"
			case "credential":
				scope.CredentialFingerprint = strings.Repeat("b", 64)
			case "quota":
				scope.MaxInstances++
			}
			_, err := reader.ObserveHostInstance(context.Background(), scope, IncusInstanceObservationRequest{InstanceID: f.request.InstanceID, WorkloadID: f.request.WorkloadID})
			if err == nil || len(f.paths) != 0 {
				t.Fatal("changed lease authority reached Incus")
			}
		})
	}
}
