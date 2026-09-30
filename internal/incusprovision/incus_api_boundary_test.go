package incusprovision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIncusEnvelopeRequiresMatchingTransportAndTerminalEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"empty envelope", 200, `{}`},
		{"missing metadata", 200, `{"type":"sync","status_code":200}`},
		{"null metadata", 200, `{"type":"sync","status_code":200,"metadata":null}`},
		{"wrong status", 200, `{"type":"sync","status_code":103,"metadata":{}}`},
		{"missing status", 200, `{"type":"sync","metadata":{}}`},
		{"body error over success", 200, `{"type":"error","error_code":404,"error":"private-marker"}`},
		{"body notfound over failure", 500, `{"type":"error","error_code":404}`},
		{"accepted is not complete", 202, `{"type":"sync","status_code":200,"metadata":{}}`},
		{"conflicting error", 200, `{"type":"sync","status_code":200,"error_code":500,"metadata":{}}`},
		{"duplicate status", 200, `{"type":"sync","status_code":400,"status_code":200,"metadata":{}}`},
		{"case alias", 200, `{"type":"sync","Status_Code":200,"metadata":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result map[string]any
			err := decodeIncusEnvelope(tc.status, []byte(tc.body), &result)
			if err == nil || errors.Is(err, errIncusNotFound) || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("unconfirmed response accepted or misclassified: %v", err)
			}
		})
	}
	var result map[string]any
	if err := decodeIncusEnvelope(200, []byte(`{"type":"sync","status_code":200,"metadata":{"future":true}}`), &result); err != nil {
		t.Fatal(err)
	}
	if err := decodeIncusEnvelope(404, []byte(`{"type":"error","error_code":404}`), nil); !errors.Is(err, errIncusNotFound) {
		t.Fatal(err)
	}
}

func TestPackageServiceAutostartStillRecordsExplicitManagedActivation(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	rt := newFakeRuntime(t)
	rt.observeHook = func(obs *Observation) {
		if obs.PackageInstalled {
			obs.IncusDaemonActive = true
		}
	}
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Install(ctx, Request{}, bind(plan, PhaseInstall)); err != nil {
		t.Fatal(err)
	}
	if !store.state.Ownership.IncusServiceByANAS || !hasReceipt(store.state, "install.service", "ok") || countCalls(rt.calls, "enable-incus") != 1 {
		t.Fatal("newly installed service was not explicitly enabled, read back and recorded as managed")
	}
	plan, err = backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Configure(ctx, Request{}, bind(plan, PhaseConfigure)); err != nil {
		t.Fatalf("autostart was misclassified as external: %v", err)
	}
	if store.state.Ownership.ExternalDaemonPreserved {
		t.Fatal("owned installation became an external daemon")
	}
}

func TestUnixIncusTransportDoesNotFollowRedirects(t *testing.T) {
	client := (&incusUnixClient{socket: incusUnixSocket}).httpClient()
	defer client.CloseIdleConnections()
	if client.CheckRedirect == nil || client.CheckRedirect(&http.Request{}, nil) == nil {
		t.Fatal("privileged API client follows a daemon-controlled redirect")
	}
}

type incusRoundTripFunc func(*http.Request) (*http.Response, error)

func (f incusRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func incusTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

const testOperationID = "e372a02e-d41b-4a3c-9d3f-99a401e805ec"
const testOperationURL = "/1.0/operations/" + testOperationID

func TestIncusAsyncWaitBindsExactOperationAndDoesNotResubmit(t *testing.T) {
	for _, code := range []int{200, 299, 300, 400, 401} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			posts, polls := 0, 0
			c := &incusUnixClient{transport: incusRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost {
					posts++
					return incusTestResponse(202, `{"type":"async","status_code":100,"operation":"`+testOperationURL+`","metadata":{}}`), nil
				}
				if r.Method != http.MethodGet || r.URL.RequestURI() != testOperationURL+"/wait?timeout=30" {
					t.Fatal("operation escaped fixed wait endpoint")
				}
				polls++
				status := code
				if polls == 1 {
					status = 103
				}
				return incusTestResponse(200, fmt.Sprintf(`{"type":"sync","status_code":200,"metadata":{"id":%q,"status_code":%d,"err":""}}`, testOperationID, status)), nil
			})}
			err := c.do(context.Background(), http.MethodPost, "/1.0/storage-pools", map[string]string{"name": StoragePoolName}, nil)
			if (err == nil) != (code == 200) || posts != 1 || polls != 2 {
				t.Fatalf("incorrect terminal evidence: err=%v posts=%d polls=%d", err, posts, polls)
			}
		})
	}
}

func TestIncusAsyncRejectsExternalOrAmbiguousOperationAndReadActivation(t *testing.T) {
	for _, operation := range []string{
		"https://other.invalid" + testOperationURL, "//other.invalid" + testOperationURL,
		testOperationURL + "?secret=private-marker", testOperationURL + "#fragment", testOperationURL + "/wait",
		"/1.0/operations/../certificates", "/1.0/operations/%2e%2e/certificates", "/1.0/operations/not-an-id",
	} {
		requests := 0
		c := &incusUnixClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return incusTestResponse(202, fmt.Sprintf(`{"type":"async","status_code":100,"operation":%q}`, operation)), nil
		})}
		if err := c.do(context.Background(), http.MethodPost, "/1.0/storage-pools", nil, nil); err == nil || requests != 1 || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("untrusted operation URL accepted")
		}
	}
	for _, tc := range []struct {
		method string
		status int
		out    any
	}{
		{http.MethodGet, 202, nil}, {http.MethodPost, 200, nil}, {http.MethodPost, 500, nil}, {http.MethodPost, 202, &incusServer{}},
	} {
		requests := 0
		c := &incusUnixClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return incusTestResponse(tc.status, `{"type":"async","status_code":100,"operation":"`+testOperationURL+`"}`), nil
		})}
		if err := c.do(context.Background(), tc.method, "/1.0", nil, tc.out); err == nil || requests != 1 {
			t.Fatal("ambiguous async response triggered a wait")
		}
	}
}

func TestIncusWaitRequiresMatchingOperationIDAndHandlesCancellation(t *testing.T) {
	for _, metadata := range []string{
		`{"status_code":200}`, `{"id":"different","status_code":200}`,
		`{"id":"` + testOperationID + `","status_code":200,"err":"private-marker"}`,
	} {
		c := &incusUnixClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":`+metadata+`}`), nil
		})}
		if err := c.waitOperation(context.Background(), testOperationURL); err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("missing or foreign operation evidence accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &incusUnixClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":{"id":"`+testOperationID+`","status_code":103}}`), nil
	})}
	defer cancel()
	if err := c.waitOperation(ctx, testOperationURL); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled operation wait returned %v", err)
	}
}

type incusCloseFailReader struct{ io.Reader }

func (incusCloseFailReader) Close() error { return errors.New("private-close-marker") }

func TestUnixIncusResponseSizeCloseAndRedirectBoundaries(t *testing.T) {
	for _, kind := range []string{"oversize", "oversize declared", "close", "redirect", "transport", "invalid utf8", "depth"} {
		t.Run(kind, func(t *testing.T) {
			requests := 0
			c := &incusUnixClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
				requests++
				r := incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":{}}`)
				switch kind {
				case "oversize":
					r.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", maxIncusResponseBytes+1)))
					r.ContentLength = -1
				case "oversize declared":
					r.ContentLength = maxIncusResponseBytes + 1
				case "close":
					r.Body = incusCloseFailReader{Reader: strings.NewReader(`{"type":"sync","status_code":200,"metadata":{}}`)}
				case "redirect":
					r.StatusCode = 307
					r.Header.Set("Location", "http://other.invalid/private-marker")
				case "transport":
					return nil, errors.New("private-transport-marker")
				case "invalid utf8":
					r = incusTestResponse(200, "{\"type\":\"sync\",\"status_code\":200,\"metadata\":{\"value\":\"\xff\"}}")
				case "depth":
					r = incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":{"value":`+strings.Repeat("[", 66)+"0"+strings.Repeat("]", 66)+"}}")
				}
				return r, nil
			})}
			var out map[string]any
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := c.do(ctx, http.MethodGet, "/1.0", nil, &out); err == nil || requests != 1 || strings.Contains(err.Error(), "private-") {
				t.Fatalf("response boundary %s failed: %v requests=%d", kind, err, requests)
			}
		})
	}
}

