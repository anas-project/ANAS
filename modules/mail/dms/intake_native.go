package dms

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"regexp"
	"strings"
	"time"
)

const intakeHeader = "X-ANAS-Ingress-Key"
const intakeHeaderChecks = `/^X-ANAS-Ingress-Key:/ IGNORE
`
const intakeTrustedHeaderChecks = `/^X-ANAS-Ingress-Key: [a-f0-9]{64}$/ WARN ANAS ingress trace
`

// Ordinary SMTP must never supply a trusted cloud intake trace. The cloud
// adapter uses a loopback-only SMTP listener with a separate cleanup service;
// both cleanup paths retain the existing native header policies. This script
// runs before Postfix and also recognizes the prefix from earlier versions.
const intakeStartup = `
anas_intake_checks=$(/usr/sbin/postconf -h header_checks)
case "$anas_intake_checks" in
  'regexp:/etc/postfix/anas-intake-headers') anas_intake_checks= ;;
  'regexp:/etc/postfix/anas-intake-headers, '*) anas_intake_checks=${anas_intake_checks#regexp:/etc/postfix/anas-intake-headers, } ;;
  *regexp:/etc/postfix/anas-intake-headers*) exit 1 ;;
esac
/usr/sbin/postconf -e "header_checks = regexp:/etc/postfix/anas-intake-headers${anas_intake_checks:+, $anas_intake_checks}"
/usr/sbin/postconf -M '127.0.0.1:2525/inet=127.0.0.1:2525 inet n - n - - smtpd'
/usr/sbin/postconf -M 'anas_intake_cleanup/unix=anas_intake_cleanup unix n - n - 0 cleanup'
/usr/sbin/postconf -P '127.0.0.1:2525/inet/cleanup_service_name=anas_intake_cleanup'
# Pin the expanded filter list on the private listener. A filter outage must
# defer delivery; inheriting the image's default "accept" bypasses filtering.
anas_intake_milters=$(/usr/sbin/postconf -xh smtpd_milters)
test -n "$anas_intake_milters" || exit 1
/usr/sbin/postconf -P "127.0.0.1:2525/inet/smtpd_milters=$anas_intake_milters"
/usr/sbin/postconf -P '127.0.0.1:2525/inet/milter_default_action=tempfail'
/usr/sbin/postconf -P '127.0.0.1:2525/inet/receive_override_options='
/usr/sbin/postconf -e "anas_intake_header_checks = regexp:/etc/postfix/anas-intake-trusted-headers${anas_intake_checks:+, $anas_intake_checks}"
/usr/sbin/postconf -P 'anas_intake_cleanup/unix/header_checks=$anas_intake_header_checks'
/usr/sbin/postconf -P 'anas_intake_cleanup/unix/syslog_name=postfix'
`

var queuedAs = regexp.MustCompile(`(?i)queued as ([A-Za-z0-9]{5,64})(?:\s|$)`)
var storedMailLine = regexp.MustCompile(`^[a-fA-F0-9]{32} [0-9]+$`)
var cleanupTrace = regexp.MustCompile(`postfix/cleanup\[[0-9]+\]: ([A-Za-z0-9]{5,64}): warning: header X-ANAS-Ingress-Key: ([a-f0-9]{64})(?:\s|$)`)

// A failed user-patches.sh does not necessarily stop the upstream container.
// Never submit or confirm a cloud message unless both reserved-header paths
// and the loopback cleanup binding have actually been configured.
func nativeIntakeProtected(ctx context.Context) bool {
	for path, expected := range map[string]string{
		"/etc/postfix/anas-intake-headers":         intakeHeaderChecks,
		"/etc/postfix/anas-intake-trusted-headers": intakeTrustedHeaderChecks,
	} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() != int64(len(expected)) {
			return false
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			return false
		}
	}
	for key, prefix := range map[string]string{
		"header_checks":             "regexp:/etc/postfix/anas-intake-headers",
		"anas_intake_header_checks": "regexp:/etc/postfix/anas-intake-trusted-headers",
	} {
		out, err := command(ctx, "/usr/sbin/postconf", "-h", key)
		value := strings.TrimSpace(out)
		if err != nil || value != prefix && !strings.HasPrefix(value, prefix+", ") {
			return false
		}
	}
	return nativeIntakeFilterProtected(ctx, command)
}

