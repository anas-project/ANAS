package computeclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
)

type httpTestRecord struct {
	request computeingress.Request
	receipt *computeingress.RequestReceipt
}

type httpTestWriter struct {
	records       map[string]httpTestRecord
	submitted     []computeingress.Request
	withdrawCalls int
	replace       bool
	nilReceipt    bool
	submitError   error
	withdrawError error
	closed        bool
}

func (w *httpTestWriter) Submit(ctx context.Context, request computeingress.Request) (*computeingress.RequestReceipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.submitted = append(w.submitted, request)
	if w.submitError != nil || w.nilReceipt {
		return nil, w.submitError
	}
	key := request.InstanceID + ":" + strconv.Itoa(int(request.GuestPort))
	if previous, found := w.records[key]; found {
		if previous.request != request {
			return nil, computeingress.ErrRequestConflict
		}
		if !w.replace {
			return previous.receipt, nil
		}
	}
	receipt := &computeingress.RequestReceipt{}
	w.records[key] = httpTestRecord{request: request, receipt: receipt}
	w.replace = false
	return receipt, nil
}

func (w *httpTestWriter) Withdraw(ctx context.Context, receipt *computeingress.RequestReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.withdrawCalls++
	if w.withdrawError != nil {
		return w.withdrawError
	}
	for key, record := range w.records {
		if record.receipt == receipt {
			delete(w.records, key)
			return nil
		}
	}
	return computeingress.ErrRequestReceiptStale
}

func (w *httpTestWriter) Close() error {
	w.closed = true
	return nil
}

func httpTestConfig(mode string) HTTPPublicationConfig {
	lease := testLease()
	key := ""
	if mode == "random" {
		key = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	}
	return HTTPPublicationConfig{
		Interface: lease.Interface, Project: lease.Sandbox, InstancePrefix: lease.InstancePrefix,
		BaseDomain:       "example.test",
		Policy:           computeingress.Policy{AllowedPorts: []uint16{7000, 7001}, Auth: "none", Domain: computeingress.Domain{Mode: mode, Prefix: "ci"}},
		RequestDirectory: "/not-opened-by-unit-tests", LeaseSecret: key,
	}
}

func httpTestInstance(id, workload, state string) map[string]any {
	return map[string]any{
		"name": id, "status": state,
		"config": map[string]string{"user.anas.managed": "true", "user.anas.workload": workload},
	}
}

func httpSetInstances(t *testing.T, run *fakeRunner, instances ...map[string]any) {
	t.Helper()
	body, err := json.Marshal(instances)
	if err != nil {
		t.Fatal(err)
	}
	run.reply["list"] = body
}

func httpTestPublisher(t *testing.T, mode string) (*HTTPPublisher, *fakeRunner, *httpTestWriter) {
	t.Helper()
	client, run := testClient(t, testLease())
	config := httpTestConfig(mode)
	authorization, err := validateHTTPPublicationConfig(client, config)
	if err != nil {
		t.Fatal(err)
	}
	writer := &httpTestWriter{records: make(map[string]httpTestRecord)}
	publisher := newHTTPPublisher(client, authorization, writer)
	httpSetInstances(t, run, httpTestInstance("anas-fj-job1", "job:1", "Running"))
	t.Cleanup(func() { _ = publisher.Close() })
	return publisher, run, writer
}

func TestInspectSelectsExactManagedInstanceForHTTP(t *testing.T) {
	client, run := testClient(t, testLease())
	httpSetInstances(t, run,
		httpTestInstance("anas-fj-job10", "job:10", "Running"),
		httpTestInstance("anas-fj-job1", "job:1", "Running"),
	)
	instance, err := client.Inspect(context.Background(), "anas-fj-job1")
	if err != nil || instance.ID != "anas-fj-job1" || instance.WorkloadID != "job:1" {
		t.Fatalf("exact instance = %+v, error = %v", instance, err)
	}
	httpSetInstances(t, run, httpTestInstance("anas-fj-job10", "job:10", "Running"))
	instance, err = client.Inspect(context.Background(), "anas-fj-job1")
	if err != nil || instance.State != "missing" || instance.ID != "anas-fj-job1" {
		t.Fatalf("fuzzy-only result = %+v, error = %v", instance, err)
	}
	httpSetInstances(t, run,
		httpTestInstance("anas-fj-job1", "job:1", "Running"),
		httpTestInstance("anas-fj-job1", "job:2", "Running"),
	)
	if _, err := client.Inspect(context.Background(), "anas-fj-job1"); err == nil {
		t.Fatal("duplicate exact identity was accepted")
	}
}