func TestSkipCannotApproveAHiddenConfigureEnrollOrUninstallEffect(t *testing.T) {
	for _, phase := range []Phase{PhaseConfigure, PhaseEnroll, PhaseUninstall} {
		t.Run(string(phase), func(t *testing.T) {
			ctx := context.Background()
			store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{ID: "owned", PackagesInstalledByANAS: true, IncusServiceByANAS: true, ManagementTrust: strings.Repeat("a", 64), ControlListener: true}}}
			rt := newFakeRuntime(t)
			backend := newBackendForTest(store, rt)
			plan, err := backend.Plan(ctx, Request{Skip: true})
			if err != nil {
				t.Fatal(err)
			}
			var run func(context.Context, Request, Binding) (ApplyResult, error)
			switch phase {
			case PhaseConfigure:
				run = backend.Configure
			case PhaseEnroll:
				run = backend.Enroll
			case PhaseUninstall:
				run = backend.Uninstall
			}
			if _, err := run(ctx, Request{Skip: true}, bind(plan, phase)); err == nil || len(rt.calls) != 0 || store.saves != 0 {
				t.Fatalf("skip plan authorized a hidden %s effect: err=%v calls=%v saves=%d", phase, err, rt.calls, store.saves)
			}
		})
	}
}

func TestManagementTrustReadbackChecksActualCertificateAndAuthority(t *testing.T) {
	credential, err := generateCredential()
	if err != nil {
		t.Fatal(err)
	}
	valid := incusCertificate{Name: ManagementCertName, Fingerprint: credential.Fingerprint, Certificate: credential.Certificate, Type: "client"}
	if err := validateManagementCertificate(valid, credential.Fingerprint); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*incusCertificate){
		"name":                func(c *incusCertificate) { c.Name = "external" },
		"restricted":          func(c *incusCertificate) { c.Restricted = true },
		"projects":            func(c *incusCertificate) { c.Projects = []string{"default"} },
		"metrics":             func(c *incusCertificate) { c.Type = "metrics" },
		"missing identity":    func(c *incusCertificate) { c.Fingerprint = "" },
		"missing certificate": func(c *incusCertificate) { c.Certificate = "" },
		"wrong bytes": func(c *incusCertificate) {
			other, e := generateCredential()
			if e != nil {
				t.Fatal(e)
			}
			c.Certificate = other.Certificate
		},
		"trailing certificate": func(c *incusCertificate) { c.Certificate += credential.Certificate },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if validateManagementCertificate(candidate, credential.Fingerprint) == nil {
				t.Fatal("unverified trust accepted")
			}
		})
	}
}
