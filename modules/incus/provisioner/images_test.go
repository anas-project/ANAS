package main

import (
	"context"
	"strings"
	"testing"
)

func TestEnsureRefusesWrongImageBeforeRegisteringTrust(t *testing.T) {
	for _, field := range []string{"fingerprint", "architecture", "type"} {
		t.Run(field, func(t *testing.T) {
			d := newFakeDaemon(t)
			d.imageFilter = func(image *imageRecord) {
				switch field {
				case "fingerprint":
					image.Fingerprint = strings.Repeat("d", 64)
				case "architecture":
					image.Architecture = "aarch64"
				case "type":
					image.Type = "container"
				}
			}
			if _, err := ensure(context.Background(), d.clientFor(t), testLease(t, "vm")); err == nil {
				t.Fatal("accepted incompatible image")
			}
			if len(d.certificates) > 0 {
				t.Fatal("registered trust after image verification failed")
			}
		})
	}
}
