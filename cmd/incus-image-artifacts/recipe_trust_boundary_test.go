package main

import (
	"context"
	"errors"
	"testing"
)

func TestDebianTrustPreflightRejectsAliasedAndDuplicateSourceIdentity(t *testing.T) {
	for _, recipe := range []string{
		"image: {distribution: debian}\nSource: {downloader: debootstrap}\n",
		"image: {distribution: debian, Distribution: ubuntu}\nsource: {downloader: debootstrap}\n",
		"image: {distribution: debian}\nsource: {downloader: debootstrap, Downloader: other}\n",
		"image: {distribution: debian}\nsource: {downloader: debootstrap, downloader: other}\n",
		"image: {distribution: debian}\nsource: &source {downloader: debootstrap, skip_verification: false}\nsource: *source\n",
		"image: {distribution: debian}\ndefaults: &defaults {downloader: debootstrap}\nsource: {<<: *defaults}\n",
		"image: {distribution: debian}\nsource: {downloader: debootstrap, skip_verification: 'false'}\n",
	} {
		calls := 0
		err := validateRecipeBuildTrust(context.Background(), []byte(recipe), func(context.Context) error {
			calls++
			return nil
		})
		if err == nil || calls != 0 {
			t.Fatalf("ambiguous recipe reached keyring validation: calls=%d error=%v", calls, err)
		}
	}
}

func TestDebianTrustPreflightRetainsShorterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := validateRecipeBuildTrust(ctx, []byte(debianBootstrapRecipe), func(received context.Context) error {
		if received != ctx {
			t.Fatal("keyring inspection detached from the caller")
		}
		cancel()
		return errors.New("private inspection error")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inspection authorized a build or hid cancellation: %v", err)
	}
}

func TestDebianTrustPreflightRequiresKeyringEvenWithExplicitVerification(t *testing.T) {
	calls := 0
	err := validateRecipeBuildTrust(context.Background(), []byte(debianBootstrapRecipe+"  skip_verification: false\n"), func(context.Context) error {
		calls++
		return errDebianBootstrapTrust
	})
	if calls != 1 || !errors.Is(err, errDebianBootstrapTrust) {
		t.Fatal("a recipe's verification declaration replaced the actual keyring prerequisite", err)
	}
}
