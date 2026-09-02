package webui

import (
	"bytes"
	"errors"
	"testing"
)

func TestEmbeddedBundlesAreClosedAndIndependent(t *testing.T) {
	tests := []struct {
		name        Asset
		contentType string
		contains    []byte
	}{
		{MainIndex, "text/html; charset=utf-8", []byte(`src="/assets/main.js"`)},
		{MainJavaScript, "text/javascript; charset=utf-8", []byte("ANAS")},
		{MainStyles, "text/css; charset=utf-8", []byte("lan-warning")},
		{RecoveryIndex, "text/html; charset=utf-8", []byte("data-emergency-ui")},
		{RecoveryScript, "text/javascript; charset=utf-8", []byte("/healthz")},
		{RecoveryStyles, "text/css; charset=utf-8", []byte("ui-monospace")},
	}
	for _, test := range tests {
		content, err := Read(test.name)
		if err != nil {
			t.Fatalf("Read(%q): %v", test.name, err)
		}
		if content.ContentType != test.contentType || !bytes.Contains(content.Body, test.contains) {
			t.Fatalf("Read(%q) = type %q body length %d", test.name, content.ContentType, len(content.Body))
		}
	}

	main, _ := Read(MainJavaScript)
	recovery, _ := Read(RecoveryScript)
	if bytes.Contains(recovery.Body, []byte("createApp")) || len(recovery.Body) >= len(main.Body)/4 {
		t.Fatalf("recovery bundle is not independent: main=%d recovery=%d", len(main.Body), len(recovery.Body))
	}
	if _, err := Read(Asset("../../secret")); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("unknown asset error = %v", err)
	}
}
