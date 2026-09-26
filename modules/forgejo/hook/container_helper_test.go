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
