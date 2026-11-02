package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestContainerHelperCancellationReachesTheActualProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	body, err := boundedContainerHelper(ctx, nil, "/bin/sleep", "30")
	if !errors.Is(err, context.DeadlineExceeded) || body != nil || time.Since(started) > 3*time.Second {
		t.Fatal("container helper did not observe the actual caller deadline", err)
	}
	if _, err := boundedContainerHelper(ctx, nil, "/nonexistent-never-executed"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("expired caller attempted to launch another helper", err)
	}
}

func TestContainerHelperBoundsOutputAndDiscardsFailureText(t *testing.T) {
	var output helperOutput
	if _, err := output.Write([]byte(strings.Repeat("x", 64<<10))); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("private-response")); err == nil || output.Len() != 64<<10 {
		t.Fatal("unbounded helper output")
	}
	body, err := boundedContainerHelper(context.Background(), nil, "/bin/sh", "-c", "printf 'private-upstream'; exit 1")
	if err == nil || body != nil || strings.Contains(err.Error(), "private-upstream") {
		t.Fatal("failed helper disclosed raw response")
	}
}

func TestContainerHelperLongBudgetOnlyForFixedOIDCInvocation(t *testing.T) {
	args := []string{"exec", "-i", "--user", "1000:1000", "owned_forgejo", "/usr/local/bin/anas-forgejo-entrypoint", "oidc"}
	if got := containerHelperBudget("docker", args); got != 180*time.Second {
		t.Fatalf("fixed OIDC allowance = %s", got)
	}
	for index := range args {
		changed := append([]string(nil), args...)
		changed[index] = "untrusted"
		if got := containerHelperBudget("docker", changed); got != 45*time.Second {
			t.Fatalf("modified argument %d selected long allowance: %s", index, got)
		}
	}
	for _, target := range []string{"forgejo", "../owned_forgejo", "owned_forgejo;true", "-option_forgejo"} {
		changed := append([]string(nil), args...)
		changed[4] = target
		if got := containerHelperBudget("docker", changed); got != 45*time.Second {
			t.Fatalf("invalid target selected long allowance: %s", got)
		}
	}
	if containerHelperBudget("other", args) != 45*time.Second ||
		containerHelperBudget("docker", append(args, "extra")) != 45*time.Second {
		t.Fatal("unrelated command selected long allowance")
	}
	if containerHelperBudget("docker", []string{"stop", "--time", "125", strings.Repeat("a", 64)}) != 135*time.Second {
		t.Fatal("graceful stop allowance changed")
	}
}

func TestOIDCHelperReportsOnlyClosedFailureReasons(t *testing.T) {
	for _, message := range []string{
		"list Forgejo authentication sources failed",
		"reconcile Forgejo OIDC source failed",
		"verify Forgejo OIDC source failed",
		"Forgejo OIDC source is missing, disabled, or has the wrong type after reconciliation",
	} {
		if err := oidcHelperFailure([]byte("anas-forgejo: " + message + "\n")); !errors.Is(err, errContainerHelper) || !strings.Contains(err.Error(), message) {
			t.Fatal("fixed failure reason was lost", err)
		}
		for _, body := range []string{message, "anas-forgejo: " + message + "\nprivate-upstream", "private-upstream\nanas-forgejo: " + message + "\n"} {
			if err := oidcHelperFailure([]byte(body)); err != errContainerHelper {
				t.Fatal("unknown response crossed the private-output boundary", err)
			}
		}
	}
}