func TestHTTPPublicationConfigClonesAndValidatesLeaseProjection(t *testing.T) {
	client, _ := testClient(t, testLease())
	config := httpTestConfig("random")
	authorization, err := validateHTTPPublicationConfig(client, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Policy.AllowedPorts[0] = 9000
	if authorization.Policy.AllowedPorts[0] != 7000 {
		t.Fatal("publisher retained caller's mutable authorization slice")
	}
	for name, mutate := range map[string]func(*HTTPPublicationConfig){
		"missing policy":    func(c *HTTPPublicationConfig) { c.Policy = computeingress.Policy{} },
		"another project":   func(c *HTTPPublicationConfig) { c.Project = "anas-other" },
		"another interface": func(c *HTTPPublicationConfig) { c.Interface = InterfaceContainer },
		"another prefix":    func(c *HTTPPublicationConfig) { c.InstancePrefix = "anas-other-" },
		"missing key":       func(c *HTTPPublicationConfig) { c.LeaseSecret = "" },
		"noncanonical key":  func(c *HTTPPublicationConfig) { c.LeaseSecret += "\n" },
		"unsorted ports":    func(c *HTTPPublicationConfig) { c.Policy.AllowedPorts = []uint16{7001, 7000} },
		"unknown auth":      func(c *HTTPPublicationConfig) { c.Policy.Auth = "injected" },
		"invalid domain":    func(c *HTTPPublicationConfig) { c.BaseDomain = "https://example.test" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := httpTestConfig("random")
			mutate(&candidate)
			if _, err := validateHTTPPublicationConfig(client, candidate); !errors.Is(err, ErrHTTPPublicationPolicy) {
				t.Fatalf("projection accepted or unexpected error: %v", err)
			}
		})
	}
	for _, mode := range []string{"fixed", "named"} {
		candidate := httpTestConfig(mode)
		if mode == "named" {
			candidate.Policy.Domain.Prefix = strings.Repeat("a", 61)
		}
		if _, err := validateHTTPPublicationConfig(client, candidate); err != nil {
			t.Fatalf("valid %s policy rejected: %v", mode, err)
		}
		candidate.LeaseSecret = httpTestConfig("random").LeaseSecret
		if _, err := validateHTTPPublicationConfig(client, candidate); !errors.Is(err, ErrHTTPPublicationPolicy) {
			t.Fatalf("unnecessary key accepted for %s", mode)
		}
	}
	private := httpTestConfig("random")
	for _, formatted := range []string{fmt.Sprintf("%v", private), fmt.Sprintf("%+v", private), fmt.Sprintf("%#v", private)} {
		if strings.Contains(formatted, private.LeaseSecret) {
			t.Fatal("configuration formatting disclosed the naming key")
		}
	}
}

func TestHTTPPublicationSubmitsSmallRequestAndReusesReceipt(t *testing.T) {
	publisher, _, writer := httpTestPublisher(t, "random")
	first, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{})
	if err != nil || first != second || len(writer.records) != 1 {
		t.Fatalf("retry did not reuse receipt: %v", err)
	}
	host, err := publisher.policy.Host(publisher.baseDomain, "job:1", "", publisher.key)
	if err != nil || first.RequestedURL() != "https://"+host {
		t.Fatal("URL differs from the shared deterministic naming policy")
	}
	body, err := json.Marshal(writer.submitted[0])
	if err != nil {
		t.Fatal(err)
	}
	request, err := computeingress.ParseRequest(body)
	if err != nil || request.InstanceID != "anas-fj-job1" || request.WorkloadID != "job:1" || request.GuestPort != 7000 || request.Action != "publish" {
		t.Fatalf("request does not match the mediator schema: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || len(fields) != 4 {
		t.Fatal("request contains fields outside the no-label schema")
	}
	for _, name := range []string{"host", "url", "auth", "lease_secret", "project", "target_ip", "entrypoint"} {
		if _, found := fields[name]; found {
			t.Fatalf("request contains forbidden field %q", name)
		}
	}
}

func TestHTTPPublicationRejectsWrongStatePortLabelAndWorkload(t *testing.T) {
	for _, tc := range []struct {
		name, mode, id, workload, state, label string
		port                                   uint16
	}{
		{name: "stopped", mode: "random", id: "anas-fj-job1", workload: "job:1", state: "Stopped", port: 7000},
		{name: "missing workload", mode: "random", id: "anas-fj-job1", state: "Running", port: 7000},
		{name: "invalid workload", mode: "random", id: "anas-fj-job1", workload: "../job", state: "Running", port: 7000},
		{name: "other lease", mode: "random", id: "anas-other-job1", workload: "job:1", state: "Running", port: 7000},
		{name: "unapproved port", mode: "random", id: "anas-fj-job1", workload: "job:1", state: "Running", port: 7002},
		{name: "random label", mode: "random", id: "anas-fj-job1", workload: "job:1", state: "Running", port: 7000, label: "api"},
		{name: "named missing label", mode: "named", id: "anas-fj-job1", workload: "job:1", state: "Running", port: 7000},
		{name: "named path injection", mode: "named", id: "anas-fj-job1", workload: "job:1", state: "Running", port: 7000, label: "../api"},
		{name: "fixed label", mode: "fixed", id: "anas-fj-job1", workload: "job:1", state: "Running", port: 7000, label: "api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher, run, writer := httpTestPublisher(t, tc.mode)
			httpSetInstances(t, run, httpTestInstance(tc.id, tc.workload, tc.state))
			if _, err := publisher.PublishPort(context.Background(), tc.id, tc.port, PublishOptions{Label: tc.label}); err == nil {
				t.Fatal("invalid publication accepted")
			}
			if len(writer.submitted) != 0 {
				t.Fatal("invalid publication reached the request writer")
			}
		})
	}
}

