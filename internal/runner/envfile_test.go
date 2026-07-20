package runner

import (
	"path/filepath"
	"testing"
)

func TestQuoteEnvForms(t *testing.T) {
	cases := map[string]string{
		"":                 `""`,
		"plain":            "plain",
		"nas.example.com":  "nas.example.com",
		"/srv/data:ro":     "/srv/data:ro",
		"has space":        "'has space'",
		"pre#fix":          "'pre#fix'",
		`{"json":"value"}`: `'{"json":"value"}'`,
		"a$b":              "'a$b'",
		"back\\slash":      "'back\\slash'",
		"it's":             `"it's"`,
		"it's $1":          `"it's $$1"`,
		"line\nbreak":      `"line\nbreak"`,
		`mix'ed "q" \ end`: `"mix'ed \"q\" \\ end"`,
	}
	for in, want := range cases {
		if got := quoteEnv(in); got != want {
			t.Errorf("quoteEnv(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestEnvFileRoundTrip(t *testing.T) {
	env := map[string]string{
		"PLAIN":     "value",
		"EMPTY":     "",
		"SPACES":    "a b\tc",
		"SQUOTE":    "it's a 'test'",
		"DQUOTE":    `she said "hi"`,
		"DOLLAR":    "pa$$word${HOME}",
		"NEWLINE":   "first\nsecond\r\nthird",
		"BACKSLASH": `C:\path\to`,
		"HASH":      "abc#def",
		"MIXED":     "'\"$\\\n#`end",
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := writeEnv(path, env); err != nil {
		t.Fatalf("writeEnv: %v", err)
	}
	got, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	if len(got) != len(env) {
		t.Fatalf("round trip key count = %d, want %d", len(got), len(env))
	}
	for k, v := range env {
		if got[k] != v {
			t.Errorf("round trip %s = %q, want %q", k, got[k], v)
		}
	}
}
