package main

import (
	"strings"
	"sync"
)

// Redactor removes known secret values from anything on its way out of the
// process: log lines, database rows, issue comments. It is deliberately a
// value-based scrubber rather than a set of careful call sites, because the
// requirement is that a credential never appears in an issue, a comment, a log,
// an image or a deployment manifest (AGENT-R-010, AGENT-R-056) -- and a rule
// enforced only by care at the call site is not enforced.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

// Placeholder is what a redacted value is replaced with. It is a fixed string
// so a reader can tell redaction happened rather than seeing a truncated value
// and assuming corruption.
const Placeholder = "[redacted]"

// minSecretLength keeps short values out of the scrubber. Redacting a two-
// character secret would blank out unrelated text everywhere it happened to
// occur, which destroys the record without protecting anything: a value that
// short is guessable regardless.
const minSecretLength = 8

// NewRedactor builds a scrubber over the given values. Empty and very short
// values are ignored.
func NewRedactor(values ...string) *Redactor {
	r := &Redactor{}
	r.Add(values...)
	return r
}

// Add registers more secrets, for credentials minted after start-up such as an
// agent's freshly issued token.
func (r *Redactor) Add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) < minSecretLength {
			continue
		}
		if r.containsLocked(value) {
			continue
		}
		r.secrets = append(r.secrets, value)
	}
}

func (r *Redactor) containsLocked(value string) bool {
	for _, secret := range r.secrets {
		if secret == value {
			return true
		}
	}
	return false
}

// String returns text with every registered secret replaced.
func (r *Redactor) String(text string) string {
	if text == "" {
		return text
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, secret := range r.secrets {
		text = strings.ReplaceAll(text, secret, Placeholder)
	}
	return text
}

// Error scrubs an error's message. Errors are the most common way a credential
// escapes: an HTTP client that echoes a request URL, a driver that prints a
// connection string.
func (r *Redactor) Error(err error) string {
	if err == nil {
		return ""
	}
	return r.String(err.Error())
}