func TestHTTPPublicationReservesOneLocalHostAndCanWithdrawAfterDeletion(t *testing.T) {
	publisher, run, writer := httpTestPublisher(t, "fixed")
	publication, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	httpSetInstances(t, run,
		httpTestInstance("anas-fj-job1", "job:1", "Running"),
		httpTestInstance("anas-fj-job2", "job:2", "Running"),
	)
	if _, err := publisher.PublishPort(context.Background(), "anas-fj-job2", 7000, PublishOptions{}); !errors.Is(err, ErrHTTPPublicationConflict) {
		t.Fatalf("fixed Host collision not rejected: %v", err)
	}
	if _, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7001, PublishOptions{}); !errors.Is(err, ErrHTTPPublicationConflict) {
		t.Fatalf("second backend on same Host not rejected: %v", err)
	}
	run.fail["list"] = true
	callsBefore := len(run.calls)
	if err := publisher.UnpublishPort(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
	if err := publisher.UnpublishPort(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
	if len(run.calls) != callsBefore || writer.withdrawCalls != 1 || len(writer.records) != 0 {
		t.Fatal("withdrawal depended on instance existence or was not idempotent")
	}
}

func TestHTTPPublicationStaleReceiptCannotRemoveReplacement(t *testing.T) {
	publisher, _, writer := httpTestPublisher(t, "random")
	old, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writer.replace = true
	current, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{})
	if err != nil || current == old {
		t.Fatalf("replacement did not receive a new receipt: %v", err)
	}
	if err := publisher.UnpublishPort(context.Background(), old); !errors.Is(err, ErrHTTPPublicationStale) || len(writer.records) != 1 {
		t.Fatalf("stale receipt affected replacement: %v", err)
	}
	if err := publisher.UnpublishPort(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	newer, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{})
	if err != nil || newer == current {
		t.Fatalf("new submission was not independent: %v", err)
	}
	if err := publisher.UnpublishPort(context.Background(), current); err != nil || len(writer.records) != 1 {
		t.Fatal("idempotent old withdrawal deleted the newly submitted request")
	}
}

func TestHTTPPublicationCloseDoesNotClaimOrPerformRevocation(t *testing.T) {
	publisher, _, writer := httpTestPublisher(t, "random")
	publication, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := httpTestPublisher(t, "random")
	if err := other.UnpublishPort(context.Background(), publication); !errors.Is(err, ErrHTTPPublicationStale) {
		t.Fatal("another publisher accepted the receipt")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	if !writer.closed || writer.withdrawCalls != 0 || len(writer.records) != 1 || publisher.key != "" {
		t.Fatal("Close changed durable intents or retained its key reference")
	}
	if _, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{}); !errors.Is(err, ErrHTTPPublicationUnavailable) {
		t.Fatal("closed publisher accepted work")
	}
}

func TestHTTPPublicationPropagatesUncertainCommitAndRetainsFailedWithdrawal(t *testing.T) {
	publisher, _, writer := httpTestPublisher(t, "random")
	writer.submitError = computeingress.ErrRequestCommitUncertain
	if publication, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{}); publication != nil || !errors.Is(err, computeingress.ErrRequestCommitUncertain) {
		t.Fatalf("uncertain write became a receipt: %v", err)
	}
	writer.submitError, writer.nilReceipt = nil, true
	if publication, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{}); publication != nil || !errors.Is(err, computeingress.ErrRequestCommitUncertain) {
		t.Fatalf("missing writer receipt was accepted: %v", err)
	}
	writer.nilReceipt = false
	publication, err := publisher.PublishPort(context.Background(), "anas-fj-job1", 7000, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writer.withdrawError = computeingress.ErrRequestCommitUncertain
	if err := publisher.UnpublishPort(context.Background(), publication); !errors.Is(err, computeingress.ErrRequestCommitUncertain) || len(publisher.active) != 1 {
		t.Fatalf("failed withdrawal lost its receipt: %v", err)
	}
	writer.withdrawError = nil
	if err := publisher.UnpublishPort(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPPublicationAdmissionHonorsCancellation(t *testing.T) {
	publisher, run, writer := httpTestPublisher(t, "random")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := publisher.PublishPort(ctx, "anas-fj-job1", 7000, PublishOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request error = %v", err)
	}
	publisher.gate <- struct{}{}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := publisher.PublishPort(ctx, "anas-fj-job1", 7000, PublishOptions{})
	<-publisher.gate
	if !errors.Is(err, context.DeadlineExceeded) || len(run.calls) != 0 || len(writer.submitted) != 0 {
		t.Fatalf("blocked admission ignored context or performed I/O: %v", err)
	}
}
