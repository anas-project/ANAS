package computeingressruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestObservedJSONExactSelectedFieldsAndExtensibleMaps(t *testing.T) {
	type child struct {
		State string `json:"state"`
	}
	type document struct {
		Auth     string           `json:"auth"`
		Children []child          `json:"children"`
		Names    map[string]child `json:"names"`
		Raw      json.RawMessage  `json:"raw"`
	}
	for _, body := range []string{
		`{"auth":"trusted","Auth":"untrusted"}`,
		`{"Auth":"trusted"}`,
		`{"children":[{"STATE":"Running"}]}`,
		`{"names":{"MixedCaseName":{"State":"Running"}}}`,
		`{"raw":{"a":1,"a":2}}`,
		`{"auth":"a","auth":"b"}`,
		`{"auth":"a"} {}`,
		"{\"auth\":\"\xff\"}",
	} {
		var out document
		if decodeObservedJSON([]byte(body), &out) == nil {
			t.Fatalf("accepted noncanonical selected field or invalid JSON: %q", body)
		}
	}
	var out document
	if err := decodeObservedJSON([]byte(`{"auth":"trusted","future":{"other":true},"children":[{"state":"Running","extra":null}],"names":{"A":{"state":"Running"},"a":{"state":"Stopped"}},"raw":{"AnyKey":1}}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Auth != "trusted" || out.Names["A"].State != "Running" || out.Names["a"].State != "Stopped" {
		t.Fatal("case-sensitive API map data was normalized")
	}
}

type readerRoundTripper func(*http.Request) (*http.Response, error)

func (f readerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPinnedReaderChecksValidityAndCancellationAfterResponse(t *testing.T) {
	for _, scenario := range []string{"expiry", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &pinnedGETClient{origin: "https://example.test", validFrom: time.Now().Add(-time.Hour), validUntil: time.Now().Add(time.Hour)}
			client.http = &http.Client{Transport: readerRoundTripper(func(*http.Request) (*http.Response, error) {
				if scenario == "expiry" {
					client.validUntil = time.Now().Add(-time.Second)
				} else {
					cancel()
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})}
			body, err := client.get(ctx, "/1.0", 1024)
			if err == nil || body != nil {
				t.Fatal("response outlived its validity boundary")
			}
			if scenario == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation category lost: %v", err)
			}
		})
	}
}