// Per-filter default_action overrides the service policy. Until such overrides
// have been verified, accept only a nonempty list of plain filter endpoints.
// Compare the expanded global list so a later filter change requires restart.
func nativeIntakeFilterProtected(ctx context.Context, run func(context.Context, string, ...string) (string, error)) bool {
	filters, err := run(ctx, "/usr/sbin/postconf", "-xh", "smtpd_milters")
	filters = strings.TrimSpace(filters)
	if err != nil || filters == "" || strings.ContainsAny(filters, "{}$\r\n") {
		return false
	}
	endpoints := strings.FieldsFunc(filters, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	if len(endpoints) == 0 {
		return false
	}
	for _, endpoint := range endpoints {
		if len(endpoint) <= 5 || !strings.HasPrefix(endpoint, "inet:") && !strings.HasPrefix(endpoint, "unix:") || strings.Contains(endpoint, "=") {
			return false
		}
	}
	want := map[string]string{
		"127.0.0.1:2525/inet/cleanup_service_name":     "anas_intake_cleanup",
		"127.0.0.1:2525/inet/smtpd_milters":            filters,
		"127.0.0.1:2525/inet/milter_default_action":    "tempfail",
		"127.0.0.1:2525/inet/receive_override_options": "",
		"anas_intake_cleanup/unix/header_checks":       "$anas_intake_header_checks",
		"anas_intake_cleanup/unix/syslog_name":         "postfix",
	}
	out, err := run(ctx, "/usr/sbin/postconf", "-P", "127.0.0.1:2525/inet/cleanup_service_name", "anas_intake_cleanup/unix/header_checks", "anas_intake_cleanup/unix/syslog_name", "127.0.0.1:2525/inet/smtpd_milters", "127.0.0.1:2525/inet/milter_default_action", "127.0.0.1:2525/inet/receive_override_options")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		expected, exists := want[key]
		if !ok || !exists || expected != value {
			return false
		}
		delete(want, key)
	}
	return len(want) == 0
}

// Preserve the archived original. Only the transport copy replaces reserved
// trace headers, including folded fields, so message headers cannot forge a
// source/target or another intake's native receipt.
func intakeTransport(raw io.Reader, id string) (io.Reader, error) {
	reader := bufio.NewReader(raw)
	var headers bytes.Buffer
	headers.WriteString(intakeHeader + ": " + id + "\r\n")
	skip := false
	size := 0
	for {
		line, err := reader.ReadString('\n')
		size += len(line)
		if err != nil || size > 256<<10 {
			return nil, ErrConflict
		}
		if line == "\n" || line == "\r\n" {
			headers.WriteString("\r\n")
			break
		}
		if line[0] != ' ' && line[0] != '\t' {
			name, _, _ := strings.Cut(line, ":")
			skip = strings.EqualFold(strings.TrimSpace(name), intakeHeader)
		}
		if !skip {
			headers.WriteString(line)
		}
	}
	return io.MultiReader(&headers, reader), nil
}

