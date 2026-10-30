package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) config {
	t.Helper()
	c := config{Source: "cloud-test", Token: strings.Repeat("x", 64), Root: t.TempDir(), Routes: map[string]string{"a@relay.invalid": "box-a"}}
	for _, d := range []string{"mail", "staging"} {
		if e := os.Mkdir(filepath.Join(c.Root, d), 0700); e != nil {
			t.Fatal(e)
		}
	}
	return c
}
func TestPublishedSourceKeepsExactRawAndTrustedTarget(t *testing.T) {
	c := fixture(t)
	raw := []byte("To: forged@example.org\r\n\r\n\x00\xff\r\n")
	id, e := c.accept("a@relay.invalid", "sender@example.net", "QUEUE0001", bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	stored, e := os.ReadFile(filepath.Join(c.directory(id), "raw.eml"))
	if e != nil || !bytes.Equal(stored, raw) {
		t.Fatal("raw changed")
	}
	m, e := c.metadata(id)
	if e != nil || m.Target != "box-a" || m.Delivered || m.Size != int64(len(raw)) {
		t.Fatalf("metadata: %+v %v", m, e)
	}
	if _, e = c.accept("unknown@relay.invalid", "sender", "QUEUE0001", bytes.NewReader(raw)); e == nil {
		t.Fatal("unknown recipient accepted")
	}
	entries, _ := os.ReadDir(filepath.Join(c.Root, "mail"))
	if len(entries) != 1 {
		t.Fatal("unexpected published object")
	}
	staging, _ := os.ReadDir(filepath.Join(c.Root, "staging"))
	if len(staging) != 0 {
		t.Fatal("staging leaked")
	}
}
func TestSourceAuthenticationScopeAndImmutableReceipt(t *testing.T) {
	c := fixture(t)
	id, e := c.accept("a@relay.invalid", "sender", "QUEUE0002", strings.NewReader("Subject: test\r\n\r\nbody"))
	if e != nil {
		t.Fatal(e)
	}
	h := c.handler()
	call := func(path, method, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("/messages", "GET", "wrong", ""); w.Code != 401 {
		t.Fatal("unauthenticated read accepted")
	}
	path := "/messages/" + id + "/ack"
	for _, body := range []string{`{"source":"other","target":"box-a","receipt":"one"}`, `{"source":"cloud-test","target":"box-b","receipt":"one"}`, `{"source":"cloud-test","target":"box-a","receipt":"one"} {}`} {
		if w := call(path, "POST", c.Token, body); w.Code != 400 {
			t.Fatal("invalid receipt accepted")
		}
	}
	for _, receipt := range []string{"one", "two"} {
		body := `{"source":"cloud-test","target":"box-a","receipt":"` + receipt + `"}`
		if w := call(path, "POST", c.Token, body); w.Code != 200 {
			t.Fatalf("receipt %d %s", w.Code, w.Body)
		}
	}
	b, e := os.ReadFile(filepath.Join(c.directory(id), "ack.json"))
	if e != nil {
		t.Fatal(e)
	}
	var receipt map[string]any
	if json.Unmarshal(b, &receipt) != nil || receipt["receipt"] != "one" {
		t.Fatal("first receipt overwritten")
	}
	w := call("/messages?limit=2", "GET", c.Token, "")
	var p struct{ Messages []message }
	json.Unmarshal(w.Body.Bytes(), &p)
	if len(p.Messages) != 1 || !p.Messages[0].Delivered {
		t.Fatal("completed source missing from recovery list")
	}
	w = call("/messages/"+id+"/raw", "GET", c.Token, "")
	body, _ := io.ReadAll(w.Body)
	if !bytes.Contains(body, []byte("body")) {
		t.Fatal("receipt deleted source raw")
	}
}

func TestQueueRetryReusesPublicationAndRejectsChangedBytes(t *testing.T) {
	c := fixture(t)
	raw := []byte("Message-ID: <same@example.org>\r\n\r\nbody")
	id, e := c.accept("a@relay.invalid", "sender", "QUEUE0010", bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		again, e := c.accept("a@relay.invalid", "sender", "QUEUE0010", bytes.NewReader(raw))
		if e != nil || again != id {
			t.Fatalf("retry changed identity: %s %v", again, e)
		}
	}
	if _, e = c.accept("a@relay.invalid", "sender", "QUEUE0010", strings.NewReader("different")); e == nil {
		t.Fatal("changed bytes accepted for same queue identity")
	}
	other, e := c.accept("a@relay.invalid", "sender", "QUEUE0011", bytes.NewReader(raw))
	if e != nil || other == id {
		t.Fatal("new SMTP queue item was merged by message content")
	}
	rows, _ := os.ReadDir(filepath.Join(c.Root, "mail"))
	if len(rows) != 2 {
		t.Fatal("unexpected publication count")
	}
}
