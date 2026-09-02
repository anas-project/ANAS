package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesOnlyEmptyTopLevelForm(t *testing.T) {
	action := "https://anas.example:7795" + exchangePath
	request := httptest.NewRequest(http.MethodGet, "https://source.example:7795/", nil)
	recorder := httptest.NewRecorder()
	newHandler(action).ServeHTTP(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("response = %d %#v", response.StatusCode, response.Header)
	}
	for _, want := range []string{
		`method="post"`, `action="` + action + `"`, `name="handoff"`, `type="text"`, `autocomplete="off"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q: %s", want, page)
		}
	}
	if strings.Contains(page, "value=") || strings.Contains(page, "script") {
		t.Fatalf("page contains a credential value or script: %s", page)
	}
	if policy := response.Header.Get("Content-Security-Policy"); !strings.Contains(policy, "form-action https://anas.example:7795") {
		t.Fatalf("Content-Security-Policy = %q", policy)
	}
	if policy := response.Header.Get("Referrer-Policy"); policy != "strict-origin" {
		t.Fatalf("Referrer-Policy = %q, want strict-origin", policy)
	}
}

func TestHandlerRejectsNonGETAndUnexpectedURL(t *testing.T) {
	handler := newHandler("https://anas.example" + exchangePath)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "https://source.example/", nil),
		httptest.NewRequest(http.MethodGet, "https://source.example/other", nil),
		httptest.NewRequest(http.MethodGet, "https://source.example/?query=forbidden", nil),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code < 400 {
			t.Fatalf("%s %s status = %d", request.Method, request.URL, recorder.Code)
		}
	}
}

func TestValidateFormAction(t *testing.T) {
	valid := "https://anas.example:7795" + exchangePath
	if got, err := validateFormAction(valid); err != nil || got != valid {
		t.Fatalf("valid action = %q, %v", got, err)
	}
	for _, invalid := range []string{
		"http://anas.example" + exchangePath,
		"https://user@anas.example" + exchangePath,
		"https://anas.example/other",
		valid + "?handoff=forbidden",
		valid + "#fragment",
	} {
		if _, err := validateFormAction(invalid); err == nil {
			t.Fatalf("validateFormAction(%q) succeeded", invalid)
		}
	}
}
