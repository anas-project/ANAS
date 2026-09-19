package incusprovision

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDockerClientRejectsUnconfirmedResponsesAndRedirects(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{202, `{}`}, {204, `{}`}, {404, `not found`}, {404, `{}`}, {404, `{"Message":"private-marker"}`},
		{500, `{"message":"private-marker"}`}, {200, `null`}, {200, `{"Id":"first","Id":"second"}`},
		{200, strings.Repeat(" ", maxIncusResponseBytes+1) + `{}`},
	} {
		calls := 0
		client := &dockerClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return incusTestResponse(tc.status, tc.body), nil
		})}
		var network dockerNetwork
		err := client.do(context.Background(), http.MethodGet, "/v1.44/networks/managed", nil, &network)
		if err == nil || errors.Is(err, errIncusNotFound) || strings.Contains(err.Error(), "private-marker") || calls != 1 {
			t.Fatalf("unconfirmed Docker observation accepted: %v", err)
		}
	}
	client := (&dockerClient{socket: dockerUnixSocket}).httpClient()
	defer client.CloseIdleConnections()
	if client.CheckRedirect == nil || client.CheckRedirect(&http.Request{}, nil) == nil {
		t.Fatal("root Docker client follows redirects")
	}
}

func TestDockerDeletionUsesImmutableIDAndRequiresAbsenceReadback(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, absent := range []bool{true, false} {
		deletes, reads := 0, 0
		client := &dockerClient{transport: incusRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1.44/networks/"+id {
				t.Fatal("deleted by reusable network name")
			}
			if r.Method == http.MethodDelete {
				deletes++
				return incusTestResponse(204, ""), nil
			}
			reads++
			if absent {
				return incusTestResponse(404, `{"message":"network not found"}`), nil
			}
			return incusTestResponse(200, `{"Id":"`+id+`"}`), nil
		})}
		err := client.deleteNetwork(context.Background(), id)
		if (err == nil) != absent || deletes != 1 || reads != 1 {
			t.Fatal("network deletion lacked exact readback", err)
		}
		if client.deleteNetwork(context.Background(), ControlNetworkName) == nil {
			t.Fatal("mutable network name accepted")
		}
	}
}

func TestDockerResponseCloseFailureCannotConfirmSuccess(t *testing.T) {
	client := &dockerClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
		r := incusTestResponse(200, `[]`)
		r.Body = incusCloseFailReader{Reader: strings.NewReader(`[]`)}
		return r, nil
	})}
	if _, err := client.listNetworkCIDRs(context.Background()); err == nil {
		t.Fatal("close failure hidden")
	}
}

func TestOwnedResourceDeletionRejectsReplacementAndChecksFinalState(t *testing.T) {
	ctx := context.Background()
	for _, present := range []bool{true, false} {
		deletes, reads := 0, 0
		client := &incusUnixClient{transport: incusRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodDelete {
				deletes++
				return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":{}}`), nil
			}
			if strings.Contains(r.URL.Path, "/volumes") {
				return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":[]}`), nil
			}
			reads++
			if reads > 1 && !present {
				return incusTestResponse(404, `{"type":"error","error_code":404}`), nil
			}
			return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":{"name":"anas-btrfs","driver":"btrfs","config":{"user.anas.owner":"incus-host-provision","user.anas.owner_id":"owner"}}}`), nil
		})}
		runtime := &localRuntime{incus: client}
		err := runtime.RemoveStoragePool(ctx, StoragePoolName, "owner")
		if (err == nil) == present || deletes != 1 || reads != 2 {
			t.Fatal("storage removal not read back", err)
		}
	}
	credential, err := generateCredential()
	if err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []bool{true, false} {
		deleted := false
		client := &incusUnixClient{transport: incusRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodDelete {
				deleted = true
				return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":{}}`), nil
			}
			if deleted {
				return incusTestResponse(404, `{"type":"error","error_code":404}`), nil
			}
			c := incusCertificate{Fingerprint: credential.Fingerprint, Certificate: credential.Certificate, Name: ManagementCertName, Type: "client"}
			if foreign {
				c.Name = "operator-certificate"
			}
			metadata, _ := json.Marshal(c)
			return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":`+string(metadata)+`}`), nil
		})}
		runtime := &localRuntime{incus: client}
		err := runtime.RemoveManagementCertificate(ctx, credential.Fingerprint)
		if (err == nil) == foreign || deleted == foreign {
			t.Fatal("unowned trust was removed or valid removal failed", err)
		}
	}
}

var _ io.ReadCloser = incusCloseFailReader{}