func nativeIntakeFailure(check string, err error, uncertain bool) intakeSubmission {
	result := intakeSubmission{State: "retryable", Check: check}
	if uncertain {
		result.State = "unknown"
	}
	var rejected *textproto.Error
	if errors.As(err, &rejected) {
		result.SMTPCode = rejected.Code
		result.State = "retryable"
		if rejected.Code >= 500 {
			result.State = "rejected"
		}
	}
	return result
}
func nativeIntakeSubmit(ctx context.Context, r intakeRecord, user string, raw io.Reader) intakeSubmission {
	if !nativeIntakeProtected(ctx) {
		return intakeSubmission{State: "retryable", Check: "native-delivery"}
	}
	body, err := intakeTransport(raw, r.Metadata.Key())
	if err != nil {
		return intakeSubmission{State: "rejected", Check: "message-headers"}
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:2525")
	if err != nil {
		return nativeIntakeFailure("smtp-connect", err, false)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	client, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		return nativeIntakeFailure("smtp-greeting", err, false)
	}
	defer client.Close()
	if err = client.Hello("localhost.localdomain"); err != nil {
		return nativeIntakeFailure("smtp-hello", err, false)
	}
	// The cloud has already accepted this message for an immutable mailbox.
	// A later local rejection must not resend its content to the original
	// envelope address, which may now belong to somebody else. A null reverse
	// path suppresses native DSNs/MDNs (RFC 5429); original envelope metadata
	// and archived MIME bytes remain unchanged. Authenticated outbound SMTP is
	// a separate path and keeps its real sender.
	if err = client.Mail(""); err != nil {
		return nativeIntakeFailure("smtp-sender", err, false)
	}
	if err = client.Rcpt(user); err != nil {
		return nativeIntakeFailure("smtp-recipient", err, false)
	}
	if err = client.Text.PrintfLine("DATA"); err != nil {
		return nativeIntakeFailure("smtp-data", err, false)
	}
	if _, _, err = client.Text.ReadResponse(354); err != nil {
		return nativeIntakeFailure("smtp-data", err, false)
	}
	writer := client.Text.DotWriter()
	if _, err = io.Copy(writer, body); err != nil {
		return nativeIntakeFailure("smtp-body", err, false)
	}
	// After the terminator may have reached Postfix, a transport failure is
	// uncertain. Only an actual SMTP refusal proves safe failure.
	if err = writer.Close(); err != nil {
		return nativeIntakeFailure("smtp-ack", err, true)
	}
	_, response, err := client.Text.ReadResponse(250)
	if err != nil {
		return nativeIntakeFailure("smtp-ack", err, true)
	}
	match := queuedAs.FindStringSubmatch(response)
	if len(match) != 2 {
		return intakeSubmission{State: "unknown", Check: "smtp-ack", SMTPCode: 250}
	}
	return intakeSubmission{State: "queued", QueueID: match[1], Check: "native-delivery", SMTPCode: 250}
}

// Native observations only. No match, unavailable daemons, rotated/missing
// evidence or a DATA acknowledgement lost in transit remain unknown. There is
// no inference that a missing queue item means delivery.
func nativeIntakeObserve(ctx context.Context, r intakeRecord, user string) (string, string) {
	if !nativeIntakeProtected(ctx) {
		return "unknown", r.QueueID
	}
	return observeNativeIntake(ctx, r, user, command, intakeLogs)
}

// Logs can help locate a queue candidate after a lost SMTP acknowledgement,
// but a queue ID is reusable and does not bind a later delivery log to the
// intake. Only the reserved header in the live queue or the actual mailbox is
// used as evidence here. A Sieve discard or rejection with no retained copy
// therefore stays unknown until a trustworthy per-message result is available.
func observeNativeIntake(ctx context.Context, r intakeRecord, user string, run func(context.Context, string, ...string) (string, error), logs func() string) (string, string) {
	id := r.Metadata.Key()
	mail, err := run(ctx, "/usr/bin/doveadm", "search", "-u", user, "HEADER", intakeHeader, id)
	if err == nil {
		lines := strings.FieldsFunc(strings.TrimSpace(mail), func(c rune) bool { return c == '\n' || c == '\r' })
		if len(lines) == 1 && storedMailLine.MatchString(lines[0]) {
			return "stored", r.QueueID
		}
		if len(lines) > 1 {
			return "unknown", r.QueueID
		}
	}
	// A trace is only a lookup hint: historical or later entries may use the
	// same queue ID for an unrelated message.
	queueID := r.QueueID
	for _, match := range cleanupTrace.FindAllStringSubmatch(logs(), -1) {
		if match[2] == id {
			queueID = match[1]
		}
	}
	queue, err := run(ctx, "/usr/sbin/postqueue", "-j")
	if err == nil {
		decoder := json.NewDecoder(strings.NewReader(queue))
		inspected := 0
		for count := 0; count < 10000; count++ {
			if ctx.Err() != nil || inspected >= 16 {
				break
			}
			var item struct {
				ID string `json:"queue_id"`
			}
			if err = decoder.Decode(&item); err != nil {
				break
			}
			if !postfixQueueID.MatchString(item.ID) {
				return "unknown", queueID
			}
			if queueID != "" && item.ID != queueID {
				continue
			}
			inspected++
			headers, err := run(ctx, "/usr/sbin/postcat", "-h", "-q", item.ID)
			if err == nil {
				for _, line := range strings.Split(headers, "\n") {
					if strings.TrimSpace(line) == intakeHeader+": "+id {
						return "queued", item.ID
					}
				}
			}
		}
	}
	return "unknown", queueID
}
func intakeLogs() string {
	var result strings.Builder
	for _, path := range []string{"/var/log/mail/mail.log.1", "/var/log/mail/mail.log"} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if info.Size() > 4<<20 {
			_, err = f.Seek(info.Size()-(4<<20), io.SeekStart)
		}
		if err == nil {
			raw, e := io.ReadAll(io.LimitReader(f, 4<<20))
			if e == nil {
				result.Write(raw)
				result.WriteByte('\n')
			}
		}
		f.Close()
	}
	return result.String()
}
