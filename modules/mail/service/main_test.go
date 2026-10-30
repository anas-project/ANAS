package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFoundationDoesNotPublishReadyOrMailAccess(t *testing.T) {
	for _, path := range []string{"/readyz", "/mailboxes", "/login", "/oidc/callback", "/notify"} {
		w := httptest.NewRecorder()
		handler(nil, nil).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		want := 404
		if path == "/readyz" {
			want = 503
		}
		if w.Code != want {
			t.Fatalf("%s status %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "password") {
			t.Fatal("credential exposed")
		}
	}
}
func TestDatabaseBindingFailureDoesNotExposeValues(t *testing.T) {
	for _, key := range []string{"HOST", "PORT", "NAME", "USERNAME", "PASSWORD"} {
		t.Setenv("MAIL_DB_"+key, "")
	}
	t.Setenv("MAIL_DB_PASSWORD", "synthetic-secret-never-log")
	_, e := database()
	if e == nil || strings.Contains(e.Error(), "synthetic-secret") {
		t.Fatal("invalid binding or secret error handling")
	}
}
